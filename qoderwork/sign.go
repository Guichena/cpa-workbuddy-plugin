// sign.go implements Qoder's COSY request signing: RSA-wrapped AES session
// key + AES-128-CBC encrypted identity + MD5 request signature.
//
// Pure-Go port of the algorithm validated by reference_impl.py against the
// live gateway (qwen3.8-max returned 200 with a real completion).
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// serverPubKeyPEM is Qoder's RSA public key, hardcoded in the desktop client
// (/tmp/qw_extract/.../main.js). Used to wrap the per-session AES key.
const serverPubKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

var serverPubKey *rsa.PublicKey

func init() {
	block, _ := pem.Decode([]byte(serverPubKeyPEM))
	if block == nil {
		panic("bad server pubkey PEM")
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic(err)
	}
	serverPubKey = k.(*rsa.PublicKey)
}

// cosySession holds the per-process signing state. One session is created per
// auth credential at first use and reused until the process exits or the
// underlying jobToken is refreshed (which rebuilds `info`).
type cosySession struct {
	MachineID    string `json:"machine_id"`
	MachineToken string `json:"machine_token"`
	MachineType  string `json:"machine_type"`
	TempKey      string `json:"temp_key"` // 16 ASCII chars, AES-128 key
	CosyKey      string `json:"cosy_key"` // base64(RSA(tempKey))
	Info         string `json:"info"`     // base64(AES-CBC(identityJSON, tempKey))
}

// cosyIdentity is the plaintext inside `info`. All fields required by server.
type cosyIdentity struct {
	Name               string `json:"name"`
	AID                string `json:"aid"`
	UID                string `json:"uid"`
	YxUID              string `json:"yx_uid"`
	OrganizationID     string `json:"organization_id"`
	OrganizationName   string `json:"organization_name"`
	UserType           string `json:"user_type"`
	SecurityOauthToken string `json:"security_oauth_token"`
	RefreshToken       string `json:"refresh_token"`
}

// jsonSortedCompact marshals a map with sorted keys and no whitespace,
// matching the JS/Python compact form the server expects.
func jsonSortedCompact(m map[string]string) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(m[k])
		sb.Write(kb)
		sb.WriteByte(':')
		sb.Write(vb)
	}
	sb.WriteByte('}')
	return []byte(sb.String())
}

// aesCBCEncrypt encrypts plain with AES-128-CBC, key=iv=tempKey (16 bytes).
func aesCBCEncrypt(plain, tempKey []byte) ([]byte, error) {
	block, err := aes.NewCipher(tempKey)
	if err != nil {
		return nil, err
	}
	// PKCS7 pad
	padLen := aes.BlockSize - len(plain)%aes.BlockSize
	padded := make([]byte, len(plain)+padLen)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, tempKey).CryptBlocks(out, padded)
	return out, nil
}

// cosyFingerprintSeed 返回设备指纹派生种子：优先账号 UID，缺失时回退访问令牌。
// [参数] id: 账号身份。
// [返回] 非空种子字符串（两者皆空时为固定占位）。
// 最近修改时间：2026-10-17 02:40:00 新增，用于稳定派生设备标识。
func cosyFingerprintSeed(id cosyIdentity) string {
	// 1. 优先 UID，其次访问令牌，均缺失时用占位保证不 panic
	if uid := strings.TrimSpace(id.UID); uid != "" {
		return uid
	}
	if tok := strings.TrimSpace(id.SecurityOauthToken); tok != "" {
		return tok
	}
	return "anonymous"
}

// deriveMachineID 由账号种子稳定派生 UUID 形态的设备 ID（同一账号恒定）。
// [参数] seed: 账号种子（见 cosyFingerprintSeed）。
// [返回] UUID 字符串。
// 最近修改时间：2026-10-17 02:40:00 新增，替换原随机 UUID 生成。
func deriveMachineID(seed string) string {
	// 1. 用 SHA1 命名空间 UUID 保持上游可能期望的 UUID 形态
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("qoderwork:machine:"+seed)).String()
}

// deriveMachineToken 由账号种子稳定派生 machineToken（base64url 外观，43 字符）。
// [参数] seed: 账号种子。
// [返回] base64url 编码的 43 字符令牌。
// 最近修改时间：2026-10-17 02:40:00 新增，替换原随机字节串。
func deriveMachineToken(seed string) string {
	// 1. SHA-512 取摘要后按 base64url 编码并去填充，与既有随机令牌长度对齐
	sum := sha512.Sum512([]byte("qoderwork:machinetoken:" + seed))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:43]
}

// deriveMachineType 由账号种子稳定派生 18 位十六进制 machineType。
// [参数] seed: 账号种子。
// [返回] 18 位十六进制字符串。
// 最近修改时间：2026-10-17 02:40:00 新增，替换原随机 UUID 截断。
func deriveMachineType(seed string) string {
	// 1. SHA-256 十六进制取前 18 位，与既有 UUID 去横线截断长度一致
	sum := sha256.Sum256([]byte("qoderwork:machinetype:" + seed))
	return hex.EncodeToString(sum[:])[:18]
}

// newCosySession 为给定身份构造一次 COSY 签名会话（设备标识按账号派生、会话密钥随机）。
// [参数] id: 账号身份（UID 用于派生设备标识）。
// [返回] 新建的会话指针；RSA/AES 失败时返回错误。
// 最近修改时间：2026-10-17 02:40:00；改动原因：设备标识改按账号稳定派生，消除会话重建导致的指纹漂移。
func newCosySession(id cosyIdentity) (*cosySession, error) {
	// 1. 设备标识按账号稳定派生，保证同一账号跨会话恒定、多账号互相隔离
	seed := cosyFingerprintSeed(id)
	machineID := deriveMachineID(seed)
	machineToken := deriveMachineToken(seed)
	machineType := deriveMachineType(seed)

	// 2. 会话密钥保持随机（非设备标识，仅用于加密身份载荷）
	tempKey := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	cosyKeyBytes, err := rsa.EncryptPKCS1v15(rand.Reader, serverPubKey, []byte(tempKey))
	if err != nil {
		return nil, fmt.Errorf("rsa encrypt: %w", err)
	}
	cosyKey := base64.StdEncoding.EncodeToString(cosyKeyBytes)

	// 3. 组装身份体并按会话密钥加密为 info 字段
	identityMap := map[string]string{
		"name":                 id.Name,
		"aid":                  id.AID,
		"uid":                  id.UID,
		"yx_uid":               id.YxUID,
		"organization_id":      id.OrganizationID,
		"organization_name":    id.OrganizationName,
		"user_type":            id.UserType,
		"security_oauth_token": id.SecurityOauthToken,
		"refresh_token":        id.RefreshToken,
	}
	identityJSON := jsonSortedCompact(identityMap)
	infoBytes, err := aesCBCEncrypt(identityJSON, []byte(tempKey))
	if err != nil {
		return nil, fmt.Errorf("aes encrypt: %w", err)
	}
	info := base64.StdEncoding.EncodeToString(infoBytes)

	// 4. 组装会话结构返回
	return &cosySession{
		MachineID:    machineID,
		MachineToken: machineToken,
		MachineType:  machineType,
		TempKey:      tempKey,
		CosyKey:      cosyKey,
		Info:         info,
	}, nil
}

// buildBearer constructs the Authorization header for one request.
// pathSig is url.Path with the "/algo" prefix stripped; body is the raw
// (already QoderEncoding-encoded) request body string.
func (s *cosySession) buildBearer(body, rawURL string) (payloadB64, date, bearer string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", "", err
	}
	pathSig := u.Path
	if strings.HasPrefix(pathSig, "/algo") {
		pathSig = pathSig[len("/algo"):]
	}
	payload := map[string]string{
		"cosyVersion": "0.1.43",
		"ideVersion":  "",
		"info":        s.Info,
		"requestId":   uuid.NewString(),
		"version":     "v1",
	}
	payloadB64 = base64.StdEncoding.EncodeToString(jsonSortedCompact(payload))
	date = fmt.Sprintf("%d", time.Now().Unix())
	sum := md5.Sum([]byte(payloadB64 + "\n" + s.CosyKey + "\n" + date + "\n" + body + "\n" + pathSig))
	sig := hex.EncodeToString(sum[:])
	bearer = "Bearer COSY." + payloadB64 + "." + sig
	return payloadB64, date, bearer, nil
}

// headers returns the full header set for one inference request.
// extra headers (x-model-key, x-model-source) are merged in by the caller.
func (s *cosySession) headers(uid, body, rawURL, accept string, sse bool) (map[string]string, error) {
	_, date, bearer, err := s.buildBearer(body, rawURL)
	if err != nil {
		return nil, err
	}
	h := map[string]string{
		"cosy-data-policy":  "AGREE",
		"content-type":      "application/json",
		"cosy-machinetype":  s.MachineType,
		"cosy-clienttype":   "5",
		"cosy-date":         date,
		"cosy-user":         uid,
		"cosy-key":          s.CosyKey,
		"accept":            accept,
		"cosy-clientip":     "169.254.198.161",
		"authorization":     bearer,
		"accept-encoding":   "identity",
		"cosy-version":      "0.1.43",
		"cosy-machineid":    s.MachineID,
		"cosy-machinetoken": s.MachineToken,
		"login-version":     "v2",
		"user-agent":        "Go-http-client/2.0",
	}
	if sse {
		h["cache-control"] = "no-cache"
	}
	return h, nil
}
