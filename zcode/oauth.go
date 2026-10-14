package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Z.ai 官方 OAuth 配置常量。
//
// 关键事实（2026-10-14 线上实测）：服务端 `POST /api/v1/oauth/cli/init` 的 `provider`
// 参数决定签发哪个站点的授权地址，且只有两个有效值——`zai`（国际站 `chat.z.ai`）与
// `bigmodel`（中国站 `bigmodel.cn/login`）；其余取值一律返回 `code:1000`。
// 授权地址由服务端签发，插件不得改写，因此插件侧只需传对 provider 值。
//
// 因此插件复刻官方 CLI 轮询链路，而不是让浏览器回调 CPA：
//
//  1. POST /api/v1/oauth/cli/init（Authorization: Bearer <随机 32 字节 hex>）
//     → 返回 flow_id / poll_token / authorize_url（回调已由服务端注册）；
//  2. 用户在 authorize_url 完成登录，浏览器最终落在官方回调页；
//  3. GET /api/v1/oauth/cli/poll/<flow_id>（Authorization: Bearer <poll_token>）
//     → status=pending 继续轮询；status=ready 时一次性返回 token / access_token / user。
const (
	zaiOAuthClientID    = "client_P8X5CMWmlaRO9gyO-KSqtg"
	zaiOAuthCLIInitURL  = "https://zcode.z.ai/api/v1/oauth/cli/init"
	zaiOAuthCLIPollURL  = "https://zcode.z.ai/api/v1/oauth/cli/poll/"
	zaiOAuthTokenURL    = "https://zcode.z.ai/api/v1/oauth/token"
	zaiBusinessLoginURL = "https://api.z.ai/api/auth/z/login"
	oauthTTL            = 15 * time.Minute
)

// isValidOAuthProvider 校验 provider 是否为服务端支持的两个取值之一。
//
// 服务端只认 `zai` / `bigmodel`，其余取值会返回 `code:1000 something went wrong`；
// 在插件侧提前拦截非法值，避免把无效请求打到上游并给出明确错误。
// [参数] provider: 待校验的 provider 取值。
// [返回] true 表示合法。
// 最近修改时间：2026-10-14；改动原因：新增，拦截非法 provider 取值。
func isValidOAuthProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case providerFamilyZai, providerFamilyBigmodel:
		return true
	default:
		return false
	}
}

// resolveOAuthProvider 校验并归一化入口传入的 provider 取值。
//
// 入口层必须对**原始值**调用本函数：先校验再归一化，非法值返回明确错误而不静默回退。
// 若先归一化再校验，`zhipu`/`zai-cn` 等非法值会被提前改写成 `zai`，导致校验形同虚设。
// 空值（请求未带 provider）按默认 `zai` 处理，保持向后兼容。
// [参数] raw: 入口收到的原始 provider 取值。
// [返回] 归一化后的 family 标识；非法值返回错误。
// 最近修改时间：2026-10-14；改动原因：新增，修复非法 provider 被归一化后静默回退为 zai 的缺陷。
func resolveOAuthProvider(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return providerFamilyZai, nil
	}
	if !isValidOAuthProvider(trimmed) {
		return "", fmt.Errorf("不支持的 OAuth provider: %q（仅支持 zai / bigmodel）", raw)
	}
	return normalizeProviderFamily(trimmed), nil
}

// providerFromMetadata 从宿主登录请求的 Metadata 中读取 provider 站点归属。
//
// 宿主 `AuthLoginStartRequest.Metadata` 是插件自定义登录上下文的载体；面板通过
// 该字段指定 `zai` / `bigmodel`。本函数只负责取出原始取值（nil / 非字符串按默认
// `zai` 处理），校验与归一化交由 startAccountOAuth 的 resolveOAuthProvider 完成，
// 避免在此提前归一化导致非法值被静默改写成 `zai`。
// [参数] metadata: 宿主请求携带的自定义上下文。
// [返回] 原始 provider 取值；缺失或非字符串时返回默认 `zai`。
// 最近修改时间：2026-10-14；改动原因：不再提前归一化，改为透传原始值以便入口层校验。
func providerFromMetadata(metadata map[string]any) string {
	if metadata == nil {
		return providerFamilyZai
	}
	if raw, ok := metadata["provider"].(string); ok {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			return trimmed
		}
	}
	return providerFamilyZai
}

var (
	oauthHTTPClient = &http.Client{Timeout: 30 * time.Second}

	// 测试注入点：非空时覆盖 CLI init / poll 端点前缀。
	mockOAuthCLIInitURL string
	mockOAuthCLIPollURL string
)

// oauthLoginSession 承载一次 CPA OAuth 登录或账号定向激活的轮询状态。
type oauthLoginSession struct {
	CreatedAt    time.Time
	FlowID       string
	PollToken    string
	AuthorizeURL string
	PollErr      string
	Auth         *pluginapi.AuthData
	Error        string
	Completed    bool
	TargetAuthID string // 若非空，表示针对存量账号执行激活/更新，就地覆盖该账号凭据
	TargetLabel  string // 存量账号的保留展示名称
	Provider     string // 本次授权使用的站点归属（`zai` / `bigmodel`），落盘时写入账号
}

var oauthSessions sync.Map

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// zaiCLIInitRequest 是 /api/v1/oauth/cli/init 的请求体。
type zaiCLIInitRequest struct {
	Provider string `json:"provider"`
}

// zaiCLIInitResponse 是 /api/v1/oauth/cli/init 的响应体。
type zaiCLIInitResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		FlowID          string `json:"flow_id"`
		PollToken       string `json:"poll_token"`
		AuthorizeURL    string `json:"authorize_url"`
		ExpiresAt       int64  `json:"expires_at"`
		PollIntervalSec int64  `json:"poll_interval_sec"`
	} `json:"data"`
}

// zaiCLIPollResponse 是 /api/v1/oauth/cli/poll/<flow_id> 的响应体。
type zaiCLIPollResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Status string `json:"status"` // pending | ready | failed
		Token  string `json:"token"`  // zcode JWT
		Zai    struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			TokenType    string `json:"token_type"`
		} `json:"zai"`
		Bigmodel struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"bigmodel"`
		User *ZaiUserInfo `json:"user"`
	} `json:"data"`
}

// zaiCLIInit 发起官方 CLI OAuth 流程并返回授权地址与轮询凭证。
func zaiCLIInit(provider string) (*zaiCLIInitResponse, error) {
	initURL := zaiOAuthCLIInitURL
	if mockOAuthCLIInitURL != "" {
		initURL = mockOAuthCLIInitURL
	}

	// 桌面端契约：轮询凭证在 Authorization 头中，先于 poll_token 返回。
	pollToken := randomHex(32)
	body, _ := json.Marshal(zaiCLIInitRequest{Provider: provider})

	httpReq, err := http.NewRequest(http.MethodPost, initURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create cli init request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+pollToken)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := oauthHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request cli init: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read cli init response: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("cli init upstream error %d: %s", httpResp.StatusCode, string(raw))
	}

	var out zaiCLIInitResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse cli init response: %w", err)
	}
	if out.Code != 0 {
		msg := strings.TrimSpace(out.Msg)
		if msg == "" {
			msg = fmt.Sprintf("code=%d", out.Code)
		}
		return nil, fmt.Errorf("OAuth flow 初始化失败: %s", msg)
	}
	if strings.TrimSpace(out.Data.FlowID) == "" || strings.TrimSpace(out.Data.AuthorizeURL) == "" {
		return nil, fmt.Errorf("OAuth flow 初始化响应无效：缺少 flow_id 或 authorize_url")
	}
	rawURL := strings.TrimSpace(out.Data.AuthorizeURL)
	rawURL = html.UnescapeString(rawURL)
	rawURL = strings.ReplaceAll(rawURL, "&amp;", "&")
	out.Data.AuthorizeURL = rawURL

	if strings.TrimSpace(out.Data.PollToken) == "" {
		out.Data.PollToken = pollToken
	}
	return &out, nil
}

// zaiCLIPoll 查询一次授权结果。ready 表示用户已完成授权。
func zaiCLIPoll(flowID, pollToken string) (*zaiCLIPollResponse, error) {
	base := zaiOAuthCLIPollURL
	if mockOAuthCLIPollURL != "" {
		base = mockOAuthCLIPollURL
	}
	pollURL := base + url.PathEscape(strings.TrimSpace(flowID))

	httpReq, err := http.NewRequest(http.MethodGet, pollURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create cli poll request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(pollToken))
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := oauthHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request cli poll: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read cli poll response: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("cli poll upstream error %d: %s", httpResp.StatusCode, string(raw))
	}

	var out zaiCLIPollResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse cli poll response: %w", err)
	}
	if out.Code != 0 {
		msg := strings.TrimSpace(out.Msg)
		if msg == "" {
			msg = fmt.Sprintf("code=%d", out.Code)
		}
		return nil, fmt.Errorf("OAuth flow 查询失败: %s", msg)
	}
	return &out, nil
}

// settleZaiPollResult 将 ready 状态的轮询结果落盘为账号，并构造宿主凭据记录。
// 若提供了 targetAuthID，优先在存量凭据中就地更新该账号的 APIKey、Token、AccessToken 及用户信息，
// 保持原有 AuthID 不变，彻底避免新建重复账号卡片；若未提供则正常落盘为新账号。
// [参数] resp: 轮询响应；label: 账号备注名；targetAuthID: 定向更新目标；provider: 站点归属。
// [返回] 宿主凭据记录与错误。
// 最近修改时间：2026-10-14；改动原因：落盘时写入 provider family 归属。
func settleZaiPollResult(resp *zaiCLIPollResponse, label string, targetAuthID string, provider string) (*pluginapi.AuthData, error) {
	family := normalizeProviderFamily(provider)
	status := strings.TrimSpace(resp.Data.Status)
	switch status {
	case "failed":
		return nil, fmt.Errorf("OAuth flow 授权失败")
	case "ready":
		// 继续
	default:
		return nil, fmt.Errorf("OAuth flow 查询响应无效：status=%s", status)
	}

	jwtToken := strings.TrimSpace(resp.Data.Token)
	accessToken := strings.TrimSpace(resp.Data.Zai.AccessToken)
	if accessToken == "" {
		accessToken = strings.TrimSpace(resp.Data.Bigmodel.AccessToken)
	}
	if jwtToken == "" || accessToken == "" {
		return nil, fmt.Errorf("OAuth flow 查询响应无效：缺少 token 或 access_token")
	}

	var userID string
	if resp.Data.User != nil {
		userID = strings.TrimSpace(resp.Data.User.UserID)
	}
	if userID == "" {
		return nil, fmt.Errorf("OAuth flow 查询响应无效：缺少 user_id")
	}

	expiresAt := time.Now().Add(30 * 24 * time.Hour)

	var target *StoredAuth
	targetID := strings.TrimSpace(targetAuthID)
	if targetID != "" {
		accounts, _ := listAllAuthFiles()
		for _, acc := range accounts {
			if acc.AuthID == targetID {
				target = acc
				break
			}
		}
	}

	var stored *StoredAuth
	var errSave error

	if target != nil {
		target.Token = jwtToken
		target.APIKey = jwtToken
		target.AccessToken = accessToken
		target.ExpiresAt = expiresAt.Format(time.RFC3339)
		target.UpdatedAt = time.Now().Format(time.RFC3339)
		target.Disabled = false
		target.ProviderFamily = family
		target.TestFailed = false
		if resp.Data.User != nil {
			if target.UserID == "" {
				target.UserID = strings.TrimSpace(resp.Data.User.UserID)
			}
			if target.Username == "" {
				target.Username = strings.TrimSpace(resp.Data.User.Name)
			}
			if target.Email == "" {
				target.Email = strings.TrimSpace(resp.Data.User.Email)
			}
			if target.AvatarURL == "" {
				target.AvatarURL = strings.TrimSpace(resp.Data.User.Avatar)
			}
			if target.Label == "" || strings.HasPrefix(target.Label, "ZCode ") {
				if target.Username != "" {
					target.Label = "ZCode " + target.Username
				}
			}
		}
		if label != "" {
			target.Label = label
		}
		fullPath := target.PhysicalPath
		if fullPath == "" {
			fullPath = filepath.Join(getAccountsDir(), authFileNameFor(target.APIKey))
		}
		errSave = writeAuthFileDirect(fullPath, target)
		stored = target
	} else {
		stored, errSave = saveZaiAccount(jwtToken, accessToken, "", resp.Data.User, expiresAt, label, family)
	}

	if errSave != nil {
		return nil, fmt.Errorf("save account: %w", errSave)
	}
	invalidateQuota(stored.AuthID)
	globalQuotaRefresh.EnqueueOne(stored.AuthID)

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

// startAccountOAuth 发起一次官方 CLI OAuth 流程并保存会话状态。
// [参数] targetAuthID: 可选的定向激活目标账号；label: 账号备注名；provider: 站点归属
// （`zai` 国际站 / `bigmodel` 中国站），空值按 `zai` 处理，非法值返回错误。
// [返回] 官方初始化响应、state 状态码、错误。
// 最近修改时间：2026-10-14；改动原因：provider 校验改走 resolveOAuthProvider，避免非法值被静默归一化。
func startAccountOAuth(targetAuthID, label, provider string) (*zaiCLIInitResponse, string, error) {
	family, err := resolveOAuthProvider(provider)
	if err != nil {
		return nil, "", err
	}

	initResp, err := zaiCLIInit(family)
	if err != nil {
		return nil, "", err
	}

	authorizeURL := strings.TrimSpace(initResp.Data.AuthorizeURL)
	authorizeURL = html.UnescapeString(authorizeURL)
	authorizeURL = strings.ReplaceAll(authorizeURL, "&amp;", "&")
	initResp.Data.AuthorizeURL = authorizeURL

	state := ""
	if u, errParse := url.Parse(authorizeURL); errParse == nil {
		state = strings.TrimSpace(u.Query().Get("state"))
	}
	if state == "" {
		return nil, "", fmt.Errorf("OAuth flow 初始化响应无效：authorize_url 缺少 state")
	}

	oauthSessions.Store(state, &oauthLoginSession{
		CreatedAt:    time.Now(),
		FlowID:       initResp.Data.FlowID,
		PollToken:    initResp.Data.PollToken,
		AuthorizeURL: authorizeURL,
		TargetAuthID: strings.TrimSpace(targetAuthID),
		TargetLabel:  strings.TrimSpace(label),
		Provider:     family,
	})

	return initResp, state, nil
}

// handleStartLogin 处理宿主发起 OAuth 登录请求：发起官方 CLI 轮询流程并返回真实授权地址。
// provider 从宿主请求的 Metadata 读取（缺省 `zai` 国际站），非法值返回明确错误。
// [参数] raw: 宿主登录启动请求载荷，Metadata 可携带 provider 站点归属。
// [返回] 授权启动响应与错误。
// 最近修改时间：2026-10-14；改动原因：支持宿主按 provider 发起中国站 bigmodel 授权。
func handleStartLogin(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginStartRequest
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &req)
	}
	provider := providerFromMetadata(req.Metadata)

	initResp, state, err := startAccountOAuth("", "", provider)
	if err != nil {
		return nil, err
	}

	authorizeURL := strings.TrimSpace(initResp.Data.AuthorizeURL)
	expiresAt := time.Now().Add(oauthTTL)
	if initResp.Data.ExpiresAt > 0 {
		expiresAt = time.Unix(initResp.Data.ExpiresAt, 0)
	}
	bridgeURL := "/v0/resource/plugins/" + providerName + "/oauth-bridge?state=" + url.QueryEscape(state)

	return okEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  providerName,
		URL:       authorizeURL,
		State:     state,
		ExpiresAt: expiresAt.UTC(),
		Metadata: map[string]any{
			"logo":       pluginLogoURL,
			"auth_url":   authorizeURL,
			"bridge_url": bridgeURL,
			"flow_id":    initResp.Data.FlowID,
			"prompt":     "请在打开的 Z.ai 页面完成登录授权；授权完成后本插件会自动轮询并绑定账号，无需手工复制回调地址。",
		},
	})
}

// handlePollLogin 轮询官方 CLI 端点，ready 后落盘账号并返回成功。
func handlePollLogin(raw []byte) ([]byte, error) {
	var req pluginapi.AuthLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("poll: %w", err)
	}
	state := strings.TrimSpace(req.State)
	if state == "" {
		return nil, fmt.Errorf("poll: empty state")
	}

	rawSession, ok := oauthSessions.Load(state)
	if !ok {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "授权会话不存在或已过期",
		})
	}
	session, ok := rawSession.(*oauthLoginSession)
	if !ok || session == nil {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "授权会话数据异常",
		})
	}

	if time.Since(session.CreatedAt) > oauthTTL {
		oauthSessions.Delete(state)
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "授权会话已超时（15分钟），请重新发起",
		})
	}

	// 1. 已完成的会话直接返回结果（宿主可能重复轮询）。
	if session.Completed {
		oauthSessions.Delete(state)
		if session.Error != "" {
			return okEnvelope(pluginapi.AuthLoginPollResponse{
				Status:  pluginapi.AuthLoginStatusError,
				Message: session.Error,
			})
		}
		if session.Auth == nil {
			return okEnvelope(pluginapi.AuthLoginPollResponse{
				Status:  pluginapi.AuthLoginStatusError,
				Message: "缺少凭据信息",
			})
		}
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusSuccess,
			Message: "ZCode (Z.ai) 账号授权成功",
			Auth:    *session.Auth,
		})
	}

	// 2. 查询官方 CLI 轮询端点。
	pollResp, errPoll := zaiCLIPoll(session.FlowID, session.PollToken)
	if errPoll != nil {
		// 传输类失败保留会话，等待宿主下一轮轮询重试。
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "等待授权结果（上游暂时不可达，稍后自动重试）...",
		})
	}

	switch strings.TrimSpace(pollResp.Data.Status) {
	case "pending":
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "等待在 Z.ai 页面完成授权登录...",
		})
	case "failed":
		session.Completed = true
		session.Error = "OAuth flow 授权失败：请重新发起登录"
		oauthSessions.Delete(state)
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: session.Error,
		})
	case "ready":
		authData, errSettle := settleZaiPollResult(pollResp, session.TargetLabel, session.TargetAuthID, session.Provider)
		if errSettle != nil {
			session.Completed = true
			session.Error = errSettle.Error()
			oauthSessions.Delete(state)
			return okEnvelope(pluginapi.AuthLoginPollResponse{
				Status:  pluginapi.AuthLoginStatusError,
				Message: errSettle.Error(),
			})
		}
		session.Auth = authData
		session.Completed = true
		oauthSessions.Delete(state)
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusSuccess,
			Message: "ZCode (Z.ai) 账号授权成功",
			Auth:    *authData,
		})
	default:
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "等待在 Z.ai 页面完成授权登录...",
		})
	}
}

// handleRefreshAuth 处理凭据刷新请求。
func handleRefreshAuth(raw []byte) ([]byte, error) {
	var req struct {
		Auth pluginapi.AuthData `json:"auth"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &req)
	}
	return okEnvelope(pluginapi.AuthRefreshResponse{
		Auth:             req.Auth,
		NextRefreshAfter: time.Now().Add(15 * 24 * time.Hour),
	})
}

// handleOAuthBridge 处理 /v0/resource/plugins/zcode-provider/oauth-bridge 桥接页。
//
// 官方 CLI 轮询链路下，桥接页只承担「引导跳转 + 结果查询」：授权结果由插件
// 主动轮询官方端点获得，不再依赖用户手工回传回调地址。
func handleOAuthBridge(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	q := req.Query
	state := strings.TrimSpace(q.Get("state"))

	if state == "" {
		return oauthBridgeHTML("缺少 state 参数", "请从 CLIProxyAPI 管理后台的「OAuth 登录」中点击 ZCode 重新发起。", "", false)
	}

	rawSession, ok := oauthSessions.Load(state)
	if !ok {
		return oauthBridgeHTML("会话已失效", "授权会话不存在或已完成，请回到管理后台重新点击发起登录。", "", false)
	}
	session, ok := rawSession.(*oauthLoginSession)
	if !ok || session == nil {
		return oauthBridgeHTML("会话异常", "会话数据损坏，请重新发起登录。", "", false)
	}

	if time.Since(session.CreatedAt) > oauthTTL {
		oauthSessions.Delete(state)
		return oauthBridgeHTML("会话已超时", "授权会话已超过 15 分钟有效期，请回到管理后台重新发起。", "", false)
	}

	if session.Completed {
		if session.Error != "" {
			return oauthBridgeHTML("授权失败", session.Error, state, false)
		}
		return oauthBridgeHTML("授权绑定成功！", "已成功通过 Z.ai 授权绑定账号。您可以关闭此窗口，回到 CLIProxyAPI 管理后台查看已完成的授权卡片。", state, true)
	}

	// 主动查询一次，便于用户在桥接页即时看到结果。
	if session.FlowID != "" {
		if pollResp, errPoll := zaiCLIPoll(session.FlowID, session.PollToken); errPoll == nil {
			if authData, errSettle := settleZaiPollResult(pollResp, session.TargetLabel, session.TargetAuthID, session.Provider); errSettle == nil {
				session.Auth = authData
				session.Completed = true
				accountLabel := authData.Label
				if accountLabel == "" {
					accountLabel = authData.ID
				}
				return oauthBridgeHTML("授权绑定成功！", "已成功通过 Z.ai 授权绑定账号「"+accountLabel+"」。您可以关闭此窗口，回到 CLIProxyAPI 管理后台查看已完成的授权卡片。", state, true)
			}
		}
	}

	return oauthBridgeFormHTML(state, session.AuthorizeURL)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
	return r.Replace(s)
}

func oauthBridgeHTML(title, message, state string, success bool) pluginapi.ManagementResponse {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<title>ZCode 授权绑定</title>")
	b.WriteString("<style>")
	b.WriteString("body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;background:#0f172a;color:#f8fafc;margin:0;padding:40px 16px;display:flex;justify-content:center;align-items:center;min-height:80vh}")
	b.WriteString(".card{background:#1e293b;border:1px solid #334155;border-radius:12px;box-shadow:0 10px 25px -5px rgba(0,0,0,.5);max-width:540px;width:100%;padding:32px;box-sizing:border-box}")
	b.WriteString("h1{font-size:20px;margin:0 0 16px;display:flex;align-items:center;gap:10px}")
	b.WriteString("p{line-height:1.7;margin:10px 0;font-size:14px;color:#94a3b8}")
	b.WriteString(".btn{display:inline-block;margin-top:16px;background:#3b82f6;color:#fff;border:none;border-radius:6px;padding:10px 20px;font-size:14px;font-weight:500;text-decoration:none;cursor:pointer}")
	b.WriteString(".btn:hover{background:#2563eb}")
	if success {
		b.WriteString(".box{background:rgba(34,197,94,.1);border:1px solid rgba(34,197,94,.3);color:#4ade80;border-radius:8px;padding:16px;margin-top:16px}")
	} else {
		b.WriteString(".box{background:rgba(239,68,68,.1);border:1px solid rgba(239,68,68,.3);color:#f87171;border-radius:8px;padding:16px;margin-top:16px}")
	}
	b.WriteString("</style></head><body><div class=\"card\">")
	b.WriteString("<h1>" + htmlEscape(title) + "</h1>")
	b.WriteString("<div class=\"box\"><p style=\"color:inherit;margin:0;\">" + htmlEscape(message) + "</p></div>")
	if success {
		b.WriteString("<p style=\"margin-top:20px;\">窗口将在 3 秒后尝试自动关闭…</p>")
		b.WriteString("<script>setTimeout(function(){try{window.close();}catch(e){}},3000);</script>")
	} else if state != "" {
		b.WriteString("<a class=\"btn\" href=\"/v0/resource/plugins/zcode-provider/oauth-bridge?state=" + url.QueryEscape(state) + "\">重新检查授权结果</a>")
	}
	b.WriteString("</div></body></html>")

	h := http.Header{}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: h, Body: []byte(b.String())}
}

func oauthBridgeFormHTML(state, authURL string) pluginapi.ManagementResponse {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<title>ZCode 账号授权（Z.ai 官方登录）</title>")
	b.WriteString("<style>")
	b.WriteString("body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;background:#0f172a;color:#f8fafc;margin:0;padding:40px 16px;display:flex;justify-content:center;align-items:center;min-height:80vh}")
	b.WriteString(".card{background:#1e293b;border:1px solid #334155;border-radius:12px;box-shadow:0 10px 25px -5px rgba(0,0,0,.5);max-width:560px;width:100%;padding:32px;box-sizing:border-box}")
	b.WriteString(".logo-row{display:flex;align-items:center;gap:12px;margin-bottom:16px}")
	b.WriteString(".logo-img{width:36px;height:36px;border-radius:8px}")
	b.WriteString("h1{font-size:20px;margin:0;font-weight:600}")
	b.WriteString(".desc{color:#94a3b8;font-size:13px;line-height:1.6;margin-bottom:20px}")
	b.WriteString(".step-box{background:#0f172a;border:1px solid #334155;border-radius:8px;padding:16px;margin-bottom:20px;font-size:13px;color:#cbd5e1;line-height:1.6}")
	b.WriteString(".step-btn{display:inline-block;background:#3b82f6;color:#fff;border-radius:6px;padding:9px 18px;font-size:13px;font-weight:500;text-decoration:none;margin-top:10px;transition:background .2s}")
	b.WriteString(".step-btn:hover{background:#2563eb}")
	b.WriteString(".note{color:#94a3b8;font-size:12px;line-height:1.6}")
	b.WriteString("</style></head><body>")
	b.WriteString("<div class=\"card\">")
	b.WriteString("<div class=\"logo-row\">")
	b.WriteString("<img class=\"logo-img\" src=\"" + htmlEscape(pluginLogoURL) + "\" alt=\"ZCode\">")
	b.WriteString("<h1>ZCode 账号授权（Z.ai 官方登录）</h1>")
	b.WriteString("</div>")
	b.WriteString("<div class=\"desc\">通过 Z.ai 官方登录连接您的 ZCode 账号。授权完成后插件会自动轮询并绑定账号，无需手工复制回调地址。</div>")
	b.WriteString("<div class=\"step-box\">")
	b.WriteString("<strong>前往官方授权页面</strong><br>")
	b.WriteString("点击下方按钮打开 Z.ai 授权页，使用您的手机号/微信完成登录。<br>")
	b.WriteString("<a class=\"step-btn\" href=\"" + htmlEscape(authURL) + "\" target=\"_blank\" rel=\"noopener noreferrer\">打开 Z.ai 授权页面 ↗</a>")
	b.WriteString("</div>")
	b.WriteString("<p class=\"note\">完成登录后回到 CLIProxyAPI 管理后台，授权卡片会自动变为已绑定；本页刷新可查看最新结果。</p>")
	b.WriteString("</div></body></html>")

	h := http.Header{}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: h, Body: []byte(b.String())}
}
