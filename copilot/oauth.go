package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// GitHub OAuth Device Flow 配置常量。
//
// 关键事实（来源：ericc-ch/copilot-api master 分支 src/lib/api-config.ts，
// 2026-10-15 调研）：该 client_id 是 GitHub Copilot 官方 VS Code 扩展使用的
// 公开客户端标识，scope 仅需 read:user。这两个值是逆向所得的公开常量，
// 非本项目私有凭据；若上游变更，改此处即可，无需改动其它文件。
//
// 流程分两步：
//
//  1. POST https://github.com/login/device/code（client_id + scope）
//     → 返回 device_code / user_code / verification_uri / interval；
//  2. 用户在 verification_uri 输入 user_code 完成授权；
//  3. POST https://github.com/login/oauth/access_token（grant_type=device_code）
//     → 返回 access_token；轮询期间上游返回 authorization_pending / slow_down。
const (
	githubClientID        = "Iv1.b507a08c87ecfe98"
	githubDeviceScope     = "read:user"
	githubDeviceCodeURL   = "https://github.com/login/device/code"
	githubAccessTokenURL  = "https://github.com/login/oauth/access_token"
	githubDeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
	githubDeviceCodeTTL   = 15 * time.Minute
	githubDefaultPollSec  = 5
	githubSlowDownStepSec = 5
)

var (
	oauthHTTPClient = &http.Client{Timeout: 30 * time.Second}

	// 测试注入点：非空时覆盖 device code / access token 端点。
	mockGitHubDeviceCodeURL  string
	mockGitHubAccessTokenURL string
)

// githubDeviceCodeResponse 是设备码申请响应。
type githubDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
}

// githubAccessTokenResponse 是访问令牌轮询响应。
//
// 轮询期间上游以 HTTP 200 + error 字段表达「尚未授权」，不是传输失败：
// authorization_pending 表示继续等待，slow_down 表示需拉长间隔，
// expired_token / access_denied 表示流程终止。
type githubAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	Description string `json:"error_description"`
	Interval    int64  `json:"interval"`
}

// oauthLoginSession 承载一次 GitHub Device Flow 的轮询状态。
type oauthLoginSession struct {
	CreatedAt       time.Time
	DeviceCode      string
	UserCode        string
	VerificationURI string
	Interval        time.Duration
	Auth            *pluginapi.AuthData
	Error           string
	Completed       bool

	// mu 串行化同一会话的并发轮询，避免宿主与面板同时访问令牌端点。
	mu sync.Mutex
	// nextPollAt 是下一次允许访问 GitHub 令牌端点的时间。
	nextPollAt time.Time
}

// applySlowDown 采纳 GitHub 在 slow_down 中给出的新轮询间隔。
// [参数] upstreamInterval: 上游返回的间隔秒数，0 表示未给出。
// [返回] 无。
// 最近修改时间：2026-10-17 15:00:00；改动原因：新增，slow_down 时按上游间隔拉长轮询节奏。
func (s *oauthLoginSession) applySlowDown(upstreamInterval int64) {
	if upstreamInterval > 0 {
		s.Interval = time.Duration(upstreamInterval) * time.Second
		return
	}
	s.Interval += githubSlowDownStepSec * time.Second
}

var oauthSessions sync.Map

// requestDeviceCode 向 GitHub 申请设备码与用户码。
// [参数] 无。
// [返回] 设备码响应与错误。
// 最近修改时间：2026-10-15；改动原因：新增，GitHub Device Flow 第一步。
func requestDeviceCode() (*githubDeviceCodeResponse, error) {
	endpoint := githubDeviceCodeURL
	if mockGitHubDeviceCodeURL != "" {
		endpoint = mockGitHubDeviceCodeURL
	}

	form := url.Values{}
	form.Set("client_id", githubClientID)
	form.Set("scope", githubDeviceScope)

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request device code: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read device code response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("device code upstream error %d: %s", resp.StatusCode, string(raw))
	}

	var out githubDeviceCodeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse device code response: %w", err)
	}
	if strings.TrimSpace(out.DeviceCode) == "" || strings.TrimSpace(out.UserCode) == "" {
		return nil, fmt.Errorf("设备码响应无效：缺少 device_code 或 user_code")
	}
	return &out, nil
}

// pollAccessToken 查询一次设备授权结果。
// [参数] deviceCode: 设备码。
// [返回] 令牌响应与错误；传输失败返回 error，业务态由响应 Error 字段表达。
// 最近修改时间：2026-10-15；改动原因：新增，GitHub Device Flow 第三步轮询。
func pollAccessToken(deviceCode string) (*githubAccessTokenResponse, error) {
	endpoint := githubAccessTokenURL
	if mockGitHubAccessTokenURL != "" {
		endpoint = mockGitHubAccessTokenURL
	}

	form := url.Values{}
	form.Set("client_id", githubClientID)
	form.Set("device_code", strings.TrimSpace(deviceCode))
	form.Set("grant_type", githubDeviceGrantType)

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create access token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request access token: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read access token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("access token upstream error %d: %s", resp.StatusCode, string(raw))
	}

	var out githubAccessTokenResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse access token response: %w", err)
	}
	return &out, nil
}

// fetchGitHubUser 用 GitHub token 拉取用户信息，用于账号展示名与归属。
// [参数] githubToken: GitHub access token。
// [返回] 用户信息与错误；失败返回 nil 用户（不阻断落盘）。
// 最近修改时间：2026-10-15；改动原因：新增，登录成功后补充账号展示信息。
func fetchGitHubUser(githubToken string) *GitHubUserInfo {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return nil
	}
	applyGitHubHeaders(req.Header, githubToken, currentConfig())

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}
	var user GitHubUserInfo
	if err := json.Unmarshal(raw, &user); err != nil {
		return nil
	}
	return &user
}

// settleDeviceLogin 将已授权的设备流程落盘为账号，并构造宿主凭据记录。
// [参数] githubToken: 已获取的 GitHub access token。
// [返回] 宿主凭据记录与错误。
// 最近修改时间：2026-10-15；改动原因：落盘时同步持久化 endpoints.api（关闭 GAP-002）。
func settleDeviceLogin(githubToken string) (*pluginapi.AuthData, error) {
	githubToken = strings.TrimSpace(githubToken)
	if githubToken == "" {
		return nil, fmt.Errorf("授权成功但未取得 access_token")
	}

	// 换取 Copilot 内部令牌；失败不阻断落盘，账号保留 GitHub token 供后续重试。
	copilotToken := ""
	apiEndpoint := ""
	expiresAt := time.Time{}
	if result, err := exchangeCopilotToken(githubToken); err == nil {
		copilotToken = result.Token
		apiEndpoint = result.AccountAPI
		expiresAt = result.ExpiresAt
	}

	user := fetchGitHubUser(githubToken)
	stored, errSave := saveGitHubAccount(githubToken, copilotToken, apiEndpoint, expiresAt, user, "", defaultAccountType)
	if errSave != nil {
		return nil, fmt.Errorf("save account: %w", errSave)
	}
	invalidateQuota(stored.AuthID)

	rawStorage, _ := json.Marshal(stored)
	authData := pluginapi.AuthData{
		Provider:    providerName,
		ID:          stored.AuthID,
		FileName:    stored.AuthID + ".json",
		Label:       stored.Label,
		Disabled:    stored.Disabled,
		StorageJSON: rawStorage,
		Metadata: map[string]any{
			"type":     providerName,
			"provider": providerName,
			"logo":     pluginLogoURL,
			"disabled": stored.Disabled,
		},
	}
	return &authData, nil
}

// startDeviceLogin 发起一次 GitHub Device Flow 并登记轮询会话。
// [参数] 无。
// [返回] 会话 state、设备码响应与错误。
// 最近修改时间：2026-10-15；改动原因：抽出纯内核，供宿主登录入口与面板路由共用。
func startDeviceLogin() (string, *githubDeviceCodeResponse, error) {
	deviceResp, err := requestDeviceCode()
	if err != nil {
		return "", nil, err
	}

	intervalSec := deviceResp.Interval
	if intervalSec <= 0 {
		intervalSec = githubDefaultPollSec
	}

	state := randomHex(16)
	oauthSessions.Store(state, &oauthLoginSession{
		CreatedAt:       time.Now(),
		DeviceCode:      deviceResp.DeviceCode,
		UserCode:        deviceResp.UserCode,
		VerificationURI: deviceResp.VerificationURI,
		Interval:        time.Duration(intervalSec) * time.Second,
	})
	return state, deviceResp, nil
}

// pollDeviceLogin 查询一次设备授权结果，授权成功后落盘账号。
//
// 返回值语义与宿主 AuthLoginStatus 一致：pending 表示继续等待，success 表示
// 已完成并返回凭据，error 表示流程终止。会话仅在 success / error 时销毁。
// 会话内按 GitHub 设备流的 interval 节流，未到时间直接返回 pending。
// [参数] state: 会话标识。
// [返回] 状态、提示文案、凭据记录与错误。
// 最近修改时间：2026-10-17 15:00:00；改动原因：按 interval 节流并采纳 slow_down 间隔。
func pollDeviceLogin(state string) (pluginapi.AuthLoginStatus, string, *pluginapi.AuthData, error) {
	state = strings.TrimSpace(state)
	if state == "" {
		return pluginapi.AuthLoginStatusError, "缺少授权会话标识", nil, nil
	}

	rawSession, ok := oauthSessions.Load(state)
	if !ok {
		return pluginapi.AuthLoginStatusError, "授权会话不存在或已过期", nil, nil
	}
	session, ok := rawSession.(*oauthLoginSession)
	if !ok || session == nil {
		return pluginapi.AuthLoginStatusError, "授权会话数据异常", nil, nil
	}

	// 1. 同一会话的并发轮询串行执行，避免重复消费设备码。
	session.mu.Lock()
	defer session.mu.Unlock()

	if time.Since(session.CreatedAt) > githubDeviceCodeTTL {
		oauthSessions.Delete(state)
		return pluginapi.AuthLoginStatusError, "授权会话已超时（15分钟），请重新发起", nil, nil
	}

	// 2. 已完成的会话直接返回结果（宿主可能重复轮询）。
	if session.Completed {
		oauthSessions.Delete(state)
		if session.Error != "" {
			return pluginapi.AuthLoginStatusError, session.Error, nil, nil
		}
		if session.Auth == nil {
			return pluginapi.AuthLoginStatusError, "缺少凭据信息", nil, nil
		}
		return pluginapi.AuthLoginStatusSuccess, "GitHub Copilot 账号授权成功", session.Auth, nil
	}

	// 3. 未到下次允许轮询时间时直接返回 pending，避免触发上游 slow_down。
	if time.Now().Before(session.nextPollAt) {
		return pluginapi.AuthLoginStatusPending, "等待在 GitHub 页面输入设备码完成授权...", nil, nil
	}
	session.nextPollAt = time.Now().Add(session.Interval)

	// 4. 查询 GitHub 设备授权端点。
	tokenResp, errPoll := pollAccessToken(session.DeviceCode)
	if errPoll != nil {
		// 传输类失败保留会话，等待宿主下一轮轮询重试。
		return pluginapi.AuthLoginStatusPending, "等待授权结果（上游暂时不可达，稍后自动重试）...", nil, nil
	}

	switch strings.TrimSpace(tokenResp.Error) {
	case "":
		// 无错误且带 token 视为授权成功。
	case "authorization_pending":
		return pluginapi.AuthLoginStatusPending, "等待在 GitHub 页面输入设备码完成授权...", nil, nil
	case "slow_down":
		session.applySlowDown(tokenResp.Interval)
		session.nextPollAt = time.Now().Add(session.Interval)
		return pluginapi.AuthLoginStatusPending, "GitHub 要求降低轮询频率，继续等待授权...", nil, nil
	default:
		session.Completed = true
		msg := strings.TrimSpace(tokenResp.Description)
		if msg == "" {
			msg = tokenResp.Error
		}
		session.Error = "GitHub 授权失败：" + msg
		oauthSessions.Delete(state)
		return pluginapi.AuthLoginStatusError, session.Error, nil, nil
	}

	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return pluginapi.AuthLoginStatusPending, "等待在 GitHub 页面输入设备码完成授权...", nil, nil
	}

	authData, errSettle := settleDeviceLogin(tokenResp.AccessToken)
	if errSettle != nil {
		session.Completed = true
		session.Error = errSettle.Error()
		oauthSessions.Delete(state)
		return pluginapi.AuthLoginStatusError, errSettle.Error(), nil, nil
	}

	session.Auth = authData
	session.Completed = true
	oauthSessions.Delete(state)
	return pluginapi.AuthLoginStatusSuccess, "GitHub Copilot 账号授权成功", authData, nil
}

// handleStartLogin 处理宿主发起 OAuth 登录请求：申请设备码并返回桥接页地址。
//
// URL 返回插件自有 resource 前缀的桥接页相对路径而非 GitHub 授权地址：宿主
// ServePluginAuthURL 只回传 {status, url, state}，本响应 Metadata 中的 user_code
// 会被写进宿主 session store 而不回传前端，故宿主通用弹窗打不开设备码展示；
// 让宿主弹窗打开本插件的桥接页，即可在页面内看到设备码并完成授权。
// Metadata 仍保留 user_code/verification_uri/interval，便于宿主未来修复后直接消费。
// [参数] raw: 宿主登录启动请求载荷。
// [返回] 授权启动响应与错误。
// 最近修改时间：2026-10-16；改动原因：URL 改为桥接页相对路径，绕过宿主弹窗不显示设备码。
func handleStartLogin(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginStartRequest
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &req)
	}

	state, deviceResp, err := startDeviceLogin()
	if err != nil {
		return nil, err
	}

	intervalSec := deviceResp.Interval
	if intervalSec <= 0 {
		intervalSec = githubDefaultPollSec
	}
	expiresAt := time.Now().Add(githubDeviceCodeTTL)
	if deviceResp.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second)
	}

	bridgeURL := loginBridgeURL(state)
	return okEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  providerName,
		URL:       bridgeURL,
		State:     state,
		ExpiresAt: expiresAt.UTC(),
		Metadata: map[string]any{
			"logo":             pluginLogoURL,
			"auth_url":         deviceResp.VerificationURI,
			"bridge_url":       bridgeURL,
			"user_code":        deviceResp.UserCode,
			"verification_uri": deviceResp.VerificationURI,
			"interval":         intervalSec,
			"prompt": "请在打开的授权页中输入设备码 " + deviceResp.UserCode +
				" 完成 GitHub 授权；授权后本插件会自动轮询绑定账号。",
		},
	})
}

// handlePollLogin 轮询 GitHub 设备授权结果，授权成功后落盘账号。
// [参数] raw: 宿主登录轮询请求载荷。
// [返回] 轮询结果信封与错误。
// 最近修改时间：2026-10-15；改动原因：改为复用 pollDeviceLogin 纯内核。
func handlePollLogin(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("poll: %w", err)
	}

	status, message, authData, err := pollDeviceLogin(req.State)
	if err != nil {
		return nil, err
	}

	resp := pluginapi.AuthLoginPollResponse{Status: status, Message: message}
	if authData != nil {
		resp.Auth = *authData
	}
	return okEnvelope(resp)
}

// handlePanelLoginStart 处理面板发起的登录请求，返回面板可直接消费的扁平结构。
//
// 面板路由的响应体由宿主原样写回 HTTP 客户端，不经过 envelope 包装，
// 因此这里返回小写 snake_case 的扁平 JSON，与既有插件面板约定一致。
// [参数] 无。
// [返回] 管理响应。
// 最近修改时间：2026-10-15；改动原因：新增，面板设备码登录入口。
func handlePanelLoginStart() ([]byte, error) {
	state, deviceResp, err := startDeviceLogin()
	if err != nil {
		body, _ := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		return makeHTTPResponse(http.StatusInternalServerError, body)
	}

	intervalSec := deviceResp.Interval
	if intervalSec <= 0 {
		intervalSec = githubDefaultPollSec
	}
	body, _ := json.Marshal(map[string]any{
		"ok":               true,
		"state":            state,
		"user_code":        deviceResp.UserCode,
		"verification_uri": deviceResp.VerificationURI,
		"interval":         intervalSec,
	})
	return makeHTTPResponse(http.StatusOK, body)
}

// handlePanelLoginPoll 处理面板的登录轮询请求，返回扁平状态结构。
// [参数] state: 会话标识。
// [返回] 管理响应。
// 最近修改时间：2026-10-15；改动原因：新增，面板设备码轮询入口。
func handlePanelLoginPoll(state string) ([]byte, error) {
	status, message, _, err := pollDeviceLogin(state)
	if err != nil {
		body, _ := json.Marshal(map[string]any{"status": "error", "message": err.Error()})
		return makeHTTPResponse(http.StatusOK, body)
	}
	body, _ := json.Marshal(map[string]any{"status": string(status), "message": message})
	return makeHTTPResponse(http.StatusOK, body)
}

// handleRefreshAuth 处理凭据刷新请求：重取 Copilot 内部令牌。
// [参数] raw: 宿主刷新请求载荷。
// [返回] 刷新结果信封与错误。
// 最近修改时间：2026-10-15；改动原因：刷新时同步持久化 endpoints.api（关闭 GAP-002）。
func handleRefreshAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &req)
	}

	var sa StoredAuth
	if len(req.StorageJSON) > 0 {
		_ = json.Unmarshal(req.StorageJSON, &sa)
	}

	nextRefresh := time.Now().Add(12 * time.Hour)
	if githubToken := strings.TrimSpace(sa.GitHubToken); githubToken != "" {
		if result, err := exchangeCopilotToken(githubToken); err == nil {
			sa.CopilotToken = result.Token
			sa.APIKey = result.Token
			sa.UpdatedAt = time.Now().Format(time.RFC3339)
			if result.AccountAPI != "" {
				sa.APIEndpoint = result.AccountAPI
			}
			if !result.ExpiresAt.IsZero() {
				sa.ExpiresAt = result.ExpiresAt.Format(time.RFC3339)
				// 按令牌寿命的 80% 安排下次刷新，下限 5 分钟。
				if remain := time.Until(result.ExpiresAt); remain > 0 {
					nextRefresh = time.Now().Add(remain * 4 / 5)
					if nextRefresh.Before(time.Now().Add(5 * time.Minute)) {
						nextRefresh = time.Now().Add(5 * time.Minute)
					}
				}
			}
			// 顺带刷新模型清单：存量账号的 models 字段可能仍是历史兜底值，
			// 不刷新会让面板与宿主按账号注册长期显示失真清单。
			if models, errModels := fetchUpstreamModels(context.Background(), &sa); errModels == nil && len(models) > 0 {
				sa.Models = models
			}
			if path := strings.TrimSpace(sa.PhysicalPath); path != "" {
				_ = writeAuthFileDirect(path, &sa)
			}
		}
	}

	refreshed := pluginapi.AuthData{
		Provider:    providerName,
		ID:          sa.AuthID,
		FileName:    sa.AuthID + ".json",
		Label:       sa.Label,
		Disabled:    sa.Disabled,
		StorageJSON: req.StorageJSON,
		Metadata:    req.Metadata,
		Attributes:  req.Attributes,
	}
	if rawStorage, err := json.Marshal(&sa); err == nil {
		refreshed.StorageJSON = rawStorage
	}
	return okEnvelope(pluginapi.AuthRefreshResponse{
		Auth:             refreshed,
		NextRefreshAfter: nextRefresh,
	})
}
