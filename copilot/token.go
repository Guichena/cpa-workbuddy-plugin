package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// Copilot 内部令牌交换端点与刷新策略常量。
//
// 关键事实（来源：ericc-ch/copilot-api master 分支
// src/services/github/get-copilot-token.ts，2026-10-15 调研）：
// GitHub access token 不能直接用于 Copilot 推理，必须先换取短期内部令牌；
// 该端点要求 GitHub API 版本头与 VS Code 编辑器指纹头。
const (
	copilotTokenURL = "https://api.github.com/copilot_internal/v2/token"
	// copilotTokenRefreshRatio 是刷新提前量：按令牌剩余寿命的该比例提前重取。
	copilotTokenRefreshRatio = 4.0 / 5.0
	// copilotTokenMinRefresh 是刷新间隔下限，避免短寿命令牌导致高频重取。
	copilotTokenMinRefresh = 5 * time.Minute
	// copilotTokenFallbackTTL 是上游未返回有效期时的兜底刷新周期。
	copilotTokenFallbackTTL = 25 * time.Minute
)

// copilotTokenResult 是令牌交换结果。
type copilotTokenResult struct {
	Token      string
	ExpiresAt  time.Time
	RefreshIn  time.Duration
	AccountAPI string
}

var (
	copilotTokenMu    sync.Mutex
	copilotTokenCache = make(map[string]copilotTokenResult)

	// copilotTokenURLForTest 是测试注入点：非空时覆盖令牌交换端点。
	copilotTokenURLForTest string
)

// exchangeCopilotToken 用 GitHub access token 换取 Copilot 内部令牌。
//
// 1. 请求端点固定为 api.github.com/copilot_internal/v2/token；
// 2. 鉴权用 GitHub token（`token` 形式）并附 GitHub API 版本头与编辑器指纹；
// 3. 响应解析 token / expires_at / refresh_in，并解析 endpoints.api（上游权威推理端点）。
// [参数] githubToken: GitHub access token。
// [返回] 令牌交换结果（含端点与过期时间）与错误。
// 最近修改时间：2026-10-15；改动原因：返回完整结果，使 endpoints.api 可被持久化消费（关闭 GAP-002）。
func exchangeCopilotToken(githubToken string) (copilotTokenResult, error) {
	githubToken = strings.TrimSpace(githubToken)
	if githubToken == "" {
		return copilotTokenResult{}, fmt.Errorf("empty github token")
	}

	result, err := fetchCopilotToken(githubToken)
	if err != nil {
		return copilotTokenResult{}, err
	}

	copilotTokenMu.Lock()
	copilotTokenCache[githubToken] = result
	copilotTokenMu.Unlock()

	return result, nil
}

// fetchCopilotToken 发起一次令牌交换请求并解析响应。
// [参数] githubToken: GitHub access token。
// [返回] 令牌交换结果与错误。
// 最近修改时间：2026-10-15；改动原因：新增，抽出纯传输内核便于测试注入。
func fetchCopilotToken(githubToken string) (copilotTokenResult, error) {
	endpoint := copilotTokenURL
	if copilotTokenURLForTest != "" {
		endpoint = copilotTokenURLForTest
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return copilotTokenResult{}, fmt.Errorf("create copilot token request: %w", err)
	}
	applyGitHubHeaders(req.Header, githubToken, currentConfig())

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return copilotTokenResult{}, fmt.Errorf("request copilot token: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return copilotTokenResult{}, fmt.Errorf("read copilot token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return copilotTokenResult{}, fmt.Errorf("copilot token upstream error %d: %s", resp.StatusCode, string(raw))
	}

	var payload struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
		RefreshIn int64  `json:"refresh_in"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return copilotTokenResult{}, fmt.Errorf("parse copilot token response: %w", err)
	}
	if strings.TrimSpace(payload.Token) == "" {
		return copilotTokenResult{}, fmt.Errorf("Copilot 令牌响应无效：缺少 token")
	}

	result := copilotTokenResult{
		Token:      strings.TrimSpace(payload.Token),
		AccountAPI: strings.TrimSpace(gjson.GetBytes(raw, "endpoints.api").String()),
	}
	if payload.ExpiresAt > 0 {
		result.ExpiresAt = time.Unix(payload.ExpiresAt, 0)
	}
	if payload.RefreshIn > 0 {
		result.RefreshIn = time.Duration(payload.RefreshIn) * time.Second
	}
	return result, nil
}

// copilotTokenRefreshDelay 计算下次刷新 Copilot 令牌的等待时长。
//
// 优先用上游返回的 refresh_in；否则按 expires_at 剩余寿命的 80% 提前重取；
// 两者皆缺时回退固定周期。任何情况下不低于下限，避免高频重取触发风控。
// [参数] result: 令牌交换结果。
// [返回] 下次刷新前的等待时长。
// 最近修改时间：2026-10-15；改动原因：新增，令牌刷新节奏计算。
func copilotTokenRefreshDelay(result copilotTokenResult) time.Duration {
	delay := time.Duration(0)
	switch {
	case result.RefreshIn > 0:
		delay = result.RefreshIn
	case !result.ExpiresAt.IsZero():
		if remain := time.Until(result.ExpiresAt); remain > 0 {
			delay = time.Duration(float64(remain) * copilotTokenRefreshRatio)
		}
	default:
		delay = copilotTokenFallbackTTL
	}
	if delay < copilotTokenMinRefresh {
		delay = copilotTokenMinRefresh
	}
	return delay
}

// cachedCopilotToken 返回未过期且仍在刷新窗口内的缓存令牌。
// [参数] githubToken: GitHub access token。
// [返回] 缓存的令牌结果；无可用缓存时返回 false。
// 最近修改时间：2026-10-15；改动原因：新增，避免每次请求都换取令牌。
func cachedCopilotToken(githubToken string) (copilotTokenResult, bool) {
	githubToken = strings.TrimSpace(githubToken)
	if githubToken == "" {
		return copilotTokenResult{}, false
	}
	copilotTokenMu.Lock()
	defer copilotTokenMu.Unlock()

	result, ok := copilotTokenCache[githubToken]
	if !ok || strings.TrimSpace(result.Token) == "" {
		return copilotTokenResult{}, false
	}
	// 已进入刷新窗口或已过期即视为不可用，触发重新换取。
	if !result.ExpiresAt.IsZero() && time.Until(result.ExpiresAt) <= copilotTokenMinRefresh {
		return copilotTokenResult{}, false
	}
	return result, true
}

// ensureCopilotToken 确保账号持有可用的 Copilot 内部令牌。
//
// 1. 账号已有未过期令牌时直接复用；
// 2. 否则用 GitHub token 换取新令牌并回写账号（含物理落盘）；
// 3. 换取失败时保留原凭据不变，返回错误交由上层换号重试。
// [参数] sa: 目标账号凭据。
// [返回] 可用的 Copilot 令牌与错误。
// 最近修改时间：2026-10-15；改动原因：换取的 endpoints.api 同步回写账号，供 base URL 解析消费。
func ensureCopilotToken(sa *StoredAuth) (string, error) {
	if sa == nil {
		return "", fmt.Errorf("nil account")
	}

	// 1. 账号自带令牌且未过期时直接复用。
	if token := strings.TrimSpace(sa.CopilotToken); token != "" {
		if exp := parseExpiry(sa.ExpiresAt); exp.IsZero() || time.Now().Add(copilotTokenMinRefresh).Before(exp) {
			return token, nil
		}
	}

	githubToken := strings.TrimSpace(sa.GitHubToken)
	if githubToken == "" {
		// 无 GitHub token 时回退把 APIKey 当 GitHub token 用（导入型账号）。
		githubToken = strings.TrimSpace(sa.APIKey)
	}
	if githubToken == "" {
		return "", fmt.Errorf("account %s has no usable credential", sa.AuthID)
	}

	result, err := fetchCopilotToken(githubToken)
	if err != nil {
		return "", err
	}

	copilotTokenMu.Lock()
	copilotTokenCache[githubToken] = result
	copilotTokenMu.Unlock()

	sa.CopilotToken = result.Token
	sa.APIKey = result.Token
	sa.GitHubToken = githubToken
	sa.UpdatedAt = time.Now().Format(time.RFC3339)
	if !result.ExpiresAt.IsZero() {
		sa.ExpiresAt = result.ExpiresAt.Format(time.RFC3339)
	}
	if result.AccountAPI != "" {
		sa.APIEndpoint = result.AccountAPI
	}
	if path := strings.TrimSpace(sa.PhysicalPath); path != "" {
		_ = writeAuthFileDirect(path, sa)
	}
	return result.Token, nil
}

// parseExpiry 解析 RFC3339 格式的过期时间。
// [参数] raw: 时间字符串。
// [返回] 解析结果；空值或非法值返回零值时间。
func parseExpiry(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return t
}
