package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	zcodeAuthFilePrefix = "zcode-"
	zcodeProviderID     = "zcode-provider"
)

// ZCode OAuth 站点归属（provider family）。
//
// 背景（2026-10-14 线上实测）：ZCode 服务端 `cli/init` 的 `provider` 参数只有两个有效值，
// 决定签发哪个站点的授权地址：`zai` 走国际站 `chat.z.ai`，`bigmodel` 走中国站
// `bigmodel.cn/login`。账号落盘时记录归属，供上游端点按 family 分支解析使用。
const (
	providerFamilyZai      = "zai"
	providerFamilyBigmodel = "bigmodel"
)

// normalizeProviderFamily 归一化 provider 取值，空值与非法值均回退默认 `zai`。
//
// 兼容既有账号：历史凭据无该字段，读取时按 `zai`（国际站）处理，行为与改造前一致。
// [参数] raw: 原始 provider 取值。
// [返回] 归一化后的 family 标识。
// 最近修改时间：2026-10-14；改动原因：新增，支持 zai / bigmodel 双站归属归一化。
func normalizeProviderFamily(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), providerFamilyBigmodel) {
		return providerFamilyBigmodel
	}
	return providerFamilyZai
}

// providerFamilyOf 返回账号的归一化站点归属，nil 账号按默认 `zai` 处理。
// [参数] sa: 账号凭据，可为 nil。
// [返回] 归一化后的 family 标识。
// 最近修改时间：2026-10-14；改动原因：新增，供端点分支与凭据解析统一读取归属。
func providerFamilyOf(sa *StoredAuth) string {
	if sa == nil {
		return providerFamilyZai
	}
	return normalizeProviderFamily(sa.ProviderFamily)
}

var (
	authFilesMu sync.RWMutex
	accountsDir string
)

// StoredAuth 代表落盘的账号结构体。
type StoredAuth struct {
	Type          string   `json:"type"`
	Provider      string   `json:"provider"`
	AuthID        string   `json:"auth_id"`
	APIKey        string   `json:"api_key"`                  // 有效调用令牌 (zcodeJwtToken 或 APIKey)
	Token         string   `json:"token,omitempty"`          // ZCode JWT Token (zcodejwttoken)
	AccessToken   string   `json:"access_token,omitempty"`   // Z.ai OAuth Access Token
	BusinessToken string   `json:"business_token,omitempty"` // Z.ai 业务 Access Token
	ExpiresAt     string   `json:"expires_at,omitempty"`     // 令牌过期时间
	UserID        string   `json:"user_id,omitempty"`        // 用户 ID
	Username      string   `json:"username,omitempty"`       // 用户名
	Email         string   `json:"email,omitempty"`          // 邮箱
	AvatarURL     string   `json:"avatar_url,omitempty"`     // 头像链接
	Label         string   `json:"label"`
	Disabled      bool     `json:"disabled"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	Models        []string `json:"models,omitempty"`
	TestFailed    bool     `json:"test_failed"`
	// ProviderFamily 记录 OAuth 授权站点归属（`zai` 国际站 / `bigmodel` 中国站）。
	// 空值视为 `zai`，保证历史凭据向后兼容。
	ProviderFamily string `json:"provider_family,omitempty"`
	PhysicalPath   string `json:"-"`
}

// ZaiUserInfo 代表 Z.ai 用户信息。
type ZaiUserInfo struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Avatar string `json:"avatar"`
}

type rpcHostAuthListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

type rpcHostAuthGetResponse struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name"`
	Path      string          `json:"path"`
	JSON      json.RawMessage `json:"json"`
}

// setAccountsDir 设置账号存储目录（方便测试与动态配置覆盖）。
func setAccountsDir(dir string) {
	authFilesMu.Lock()
	defer authFilesMu.Unlock()
	accountsDir = dir
}

// getAccountsDir 获取账号存储目录，默认 ~/.antigravity_cockpit/zcode_accounts/
func getAccountsDir() string {
	authFilesMu.RLock()
	if accountsDir != "" {
		defer authFilesMu.RUnlock()
		return accountsDir
	}
	authFilesMu.RUnlock()

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	dir := filepath.Join(home, ".antigravity_cockpit", "zcode_accounts")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// authFileNameFor 根据 API Key 生成稳定的文件名（zcode-<hash8>.json）。
func authFileNameFor(apiKey string) string {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return zcodeAuthFilePrefix + hex.EncodeToString(digest[:8]) + ".json"
}

// authIDFor 根据 API Key 生成稳定的账号 AuthID。
func authIDFor(apiKey string) string {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return zcodeAuthFilePrefix + hex.EncodeToString(digest[:8])
}

// isZCodeAuthFileName 判断文件名是否属于 zcode 凭据命名空间。
func isZCodeAuthFileName(name string) bool {
	base := strings.ToLower(strings.TrimSpace(name))
	if base == "" || !strings.HasSuffix(base, ".json") {
		return false
	}
	return strings.HasPrefix(base, zcodeAuthFilePrefix)
}

// isSafeZCodeAuthPath 校验文件名前缀与路径穿越。
func isSafeZCodeAuthPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	slash := filepath.ToSlash(path)
	if strings.Contains(slash, "../") || strings.Contains(slash, "/..") {
		return false
	}
	base := filepath.Base(path)
	if !isZCodeAuthFileName(base) {
		return false
	}
	return base == filepath.Base(filepath.Clean(path))
}

// hostAuthSaveJSON 通过宿主 RPC 保存认证文件。
func hostAuthSaveJSON(name string, raw []byte) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty auth file name")
	}
	saveReq := pluginapi.HostAuthSaveRequest{
		Name: name,
		JSON: raw,
	}
	saveBody, _ := json.Marshal(saveReq)
	rawResp, err := hostCall(pluginabi.MethodHostAuthSave, saveBody)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(rawResp, &env); err != nil || !env.OK {
		return fmt.Errorf("host.auth.save failed")
	}
	return nil
}

// writeAuthFileDirect 原子落盘写入账号凭据 JSON 文件，并在宿主在线时同步。
func writeAuthFileDirect(filePath string, auth *StoredAuth) error {
	authFilesMu.Lock()
	defer authFilesMu.Unlock()

	if auth.Type == "" {
		auth.Type = zcodeProviderID
	}
	if auth.Provider == "" {
		auth.Provider = zcodeProviderID
	}

	targetPath := filePath
	if auth.PhysicalPath != "" && isSafeZCodeAuthPath(auth.PhysicalPath) {
		targetPath = auth.PhysicalPath
	}

	if !isSafeZCodeAuthPath(targetPath) {
		return fmt.Errorf("unsafe auth file path: %s", targetPath)
	}

	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth failed: %w", err)
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir failed: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", targetPath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("write tmp file failed: %w", err)
	}

	if err := os.Rename(tmpFile, targetPath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("rename tmp file failed: %w", err)
	}

	// 若尚未关联宿主物理路径且宿主可用，通过 host.auth.save 同步注册
	if auth.PhysicalPath == "" && !auth.Disabled {
		_ = hostAuthSaveJSON(filepath.Base(targetPath), data)
	}

	return nil
}

// readAuthFile 读取单个账号凭据文件。
func readAuthFile(filePath string) (*StoredAuth, error) {
	authFilesMu.RLock()
	defer authFilesMu.RUnlock()

	if !isSafeZCodeAuthPath(filePath) {
		return nil, fmt.Errorf("unsafe auth file path: %s", filePath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var auth StoredAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, err
	}
	auth.PhysicalPath = filePath
	return &auth, nil
}

// listHostAuthFiles 从宿主 host.auth.list 获取所有 zcode 账号。
func listHostAuthFiles() []*StoredAuth {
	raw, err := hostCall(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return nil
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
		return nil
	}
	var resp rpcHostAuthListResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		return nil
	}

	var out []*StoredAuth
	for _, f := range resp.Files {
		if !isZCodeAuthFileName(f.Name) {
			continue
		}
		getBody, _ := json.Marshal(map[string]string{"auth_index": f.AuthIndex})
		getRaw, errGet := hostCall(pluginabi.MethodHostAuthGet, getBody)
		if errGet != nil {
			continue
		}
		var getEnv envelope
		if err := json.Unmarshal(getRaw, &getEnv); err != nil || !getEnv.OK {
			continue
		}
		var getResp rpcHostAuthGetResponse
		if err := json.Unmarshal(getEnv.Result, &getResp); err != nil {
			continue
		}
		var sa StoredAuth
		if err := json.Unmarshal(getResp.JSON, &sa); err == nil && (sa.APIKey != "" || sa.Token != "") {
			if sa.APIKey == "" {
				sa.APIKey = sa.Token
			}
			if sa.AuthID == "" {
				sa.AuthID = strings.TrimSuffix(f.Name, ".json")
			}
			sa.Disabled = sa.Disabled || f.Disabled
			sa.PhysicalPath = getResp.Path
			out = append(out, &sa)
		}
	}
	return out
}

// listAllAuthFiles 列出所有有效账号（合并宿主与本地目录）。
func listAllAuthFiles() ([]*StoredAuth, error) {
	seen := make(map[string]*StoredAuth)
	var list []*StoredAuth

	// 1. 尝试从宿主读取
	for _, sa := range listHostAuthFiles() {
		seen[sa.AuthID] = sa
		list = append(list, sa)
	}

	// 2. 读取本地目录
	dir := getAccountsDir()
	authFilesMu.RLock()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		authFilesMu.RUnlock()
		return list, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !isZCodeAuthFileName(entry.Name()) {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		data, errRead := os.ReadFile(fullPath)
		if errRead != nil {
			continue
		}
		var sa StoredAuth
		if err := json.Unmarshal(data, &sa); err == nil && (sa.APIKey != "" || sa.Token != "") {
			if sa.APIKey == "" {
				sa.APIKey = sa.Token
			}
			if sa.AuthID == "" {
				sa.AuthID = strings.TrimSuffix(entry.Name(), ".json")
			}
			if _, exists := seen[sa.AuthID]; !exists {
				sa.PhysicalPath = fullPath
				seen[sa.AuthID] = &sa
				list = append(list, &sa)
			}
		}
	}
	authFilesMu.RUnlock()
	return list, nil
}

// deleteAuthFileDirect 删除指定账号文件（同时清理宿主物理路径与本地目录）。
func deleteAuthFileDirect(authID string) error {
	accounts, _ := listAllAuthFiles()
	dir := getAccountsDir()

	authFilesMu.Lock()
	defer authFilesMu.Unlock()

	authID = strings.TrimSpace(authID)
	if !strings.HasPrefix(authID, zcodeAuthFilePrefix) {
		authID = zcodeAuthFilePrefix + authID
	}
	fileName := authID + ".json"
	if !isSafeZCodeAuthPath(fileName) {
		return fmt.Errorf("unsafe authID: %s", authID)
	}

	var removed bool
	for _, acc := range accounts {
		if acc.AuthID == authID && acc.PhysicalPath != "" && isSafeZCodeAuthPath(acc.PhysicalPath) {
			if err := os.Remove(acc.PhysicalPath); err == nil {
				removed = true
			}
		}
	}

	localPath := filepath.Join(dir, fileName)
	if err := os.Remove(localPath); err == nil {
		removed = true
	}

	if !removed {
		return os.ErrNotExist
	}
	return nil
}

// maskAPIKey 对 API Key 进行安全脱敏。
func maskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "******"
	}
	return key[:4] + "...." + key[len(key)-4:]
}

// saveAPIKeyAccount 保存单条 API Key 账号凭据文件并返回落盘结构。
func saveAPIKeyAccount(apiKey, label string) (*StoredAuth, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("empty api_key")
	}
	authID := authIDFor(apiKey)
	fileName := authFileNameFor(apiKey)
	fullPath := filepath.Join(getAccountsDir(), fileName)

	if label == "" {
		label = "ZCode " + maskAPIKey(apiKey)
	}

	now := time.Now().Format(time.RFC3339)
	sa := &StoredAuth{
		Type:       zcodeProviderID,
		Provider:   zcodeProviderID,
		AuthID:     authID,
		APIKey:     apiKey,
		Label:      label,
		Disabled:   false,
		CreatedAt:  now,
		UpdatedAt:  now,
		Models:     defaultModels,
		TestFailed: false,
	}

	if err := writeAuthFileDirect(fullPath, sa); err != nil {
		return nil, err
	}
	return sa, nil
}

// saveZaiAccount 保存单条 Z.ai OAuth 授权账号。
// [参数] token: 有效调用令牌；accessToken: OAuth access token；businessToken: 业务令牌；
// user: 用户信息；expiresAt: 过期时间；label: 备注名；provider: 站点归属（`zai` / `bigmodel`）。
// [返回] 落盘后的账号结构与错误。
// 最近修改时间：2026-10-14；改动原因：新增 provider 归属参数，支持中国站 bigmodel 账号。
func saveZaiAccount(token, accessToken, businessToken string, user *ZaiUserInfo, expiresAt time.Time, label string, provider string) (*StoredAuth, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		token = strings.TrimSpace(accessToken)
	}
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}

	authID := authIDFor(token)
	fileName := authFileNameFor(token)
	fullPath := filepath.Join(getAccountsDir(), fileName)

	if label == "" {
		if user != nil && strings.TrimSpace(user.Name) != "" {
			label = "ZCode " + strings.TrimSpace(user.Name)
		} else if user != nil && strings.TrimSpace(user.Email) != "" {
			label = "ZCode " + strings.TrimSpace(user.Email)
		} else {
			label = "ZCode " + maskAPIKey(token)
		}
	}

	now := time.Now().Format(time.RFC3339)
	var expStr string
	if !expiresAt.IsZero() {
		expStr = expiresAt.Format(time.RFC3339)
	}

	var uid, uname, uemail, uavatar string
	if user != nil {
		uid = strings.TrimSpace(user.UserID)
		uname = strings.TrimSpace(user.Name)
		uemail = strings.TrimSpace(user.Email)
		uavatar = strings.TrimSpace(user.Avatar)
	}

	sa := &StoredAuth{
		Type:           zcodeProviderID,
		Provider:       zcodeProviderID,
		AuthID:         authID,
		APIKey:         token, // 保存有效 token，供请求头和探活直接复用
		Token:          token,
		AccessToken:    accessToken,
		BusinessToken:  businessToken,
		ExpiresAt:      expStr,
		UserID:         uid,
		Username:       uname,
		Email:          uemail,
		AvatarURL:      uavatar,
		Label:          label,
		Disabled:       false,
		CreatedAt:      now,
		UpdatedAt:      now,
		Models:         defaultModels,
		TestFailed:     false,
		ProviderFamily: normalizeProviderFamily(provider),
	}

	if err := writeAuthFileDirect(fullPath, sa); err != nil {
		return nil, err
	}
	return sa, nil
}
