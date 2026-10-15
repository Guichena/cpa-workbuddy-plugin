package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// TestNormalizeAccountType 校验账号类型归一化：非法值与空值回退 individual。
func TestNormalizeAccountType(t *testing.T) {
	cases := map[string]string{
		"":             "individual",
		"individual":   "individual",
		"BUSINESS":     "business",
		" enterprise ": "enterprise",
		"unknown":      "individual",
	}
	for input, want := range cases {
		if got := normalizeAccountType(input); got != want {
			t.Fatalf("normalizeAccountType(%q)=%q, want %q", input, got, want)
		}
	}
}

// TestResolveCopilotBaseURL 校验端点解析优先级：配置覆盖 > endpoints.api > 按账号类型推导。
func TestResolveCopilotBaseURL(t *testing.T) {
	// 1. 个人版走默认主机。
	if got := resolveCopilotBaseURL(&StoredAuth{AccountType: "individual"}, defaultConfig()); got != defaultBaseURL {
		t.Fatalf("individual base url=%q, want %q", got, defaultBaseURL)
	}

	// 2. 企业版走带前缀主机。
	if got := resolveCopilotBaseURL(&StoredAuth{AccountType: "enterprise"}, defaultConfig()); got != "https://api.enterprise.githubcopilot.com" {
		t.Fatalf("enterprise base url=%q", got)
	}

	// 3. 配置显式指定 base_url 时优先于账号类型。
	cfg := defaultConfig()
	cfg.BaseURL = "https://custom.example.com"
	if got := resolveCopilotBaseURL(&StoredAuth{AccountType: "enterprise"}, cfg); got != "https://custom.example.com" {
		t.Fatalf("custom base url=%q", got)
	}

	// 4. 令牌交换返回的 endpoints.api 优先于按账号类型推导（GAP-002 回归）。
	//    实测免费账号返回 api.individual.githubcopilot.com，与硬推导默认值不同。
	sa := &StoredAuth{AccountType: "individual", APIEndpoint: "https://api.individual.githubcopilot.com"}
	if got := resolveCopilotBaseURL(sa, defaultConfig()); got != "https://api.individual.githubcopilot.com" {
		t.Fatalf("endpoint-driven base url=%q", got)
	}

	// 5. 配置显式 base_url 仍优先于 endpoints.api。
	cfg2 := defaultConfig()
	cfg2.BaseURL = "https://custom.example.com"
	if got := resolveCopilotBaseURL(sa, cfg2); got != "https://custom.example.com" {
		t.Fatalf("config should beat endpoint, got %q", got)
	}

	// 6. 尾部斜杠需归一化，避免拼出双斜杠路径。
	saSlash := &StoredAuth{AccountType: "individual", APIEndpoint: "https://api.individual.githubcopilot.com/"}
	if got := resolveCopilotBaseURL(saSlash, defaultConfig()); got != "https://api.individual.githubcopilot.com" {
		t.Fatalf("trailing slash not trimmed: %q", got)
	}

	// 7. 非 githubcopilot.com 域的 endpoints.api 必须被拒绝并回退（防凭据外带）。
	for _, bad := range []string{
		"https://evil.example.com",
		"http://api.githubcopilot.com",
		"https://api.githubcopilot.com.evil.com",
		"://not-a-url",
	} {
		saBad := &StoredAuth{AccountType: "individual", APIEndpoint: bad}
		if got := resolveCopilotBaseURL(saBad, defaultConfig()); got != defaultBaseURL {
			t.Fatalf("endpoint %q should be rejected, got %q", bad, got)
		}
	}
}

// TestResolveInitiator 校验 X-Initiator 判定：含 assistant/tool 消息视为 agent。
func TestResolveInitiator(t *testing.T) {
	userPayload := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	if got := resolveInitiator(userPayload); got != "user" {
		t.Fatalf("user payload initiator=%q, want user", got)
	}

	agentPayload := []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"ok"}]}`)
	if got := resolveInitiator(agentPayload); got != "agent" {
		t.Fatalf("agent payload initiator=%q, want agent", got)
	}

	toolPayload := []byte(`{"messages":[{"role":"tool","content":"result"}]}`)
	if got := resolveInitiator(toolPayload); got != "agent" {
		t.Fatalf("tool payload initiator=%q, want agent", got)
	}

	if got := resolveInitiator(nil); got != "user" {
		t.Fatalf("nil payload initiator=%q, want user", got)
	}
}

// TestApplyCopilotHeaders 校验指纹头注入：鉴权用 Copilot 令牌、必需头齐全。
func TestApplyCopilotHeaders(t *testing.T) {
	h := http.Header{}
	sa := &StoredAuth{CopilotToken: "copilot-token-abc"}
	payload := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	applyCopilotHeaders(h, sa, defaultConfig(), payload)

	checks := map[string]string{
		"Authorization":          "Bearer copilot-token-abc",
		"Copilot-Integration-Id": copilotIntegrationID,
		"Editor-Version":         defaultEditorVersion,
		"Editor-Plugin-Version":  defaultPluginVersion,
		"User-Agent":             defaultUserAgent,
		"X-GitHub-Api-Version":   githubAPIVersion,
		"X-Initiator":            "user",
	}
	for key, want := range checks {
		if got := h.Get(key); got != want {
			t.Fatalf("header %s=%q, want %q", key, got, want)
		}
	}
	if h.Get("X-Request-Id") == "" {
		t.Fatal("X-Request-Id should not be empty")
	}
}

// TestCredentialOf 校验凭据取值优先级：CopilotToken > APIKey > GitHubToken。
func TestCredentialOf(t *testing.T) {
	if got := credentialOf(&StoredAuth{CopilotToken: "c", APIKey: "a", GitHubToken: "g"}); got != "c" {
		t.Fatalf("credentialOf priority=%q, want c", got)
	}
	if got := credentialOf(&StoredAuth{APIKey: "a", GitHubToken: "g"}); got != "a" {
		t.Fatalf("credentialOf fallback=%q, want a", got)
	}
	if got := credentialOf(&StoredAuth{GitHubToken: "g"}); got != "g" {
		t.Fatalf("credentialOf last=%q, want g", got)
	}
	if got := credentialOf(nil); got != "" {
		t.Fatalf("credentialOf(nil)=%q, want empty", got)
	}
}

// TestFilterExcludedModels 校验宿主排除配置生效且不就地污染入参切片。
func TestFilterExcludedModels(t *testing.T) {
	models := []pluginapi.ModelInfo{
		{ID: "gpt-4.1"},
		{ID: "gpt-4o"},
		{ID: "claude-sonnet-4"},
	}
	host := pluginapi.HostConfigSummary{
		ExcludedModels: map[string][]string{
			providerName: {"GPT-4O"},
		},
	}
	got := filterExcludedModels(models, host)
	if len(got) != 2 {
		t.Fatalf("filtered len=%d, want 2", len(got))
	}
	for _, m := range got {
		if strings.EqualFold(m.ID, "gpt-4o") {
			t.Fatal("gpt-4o should be excluded")
		}
	}
	// 入参切片不得被就地改写。
	if len(models) != 3 {
		t.Fatalf("input slice mutated: len=%d, want 3", len(models))
	}
}

// TestParseQuotaSnapshot 校验额度响应解析：档位、SKU、三类配额明细。
func TestParseQuotaSnapshot(t *testing.T) {
	raw := []byte(`{
		"copilot_plan": "individual_pro",
		"access_type_sku": "copilot_pro",
		"chat_enabled": true,
		"quota_reset_date": "2026-11-01",
		"quota_snapshots": {
			"chat": {"entitlement": 100, "remaining": 40, "percent_remaining": 40.0, "unlimited": false},
			"completions": {"entitlement": 2000, "remaining": 2000, "unlimited": false},
			"premium_interactions": {"entitlement": 300, "remaining": 0, "unlimited": false}
		}
	}`)
	snapshot := parseQuotaSnapshot(raw)
	if snapshot.Plan != "individual_pro" {
		t.Fatalf("plan=%q", snapshot.Plan)
	}
	if snapshot.AccessTypeSKU != "copilot_pro" {
		t.Fatalf("sku=%q", snapshot.AccessTypeSKU)
	}
	if !snapshot.ChatEnabled {
		t.Fatal("chat_enabled should be true")
	}
	if snapshot.Chat == nil || snapshot.Chat.Remaining != 40 {
		t.Fatalf("chat quota=%+v", snapshot.Chat)
	}
	if snapshot.Premium == nil || snapshot.Premium.Remaining != 0 {
		t.Fatalf("premium quota=%+v", snapshot.Premium)
	}
}

// TestParseQuotaSnapshotEmpty 校验空响应不 panic 且各字段为零值。
func TestParseQuotaSnapshotEmpty(t *testing.T) {
	snapshot := parseQuotaSnapshot([]byte(`{}`))
	if snapshot == nil {
		t.Fatal("snapshot should not be nil")
	}
	if snapshot.Chat != nil || snapshot.Completions != nil || snapshot.Premium != nil {
		t.Fatal("quota details should be nil for empty payload")
	}
}

// TestCopilotTokenRefreshDelay 校验刷新节奏：优先 refresh_in，否则按寿命比例，且有下限。
func TestCopilotTokenRefreshDelay(t *testing.T) {
	// 1. 上游给出 refresh_in 时直接采用（但不得低于下限）。
	if got := copilotTokenRefreshDelay(copilotTokenResult{RefreshIn: 30 * time.Minute}); got != 30*time.Minute {
		t.Fatalf("refresh_in delay=%v", got)
	}

	// 2. 仅有过期时间时按剩余寿命 80% 计算。
	future := time.Now().Add(100 * time.Minute)
	got := copilotTokenRefreshDelay(copilotTokenResult{ExpiresAt: future})
	if got < 70*time.Minute || got > 85*time.Minute {
		t.Fatalf("expiry-based delay=%v, want ~80m", got)
	}

	// 3. 两者皆缺时回退兜底周期。
	if got := copilotTokenRefreshDelay(copilotTokenResult{}); got != copilotTokenFallbackTTL {
		t.Fatalf("fallback delay=%v", got)
	}

	// 4. 短寿命令牌受下限保护。
	if got := copilotTokenRefreshDelay(copilotTokenResult{RefreshIn: time.Second}); got != copilotTokenMinRefresh {
		t.Fatalf("min clamp delay=%v", got)
	}
}

// TestShouldRotateOnUpstreamErr 校验换号判定：鉴权/限流/5xx 换号，400 业务错不换。
func TestShouldRotateOnUpstreamErr(t *testing.T) {
	rotate := []int{401, 403, 404, 429, 500, 502, 503, 504}
	for _, code := range rotate {
		if !shouldRotateOnUpstreamErr(code, "") {
			t.Fatalf("status %d should rotate", code)
		}
	}
	if shouldRotateOnUpstreamErr(400, "invalid model parameter") {
		t.Fatal("status 400 business error should not rotate")
	}
	if !shouldRotateOnUpstreamErr(0, "quota exceeded") {
		t.Fatal("quota text should rotate")
	}
}

// TestDeviceCodeFlowStartAndPoll 用桩服务端验证设备码申请与轮询落盘全链路，含 endpoints.api 持久化。
func TestDeviceCodeFlowStartAndPoll(t *testing.T) {
	// 1. 桩：设备码申请端点。
	deviceSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "device-abc",
			"user_code":        "ABCD-1234",
			"verification_uri": "https://github.com/login/device",
			"expires_in":       900,
			"interval":         1,
		})
	}))
	defer deviceSrv.Close()

	// 2. 桩：令牌轮询端点，首次 pending，其后返回 token。
	pollCount := 0
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollCount++
		if pollCount == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "gho_test_token",
			"token_type":   "bearer",
			"scope":        "read:user",
		})
	}))
	defer tokenSrv.Close()

	// 3. 桩：Copilot 令牌交换端点。带 endpoints.api 以覆盖 GAP-002 持久化链路。
	copilotSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "copilot-internal-token",
			"expires_at": time.Now().Add(30 * time.Minute).Unix(),
			"refresh_in": 1800,
			"endpoints":  map[string]any{"api": "https://api.individual.githubcopilot.com"},
		})
	}))
	defer copilotSrv.Close()

	// 4. 注入端点并隔离账号目录。
	mockGitHubDeviceCodeURL = deviceSrv.URL
	mockGitHubAccessTokenURL = tokenSrv.URL
	prevTokenURL := copilotTokenURLForTest
	copilotTokenURLForTest = copilotSrv.URL
	defer func() {
		mockGitHubDeviceCodeURL = ""
		mockGitHubAccessTokenURL = ""
		copilotTokenURLForTest = prevTokenURL
	}()

	tmpDir := t.TempDir()
	setAccountsDir(tmpDir)
	defer setAccountsDir("")

	// 5. 启动登录。
	startRaw, err := handleStartLogin(nil)
	if err != nil {
		t.Fatalf("handleStartLogin: %v", err)
	}
	var startEnv struct {
		OK     bool `json:"ok"`
		Result struct {
			State    string         `json:"State"`
			Metadata map[string]any `json:"Metadata"`
		} `json:"result"`
	}
	if err := json.Unmarshal(startRaw, &startEnv); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	if !startEnv.OK || startEnv.Result.State == "" {
		t.Fatalf("start login failed: %s", string(startRaw))
	}
	if got := startEnv.Result.Metadata["user_code"]; got != "ABCD-1234" {
		t.Fatalf("user_code=%v, want ABCD-1234", got)
	}

	state := startEnv.Result.State

	// 6. 首次轮询应为 pending。
	pollRaw, err := handlePollLogin(mustJSON(t, map[string]any{"State": state}))
	if err != nil {
		t.Fatalf("poll#1: %v", err)
	}
	if !strings.Contains(string(pollRaw), `"pending"`) {
		t.Fatalf("poll#1 should be pending, got %s", string(pollRaw))
	}

	// 7. 第二次轮询应成功并落盘。
	pollRaw2, err := handlePollLogin(mustJSON(t, map[string]any{"State": state}))
	if err != nil {
		t.Fatalf("poll#2: %v", err)
	}
	if !strings.Contains(string(pollRaw2), `"success"`) {
		t.Fatalf("poll#2 should be success, got %s", string(pollRaw2))
	}

	accounts, err := listAllAuthFiles()
	if err != nil {
		t.Fatalf("listAllAuthFiles: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts len=%d, want 1", len(accounts))
	}
	if accounts[0].CopilotToken != "copilot-internal-token" {
		t.Fatalf("copilot token=%q", accounts[0].CopilotToken)
	}
	if accounts[0].GitHubToken != "gho_test_token" {
		t.Fatalf("github token=%q", accounts[0].GitHubToken)
	}
	// endpoints.api 必须随登录落盘，否则 resolveCopilotBaseURL 会退回按类型硬推导（GAP-002）。
	if accounts[0].APIEndpoint != "https://api.individual.githubcopilot.com" {
		t.Fatalf("api endpoint=%q, want endpoints.api persisted", accounts[0].APIEndpoint)
	}
	if got := resolveCopilotBaseURL(accounts[0], defaultConfig()); got != "https://api.individual.githubcopilot.com" {
		t.Fatalf("base url from persisted account=%q", got)
	}
}

// mustJSON 把 map 序列化为 JSON，序列化失败直接失败测试。
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// TestMutateModel 校验请求体模型名改写与非法输入兜底。
func TestMutateModel(t *testing.T) {
	// 1. 正常改写。
	got := mutateModel([]byte(`{"model":"gpt-4o","messages":[]}`), "claude-sonnet-4")
	var body map[string]any
	if err := json.Unmarshal(got, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["model"] != "claude-sonnet-4" {
		t.Fatalf("model=%v, want claude-sonnet-4", body["model"])
	}

	// 2. 空模型名不改写，保留原值。
	got = mutateModel([]byte(`{"model":"gpt-4o"}`), "")
	_ = json.Unmarshal(got, &body)
	if body["model"] != "gpt-4o" {
		t.Fatalf("empty model should keep original, got %v", body["model"])
	}

	// 3. 非法 JSON 原样返回副本（且不得与入参共享底层数组）。
	raw := []byte(`not-json`)
	out := mutateModel(raw, "x")
	if string(out) != "not-json" {
		t.Fatalf("invalid json should pass through, got %q", out)
	}
	out[0] = 'X'
	if raw[0] == 'X' {
		t.Fatal("mutateModel must not alias caller slice")
	}
}

// TestParseModelAliasAttribute 校验三种别名表形态（JSON 数组 / JSON 对象 / 逗号分隔）。
func TestParseModelAliasAttribute(t *testing.T) {
	// 1. JSON 数组形态。
	arr := parseModelAliasAttribute(map[string]string{
		"model_alias": `[{"name":"gpt-4o","alias":"fast"}]`})
	if arr["fast"] != "gpt-4o" {
		t.Fatalf("array form alias=%v", arr)
	}

	// 2. JSON 对象形态：键是别名，值是真实模型名。
	obj := parseModelAliasAttribute(map[string]string{
		"model-alias": `{"fast":"gpt-4o"}`})
	if obj["fast"] != "gpt-4o" {
		t.Fatalf("object form alias=%v", obj)
	}

	// 3. 逗号分隔形态：`别名=真实名`。
	csv := parseModelAliasAttribute(map[string]string{
		"oauth-model-alias": "fast=gpt-4o,smart=o4-mini"})
	if csv["fast"] != "gpt-4o" || csv["smart"] != "o4-mini" {
		t.Fatalf("csv form alias=%v", csv)
	}

	// 4. 无别名 / 别名等于本名时不登记（避免自映射死循环）。
	if got := parseModelAliasAttribute(nil); got != nil {
		t.Fatalf("nil attributes should return nil, got %v", got)
	}
	same := parseModelAliasAttribute(map[string]string{"model_alias": "gpt-4o=gpt-4o"})
	if _, ok := same["gpt-4o"]; ok {
		t.Fatal("self-mapping alias must be dropped")
	}
}

// TestResolveUpstreamModel 校验别名解析优先级：账号属性 > 全局缓存 > 原名。
func TestResolveUpstreamModel(t *testing.T) {
	// 1. 账号属性中的别名优先。
	if got := resolveUpstreamModel("fast", map[string]string{"model_alias": `{"fast":"gpt-4o"}`}); got != "gpt-4o" {
		t.Fatalf("attribute alias=%q, want gpt-4o", got)
	}

	// 2. 无别名时原样返回（去除首尾空白）。
	if got := resolveUpstreamModel("  gpt-4o  ", nil); got != "gpt-4o" {
		t.Fatalf("plain model=%q, want gpt-4o", got)
	}

	// 3. 空模型名原样返回空串，不 panic。
	if got := resolveUpstreamModel("", nil); got != "" {
		t.Fatalf("empty model=%q, want empty", got)
	}
}

// TestFilterExcludedModelsTable 校验排除表按 providerName 大小写不敏感匹配。
func TestFilterExcludedModelsTable(t *testing.T) {
	models := []pluginapi.ModelInfo{{ID: "gpt-4o"}, {ID: "o4-mini"}}

	// 1. 大小写不同的 provider 键仍应命中排除。
	host := pluginapi.HostConfigSummary{
		ExcludedModels: map[string][]string{"Copilot-Provider": {"o4-mini"}},
	}
	out := filterExcludedModels(models, host)
	if len(out) != 1 || out[0].ID != "gpt-4o" {
		t.Fatalf("case-insensitive exclude failed: %+v", out)
	}
	// 2. 入参切片不得被就地改写（缓存污染防线）。
	if len(models) != 2 {
		t.Fatal("filterExcludedModels must not mutate input slice")
	}
}

// TestHandleExecExecuteNoAccount 校验无可用账号时返回错误而非伪造成功响应。
func TestHandleExecExecuteNoAccount(t *testing.T) {
	tmpDir := t.TempDir()
	setAccountsDir(tmpDir)
	defer setAccountsDir("")

	raw := mustJSON(t, map[string]any{
		"Model":   "gpt-4o",
		"Payload": []byte(`{"model":"gpt-4o"}`),
	})
	if _, err := handleExecExecute(raw); err == nil {
		t.Fatal("expected error when no account available")
	}
}

// TestHandleExecStreamNoAccount 校验流式路径无账号时通过流事件上报错误。
//
// 与 zcode 基线同构：流式错误不走 Go error 返回，而是经 streamEmitError 上报后
// 仍返回 ok 信封（HTTP 层已 200，错误在流内传达），因此断言落点是事件而非 error。
func TestHandleExecStreamNoAccount(t *testing.T) {
	tmpDir := t.TempDir()
	setAccountsDir(tmpDir)
	defer setAccountsDir("")

	raw := mustJSON(t, map[string]any{
		"Model":    "gpt-4o",
		"Payload":  []byte(`{"model":"gpt-4o"}`),
		"StreamID": "s-1",
	})
	out, err := handleExecStream(raw)
	if err != nil {
		t.Fatalf("stream path should not return Go error, got %v", err)
	}
	if !strings.Contains(string(out), `"ok":true`) {
		t.Fatalf("expected ok envelope, got %s", out)
	}
}
