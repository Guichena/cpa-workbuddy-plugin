package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

var (
	managementBasePathMu sync.RWMutex
	managementBasePath   = "/v0/management"
)

func setManagementBasePath(base string) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return
	}
	managementBasePathMu.Lock()
	defer managementBasePathMu.Unlock()
	managementBasePath = base
}

func getManagementBasePath() string {
	managementBasePathMu.RLock()
	defer managementBasePathMu.RUnlock()
	return managementBasePath
}

type managementRoute struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type resourceRoute struct {
	Path        string `json:"path"`
	Menu        string `json:"menu,omitempty"`
	Description string `json:"description,omitempty"`
}

type managementRegistrationResponse struct {
	Routes    []managementRoute `json:"routes,omitempty"`
	Resources []resourceRoute   `json:"resources,omitempty"`
}

// managementRegistration 声明插件对宿主暴露的管理路由与资源页。
// [参数] 无。
// [返回] 路由与资源注册结构。
func managementRegistration() managementRegistrationResponse {
	base := "/plugins/" + copilotProviderID
	return managementRegistrationResponse{
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: base + "/accounts", Description: "List Copilot accounts."},
			{Method: http.MethodGet, Path: base + "/status", Description: "List Copilot accounts status."},
			{Method: http.MethodPost, Path: base + "/import", Description: "Import GitHub tokens."},
			{Method: http.MethodGet, Path: base + "/export", Description: "Export all Copilot credentials."},
			{Method: http.MethodPost, Path: base + "/export", Description: "Export all Copilot credentials."},
			{Method: http.MethodPost, Path: base + "/delete", Description: "Delete one Copilot account."},
			{Method: http.MethodPost, Path: base + "/enable", Description: "Enable one Copilot account."},
			{Method: http.MethodPost, Path: base + "/disable", Description: "Disable one Copilot account."},
			{Method: http.MethodPost, Path: base + "/enable-all", Description: "Enable all Copilot accounts."},
			{Method: http.MethodPost, Path: base + "/disable-all", Description: "Disable all Copilot accounts."},
			{Method: http.MethodGet, Path: base + "/models", Description: "List models available for one account."},
			{Method: http.MethodPost, Path: base + "/test-active", Description: "Send an active ping chat inference request for one account (body: {auth_index, model})."},
			{Method: http.MethodGet, Path: base + "/credits", Description: "Query Copilot quota for one account or all accounts."},
			{Method: http.MethodPost, Path: base + "/login-start", Description: "Start GitHub Device Flow login from the panel."},
			{Method: http.MethodGet, Path: base + "/login-poll", Description: "Poll GitHub Device Flow login result from the panel."},
		},
		Resources: []resourceRoute{
			{Path: "/panel", Menu: "Copilot", Description: "GitHub Copilot Provider dashboard: accounts, models, quota, import."},
			{Path: "/login-bridge", Description: "Device-code bridge page opened by the host OAuth dialog."},
		},
	}
}

type accountSummary struct {
	AuthID       string                `json:"auth_id"`
	AuthIndex    string                `json:"auth_index"`
	Label        string                `json:"label"`
	APIKeyMasked string                `json:"api_key_masked"`
	UserID       string                `json:"user_id,omitempty"`
	Username     string                `json:"username,omitempty"`
	Email        string                `json:"email,omitempty"`
	AvatarURL    string                `json:"avatar_url,omitempty"`
	AccountType  string                `json:"account_type,omitempty"`
	ExpiresAt    string                `json:"expires_at,omitempty"`
	Disabled     bool                  `json:"disabled"`
	TestFailed   bool                  `json:"test_failed"`
	CoolingDown  bool                  `json:"cooling_down"`
	CreatedAt    string                `json:"created_at"`
	UpdatedAt    string                `json:"updated_at"`
	Models       []string              `json:"models"`
	Quota        *copilotQuotaSnapshot `json:"quota,omitempty"`
}

// normalizeMgmtSubPath 归一化管理子路径，兼容宿主三种前缀形态。
// [参数] rawPath: 宿主传入的原始路径。
// [返回] 归一化子路径与从路径解析出的查询参数。
func normalizeMgmtSubPath(rawPath string) (string, url.Values) {
	u, err := url.Parse(rawPath)
	path := rawPath
	var query url.Values
	if err == nil {
		path = u.Path
		query = u.Query()
	}
	path = strings.TrimRight(path, "/")

	// 兼容 /v0/resource/plugins/copilot-provider/...
	resPrefix := "/v0/resource/plugins/" + copilotProviderID
	if strings.HasPrefix(path, resPrefix) {
		sub := strings.TrimPrefix(path, resPrefix)
		if sub == "" {
			return "/panel", query
		}
		return sub, query
	}

	// 兼容 /v0/management/plugins/copilot-provider/... 与 /plugins/copilot-provider/...
	fullPrefix := getManagementBasePath() + "/plugins/" + copilotProviderID
	shortPrefix := "/plugins/" + copilotProviderID
	switch {
	case strings.HasPrefix(path, fullPrefix):
		path = strings.TrimPrefix(path, fullPrefix)
	case strings.HasPrefix(path, shortPrefix):
		path = strings.TrimPrefix(path, shortPrefix)
	}
	if path == "" {
		path = "/"
	}
	return path, query
}

// handleManagement 分发插件管理路由。
// [参数] raw: 宿主 management.handle 载荷。
// [返回] 管理响应信封与错误。
func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}

	subPath, urlQuery := normalizeMgmtSubPath(req.Path)
	method := strings.ToUpper(req.Method)

	if method == http.MethodGet && (subPath == "/" || subPath == "/panel") {
		return handlePanelPage(), nil
	}

	getQueryParam := func(key string) string {
		if vals := req.Query[key]; len(vals) > 0 && strings.TrimSpace(vals[0]) != "" {
			return strings.TrimSpace(vals[0])
		}
		if urlQuery != nil {
			if v := strings.TrimSpace(urlQuery.Get(key)); v != "" {
				return v
			}
		}
		return ""
	}

	if method == http.MethodGet && subPath == "/login-bridge" {
		return okEnvelope(handleLoginBridge(getQueryParam("state")))
	}

	switch {
	case method == http.MethodGet && (subPath == "/accounts" || subPath == "/status"):
		return handleListAccounts()

	case method == http.MethodPost && subPath == "/import":
		return handleImportAccounts(req.Body)

	case (method == http.MethodPost || method == http.MethodGet) && subPath == "/export":
		return handleExportAccounts()

	case method == http.MethodPost && subPath == "/delete":
		return handleDeleteAccount(req.Body)

	case method == http.MethodPost && subPath == "/enable":
		return handleSetAccountStatus(req.Body, false)

	case method == http.MethodPost && subPath == "/disable":
		return handleSetAccountStatus(req.Body, true)

	case method == http.MethodPost && subPath == "/enable-all":
		return handleSetAllAccountStatus(false)

	case method == http.MethodPost && subPath == "/disable-all":
		return handleSetAllAccountStatus(true)

	case method == http.MethodGet && subPath == "/models":
		authID := getQueryParam("auth_index")
		if authID == "" {
			authID = getQueryParam("auth_id")
		}
		if authID == "" {
			return makeHTTPResponse(http.StatusBadRequest, []byte(`{"error":"auth_index is required"}`))
		}
		models := authModelsForIndex(authID)
		body, _ := json.Marshal(map[string]any{"models": models})
		return makeHTTPResponse(http.StatusOK, body)

	case method == http.MethodPost && subPath == "/test-active":
		return handleTestActive(req.Body)

	case method == http.MethodGet && subPath == "/credits":
		authID := getQueryParam("auth_index")
		if authID == "" {
			authID = getQueryParam("auth_id")
		}
		return handleQuotaQuery(authID)

	case method == http.MethodPost && subPath == "/login-start":
		return handlePanelLoginStart()

	case method == http.MethodGet && subPath == "/login-poll":
		return handlePanelLoginPoll(getQueryParam("state"))

	default:
		return makeHTTPResponse(http.StatusNotFound, []byte(`{"error":"not_found"}`))
	}
}

func makeHTTPResponse(status int, body []byte) ([]byte, error) {
	resp := pluginapi.ManagementResponse{
		StatusCode: status,
		Headers: http.Header{
			"Content-Type": []string{"application/json; charset=utf-8"},
		},
		Body: body,
	}
	return okEnvelope(resp)
}

// handleListAccounts 返回全部账号摘要。
func handleListAccounts() ([]byte, error) {
	accounts, err := listAllAuthFiles()
	if err != nil {
		return makeHTTPResponse(http.StatusInternalServerError, []byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
	}

	list := make([]accountSummary, 0, len(accounts))
	for _, acc := range accounts {
		// 只读缓存：面板刷新会遍历全部账号，逐账号发上游请求会在上游不可达时阻塞加载。
		models := cachedModelsForIndex(acc.AuthID)
		list = append(list, accountSummary{
			AuthID:       acc.AuthID,
			AuthIndex:    acc.AuthID,
			Label:        acc.Label,
			APIKeyMasked: maskAPIKey(credentialOf(acc)),
			UserID:       acc.UserID,
			Username:     acc.Username,
			Email:        acc.Email,
			AvatarURL:    acc.AvatarURL,
			AccountType:  acc.AccountType,
			ExpiresAt:    acc.ExpiresAt,
			Disabled:     acc.Disabled,
			TestFailed:   acc.TestFailed,
			CoolingDown:  isAccountCoolingDown(acc.AuthID),
			CreatedAt:    acc.CreatedAt,
			UpdatedAt:    acc.UpdatedAt,
			Models:       models,
			Quota:        cachedQuota(acc.AuthID),
		})
	}

	body, _ := json.Marshal(map[string]any{
		"accounts": list,
		"count":    len(list),
	})
	return makeHTTPResponse(http.StatusOK, body)
}

// handleImportAccounts 解析并导入用户粘贴的 GitHub token 列表。
//
// 支持两种输入：JSON 数组（[{token,label},...]）与纯文本逐行（每行一个 token，
// 可用 `token----label` 指定备注）。重复 token 按 AuthID 去重覆盖。
// [参数] body: 请求体。
// [返回] 管理响应。
func handleImportAccounts(body []byte) ([]byte, error) {
	content := gjson.GetBytes(body, "content").String()
	if content == "" {
		content = string(body)
	}

	imported := 0
	skipped := 0
	var errs []string

	type tokenEntry struct {
		Token string
		Label string
	}
	var entries []tokenEntry

	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "[") {
		var list []struct {
			Token string `json:"token"`
			Label string `json:"label"`
		}
		if err := json.Unmarshal([]byte(trimmed), &list); err != nil {
			return makeHTTPResponse(http.StatusBadRequest, []byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
		}
		for _, e := range list {
			entries = append(entries, tokenEntry{Token: e.Token, Label: e.Label})
		}
	} else {
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			token, label := line, ""
			if parts := strings.SplitN(line, "----", 2); len(parts) == 2 {
				token = strings.TrimSpace(parts[0])
				label = strings.TrimSpace(parts[1])
			}
			entries = append(entries, tokenEntry{Token: token, Label: label})
		}
	}

	for _, e := range entries {
		token := strings.TrimSpace(e.Token)
		if token == "" {
			skipped++
			continue
		}
		if _, err := saveTokenAccount(token, strings.TrimSpace(e.Label)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", maskAPIKey(token), err))
			skipped++
			continue
		}
		imported++
	}

	respBody, _ := json.Marshal(map[string]any{
		"imported": imported,
		"skipped":  skipped,
		"errors":   errs,
	})
	return makeHTTPResponse(http.StatusOK, respBody)
}

// handleExportAccounts 导出全部账号凭据。
func handleExportAccounts() ([]byte, error) {
	accounts, err := listAllAuthFiles()
	if err != nil {
		return makeHTTPResponse(http.StatusInternalServerError, []byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
	}
	body, _ := json.Marshal(map[string]any{
		"accounts": accounts,
		"count":    len(accounts),
		"exported": time.Now().Format(time.RFC3339),
	})
	return makeHTTPResponse(http.StatusOK, body)
}

// handleDeleteAccount 删除指定账号。
// [参数] body: 含 auth_id 的请求体。
// [返回] 管理响应。
// 最近修改时间：2026-10-15；改动原因：删除后清理额度缓存，避免面板残留已删账号的陈旧额度。
func handleDeleteAccount(body []byte) ([]byte, error) {
	authID := extractAuthID(body)
	if authID == "" {
		return makeHTTPResponse(http.StatusBadRequest, []byte(`{"error":"auth_id is required"}`))
	}
	if err := deleteAuthFileDirect(authID); err != nil {
		return makeHTTPResponse(http.StatusNotFound, []byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
	}
	invalidateQuota(authID)
	return makeHTTPResponse(http.StatusOK, []byte(`{"ok":true}`))
}

// handleSetAccountStatus 启用或禁用单个账号。
func handleSetAccountStatus(body []byte, disabled bool) ([]byte, error) {
	authID := extractAuthID(body)
	if authID == "" {
		return makeHTTPResponse(http.StatusBadRequest, []byte(`{"error":"auth_id is required"}`))
	}
	if err := setAccountDisabled(authID, disabled); err != nil {
		return makeHTTPResponse(http.StatusNotFound, []byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
	}
	return makeHTTPResponse(http.StatusOK, []byte(`{"ok":true}`))
}

// handleSetAllAccountStatus 批量启用或禁用全部账号。
func handleSetAllAccountStatus(disabled bool) ([]byte, error) {
	accounts, err := listAllAuthFiles()
	if err != nil {
		return makeHTTPResponse(http.StatusInternalServerError, []byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
	}
	updated := 0
	for _, acc := range accounts {
		if err := setAccountDisabled(acc.AuthID, disabled); err == nil {
			updated++
		}
	}
	body, _ := json.Marshal(map[string]any{"ok": true, "updated": updated})
	return makeHTTPResponse(http.StatusOK, body)
}

// handleQuotaQuery 查询单账号或全账号的 Copilot 配额。
// [参数] authID: 账号标识；为空表示查询全部。
// [返回] 管理响应。
func handleQuotaQuery(authID string) ([]byte, error) {
	authID = strings.TrimSpace(authID)

	if authID == "" {
		accounts, err := listAllAuthFiles()
		if err != nil {
			return makeHTTPResponse(http.StatusInternalServerError, []byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
		}
		out := make(map[string]*copilotQuotaSnapshot, len(accounts))
		for _, acc := range accounts {
			snapshot, errQuota := fetchAccountQuota(acc)
			if errQuota != nil && snapshot == nil {
				snapshot = &copilotQuotaSnapshot{Error: errQuota.Error()}
			}
			out[acc.AuthID] = snapshot
		}
		body, _ := json.Marshal(map[string]any{"quotas": out})
		return makeHTTPResponse(http.StatusOK, body)
	}

	accounts, _ := listAllAuthFiles()
	for _, acc := range accounts {
		if acc.AuthID == authID || acc.AuthID == strings.TrimSuffix(authID, ".json") {
			snapshot, errQuota := fetchAccountQuota(acc)
			if errQuota != nil && snapshot == nil {
				return makeHTTPResponse(http.StatusBadGateway, []byte(fmt.Sprintf(`{"error":%q}`, errQuota.Error())))
			}
			body, _ := json.Marshal(snapshot)
			return makeHTTPResponse(http.StatusOK, body)
		}
	}
	return makeHTTPResponse(http.StatusNotFound, []byte(`{"error":"account not found"}`))
}

// extractAuthID 从请求体中提取 auth_id / auth_index / authID。
func extractAuthID(body []byte) string {
	for _, key := range []string{"auth_id", "auth_index", "authID", "id"} {
		if v := strings.TrimSpace(gjson.GetBytes(body, key).String()); v != "" {
			return v
		}
	}
	return ""
}

// setAccountDisabled 修改指定账号的禁用状态并落盘。
// [参数] authID: 账号标识；disabled: 目标状态。
// [返回] 错误。
// 最近修改时间：2026-10-15；改动原因：新增，账号启停状态直写物理文件。
func setAccountDisabled(authID string, disabled bool) error {
	authID = strings.TrimSpace(authID)
	accounts, err := listAllAuthFiles()
	if err != nil {
		return err
	}
	for _, acc := range accounts {
		if acc.AuthID != authID && acc.AuthID != strings.TrimSuffix(authID, ".json") {
			continue
		}
		acc.Disabled = disabled
		acc.UpdatedAt = time.Now().Format(time.RFC3339)
		path := acc.PhysicalPath
		if path == "" {
			path = getAccountsDir() + "/" + authID + ".json"
		}
		return writeAuthFileDirect(path, acc)
	}
	return fmt.Errorf("account not found: %s", authID)
}
