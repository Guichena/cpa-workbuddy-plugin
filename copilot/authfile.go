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
	copilotAuthFilePrefix = "copilot-"
	copilotProviderID     = "copilot-provider"
)

var (
	authFilesMu sync.RWMutex
	accountsDir string
)

// StoredAuth 代表落盘的 GitHub Copilot 账号结构体。
//
// GitHubToken 是长期有效的 GitHub OAuth access token（Device Flow 产出，不刷新）；
// CopilotToken 是用 GitHubToken 换取的短期内部令牌，按 ExpiresAt 定时重取。
// 两者必须同时保存：前者是换取后者的唯一凭据，后者是推理请求的实际鉴权值。
type StoredAuth struct {
	Type         string   `json:"type"`
	Provider     string   `json:"provider"`
	AuthID       string   `json:"auth_id"`
	APIKey       string   `json:"api_key"`                 // 有效调用令牌（等同 CopilotToken），供请求头直接复用
	GitHubToken  string   `json:"github_token"`            // GitHub OAuth access token（长期）
	CopilotToken string   `json:"copilot_token,omitempty"` // Copilot 内部令牌（短期）
	ExpiresAt    string   `json:"expires_at,omitempty"`    // CopilotToken 过期时间
	APIEndpoint  string   `json:"api_endpoint,omitempty"`  // 上游返回的权威推理端点（endpoints.api）
	AccountType  string   `json:"account_type,omitempty"`  // individual / business / enterprise
	UserID       string   `json:"user_id,omitempty"`
	Username     string   `json:"username,omitempty"`
	Email        string   `json:"email,omitempty"`
	AvatarURL    string   `json:"avatar_url,omitempty"`
	Label        string   `json:"label"`
	Disabled     bool     `json:"disabled"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	Models       []string `json:"models,omitempty"`
	TestFailed   bool     `json:"test_failed"`
	PhysicalPath string   `json:"-"`
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

// getAccountsDir 获取账号存储目录，默认 ~/.antigravity_cockpit/copilot_accounts/
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
	dir := filepath.Join(home, ".antigravity_cockpit", "copilot_accounts")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// authFileNameFor 根据 GitHub token 生成稳定的文件名（copilot-<hash8>.json）。
func authFileNameFor(githubToken string) string {
	key := strings.TrimSpace(githubToken)
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return copilotAuthFilePrefix + hex.EncodeToString(digest[:8]) + ".json"
}

// authIDFor 根据 GitHub token 生成稳定的账号 AuthID。
func authIDFor(githubToken string) string {
	key := strings.TrimSpace(githubToken)
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return copilotAuthFilePrefix + hex.EncodeToString(digest[:8])
}

// isCopilotAuthFileName 判断文件名是否属于 copilot 凭据命名空间。
func isCopilotAuthFileName(name string) bool {
	base := strings.ToLower(strings.TrimSpace(name))
	if base == "" || !strings.HasSuffix(base, ".json") {
		return false
	}
	return strings.HasPrefix(base, copilotAuthFilePrefix)
}

// isSafeCopilotAuthPath 校验文件名前缀与路径穿越。
func isSafeCopilotAuthPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	slash := filepath.ToSlash(path)
	if strings.Contains(slash, "../") || strings.Contains(slash, "/..") {
		return false
	}
	base := filepath.Base(path)
	if !isCopilotAuthFileName(base) {
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
		auth.Type = copilotProviderID
	}
	if auth.Provider == "" {
		auth.Provider = copilotProviderID
	}

	targetPath := filePath
	if auth.PhysicalPath != "" && isSafeCopilotAuthPath(auth.PhysicalPath) {
		targetPath = auth.PhysicalPath
	}

	if !isSafeCopilotAuthPath(targetPath) {
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

	if !isSafeCopilotAuthPath(filePath) {
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

// listHostAuthFiles 从宿主 host.auth.list 获取所有 copilot 账号。
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
		if !isCopilotAuthFileName(f.Name) {
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
		if err := json.Unmarshal(getResp.JSON, &sa); err == nil && credentialOf(&sa) != "" {
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
		if entry.IsDir() || !isCopilotAuthFileName(entry.Name()) {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		data, errRead := os.ReadFile(fullPath)
		if errRead != nil {
			continue
		}
		var sa StoredAuth
		if err := json.Unmarshal(data, &sa); err == nil && credentialOf(&sa) != "" {
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
	if !strings.HasPrefix(authID, copilotAuthFilePrefix) {
		authID = copilotAuthFilePrefix + authID
	}
	fileName := authID + ".json"
	if !isSafeCopilotAuthPath(fileName) {
		return fmt.Errorf("unsafe authID: %s", authID)
	}

	var removed bool
	for _, acc := range accounts {
		if acc.AuthID == authID && acc.PhysicalPath != "" && isSafeCopilotAuthPath(acc.PhysicalPath) {
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

// maskAPIKey 对令牌进行安全脱敏。
func maskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if len(key) <= 8 {
		return "******"
	}
	return key[:4] + "...." + key[len(key)-4:]
}

// credentialOf 返回账号的有效调用令牌，优先 Copilot 内部令牌，回退 GitHub token。
// [参数] sa: 账号凭据，可为 nil。
// [返回] 有效调用令牌；均缺失时返回空串。
// 最近修改时间：2026-10-15；改动原因：新增，统一账号可用性判定的凭据取值口径。
func credentialOf(sa *StoredAuth) string {
	if sa == nil {
		return ""
	}
	if t := strings.TrimSpace(sa.CopilotToken); t != "" {
		return t
	}
	if t := strings.TrimSpace(sa.APIKey); t != "" {
		return t
	}
	return strings.TrimSpace(sa.GitHubToken)
}

// saveGitHubAccount 保存单条 GitHub Copilot OAuth 授权账号。
// [参数] githubToken: 长期 GitHub access token；copilotToken: 短期 Copilot 内部令牌；
// apiEndpoint: 令牌响应返回的权威推理端点（可为空）；expiresAt: Copilot 令牌过期时间；
// user: GitHub 用户信息；label: 备注名；accountType: 账号类型。
// [返回] 落盘后的账号结构与错误。
// 最近修改时间：2026-10-15；改动原因：新增 apiEndpoint 入参，持久化上游 endpoints.api（关闭 GAP-002）。
func saveGitHubAccount(githubToken, copilotToken, apiEndpoint string, expiresAt time.Time, user *GitHubUserInfo, label, accountType string) (*StoredAuth, error) {
	githubToken = strings.TrimSpace(githubToken)
	if githubToken == "" {
		return nil, fmt.Errorf("empty github token")
	}

	authID := authIDFor(githubToken)
	fileName := authFileNameFor(githubToken)
	fullPath := filepath.Join(getAccountsDir(), fileName)

	if label == "" {
		if user != nil && strings.TrimSpace(user.Login) != "" {
			label = "Copilot " + strings.TrimSpace(user.Login)
		} else {
			label = "Copilot " + maskAPIKey(githubToken)
		}
	}

	now := time.Now().Format(time.RFC3339)
	var expStr string
	if !expiresAt.IsZero() {
		expStr = expiresAt.Format(time.RFC3339)
	}

	var uid, uname, uemail, uavatar string
	if user != nil {
		uid = strings.TrimSpace(user.ID)
		uname = strings.TrimSpace(user.Login)
		uemail = strings.TrimSpace(user.Email)
		uavatar = strings.TrimSpace(user.AvatarURL)
	}

	sa := &StoredAuth{
		Type:         copilotProviderID,
		Provider:     copilotProviderID,
		AuthID:       authID,
		APIKey:       copilotToken,
		GitHubToken:  githubToken,
		CopilotToken: copilotToken,
		ExpiresAt:    expStr,
		APIEndpoint:  strings.TrimRight(strings.TrimSpace(apiEndpoint), "/"),
		AccountType:  normalizeAccountType(accountType),
		UserID:       uid,
		Username:     uname,
		Email:        uemail,
		AvatarURL:    uavatar,
		Label:        label,
		Disabled:     false,
		CreatedAt:    now,
		UpdatedAt:    now,
		Models:       append([]string(nil), defaultModels...),
		TestFailed:   false,
	}

	if err := writeAuthFileDirect(fullPath, sa); err != nil {
		return nil, err
	}
	return sa, nil
}

// saveTokenAccount 保存用户直接导入 GitHub token 的账号（无 Copilot 令牌，由首次使用时换取）。
// [参数] githubToken: 用户提供的 GitHub token；label: 备注名。
// [返回] 落盘后的账号结构与错误。
// 最近修改时间：2026-10-15；改动原因：新增，支持手动导入 GitHub token 的兜底路径。
func saveTokenAccount(githubToken, label string) (*StoredAuth, error) {
	githubToken = strings.TrimSpace(githubToken)
	if githubToken == "" {
		return nil, fmt.Errorf("empty github token")
	}

	authID := authIDFor(githubToken)
	fileName := authFileNameFor(githubToken)
	fullPath := filepath.Join(getAccountsDir(), fileName)

	if label == "" {
		label = "Copilot " + maskAPIKey(githubToken)
	}

	now := time.Now().Format(time.RFC3339)
	sa := &StoredAuth{
		Type:        copilotProviderID,
		Provider:    copilotProviderID,
		AuthID:      authID,
		GitHubToken: githubToken,
		AccountType: defaultAccountType,
		Label:       label,
		Disabled:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
		Models:      append([]string(nil), defaultModels...),
		TestFailed:  false,
	}

	if err := writeAuthFileDirect(fullPath, sa); err != nil {
		return nil, err
	}
	return sa, nil
}
