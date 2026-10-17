// multiaccount_test.go 覆盖 CYCLE-02 多账号调度与账号身份一致性。
//
// 关键回归点：账号身份必须统一为宿主 auth.ID（qwenwork-<uid>.json）。
// 历史上执行器曾把 accessToken 当账号身份，导致冷却、会话绑定、日冷却
// 全部写到调度器永远查不到的键上，故障账号永不退出轮转。
package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// TestAccountIdentity_MatchesHostAuthID 断言账号身份推导与宿主 auth.ID 同源。
func TestAccountIdentity_MatchesHostAuthID(t *testing.T) {
	cases := []struct {
		name string
		sa   *storedAuth
		want string
	}{
		{"normal uid", &storedAuth{Account: storedAccount{UID: "u123"}}, "qwenwork-u123.json"},
		{"uid with unsafe chars", &storedAuth{Account: storedAccount{UID: "a/b:c"}}, "qwenwork-a_b_c.json"},
		{"empty uid", &storedAuth{}, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accountIdentity(tc.sa); got != tc.want {
				t.Fatalf("accountIdentity() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAccountIdentity_NeverReturnsAccessToken 断言凭据永远不会被当成账号身份。
func TestAccountIdentity_NeverReturnsAccessToken(t *testing.T) {
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "eyJhbGciOiJIUzI1NiJ9.long-jwt-value", RefreshToken: "drt-x"},
		Account: storedAccount{UID: "u1"},
	}
	got := accountIdentity(sa)
	if strings.Contains(got, "eyJ") || strings.Contains(got, sa.Auth.AccessToken) {
		t.Fatalf("accountIdentity must not leak the access token as identity, got %q", got)
	}
}

// TestCooldown_KeyedByIdentityIsVisibleToScheduler 断言按身份记录的冷却
// 会被调度器侧的冷却判定读到——这是故障账号退出轮转的前提。
func TestCooldown_KeyedByIdentityIsVisibleToScheduler(t *testing.T) {
	prevEnabled := failoverActive()
	setFailoverEnabled(true)
	t.Cleanup(func() { setFailoverEnabled(prevEnabled) })
	clearFailoverState(t)

	sa := &storedAuth{Account: storedAccount{UID: "u-cool"}}
	id := accountIdentity(sa)
	if id == "" {
		t.Fatal("identity must not be empty")
	}
	if isAccountCoolingDown(id) {
		t.Fatal("precondition: account must start healthy")
	}
	// 1. 按身份记录一次账号级失败。
	if !recordAccountFailure(id, http.StatusUnauthorized, "upstream 401: token expired") {
		t.Fatal("401 must count as account failure")
	}
	// 2. 调度器用同一个 key 查询，必须看到冷却。
	if !isAccountCoolingDown(id) {
		t.Fatal("cooldown recorded under auth.ID must be visible to the scheduler")
	}
}

// TestCooldown_AccessTokenKeyIsInvisible 记录反证：用 accessToken 当 key
// 时调度器查不到——说明身份失配会让冷却彻底失效。
func TestCooldown_AccessTokenKeyIsInvisible(t *testing.T) {
	prevEnabled := failoverActive()
	setFailoverEnabled(true)
	t.Cleanup(func() { setFailoverEnabled(prevEnabled) })
	clearFailoverState(t)

	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "eyJhbGciOiJIUzI1NiJ9.token"},
		Account: storedAccount{UID: "u-x"},
	}
	tokenKey := sa.Auth.AccessToken
	idKey := accountIdentity(sa)
	if tokenKey == idKey {
		t.Fatal("precondition: token and identity must differ")
	}
	recordAccountFailure(tokenKey, http.StatusUnauthorized, "upstream 401")
	if isAccountCoolingDown(idKey) {
		t.Fatal("token-keyed cooldown must NOT be visible under auth.ID (this is the bug being guarded)")
	}
}

// TestSessionBinding_EvictedByIdentity 断言会话绑定按宿主 auth.ID 驱逐。
func TestSessionBinding_EvictedByIdentity(t *testing.T) {
	clearSessionBindings()
	t.Cleanup(clearSessionBindings)

	sa := &storedAuth{Account: storedAccount{UID: "u-sess"}}
	id := accountIdentity(sa)
	sessionAuthBindings["conv-1"] = sessionAuthBinding{AuthID: id, ExpiresAt: time.Now().Add(time.Hour)}
	sessionAuthBindings["conv-2"] = sessionAuthBinding{AuthID: "qwenwork-other.json", ExpiresAt: time.Now().Add(time.Hour)}

	if evicted := evictSessionBindingsForAuth(id); evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
	if _, ok := sessionAuthBindings["conv-1"]; ok {
		t.Fatal("binding for the failed account must be evicted")
	}
	if _, ok := sessionAuthBindings["conv-2"]; !ok {
		t.Fatal("binding for another account must survive")
	}
}

// TestSchedulerMode_OffIsHonored 断言 scheduler_mode: off 被正确解析并让调度器 defer。
func TestSchedulerMode_OffIsHonored(t *testing.T) {
	cfg := map[string]any{"config_yaml": []byte("scheduler_mode: off\n")}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	prev := loadedSchedulerMode()
	t.Cleanup(func() { setSchedulerMode(prev) })

	configure(raw)
	if got := loadedSchedulerMode(); got != schedulerModeOff {
		t.Fatalf("scheduler_mode = %q, want %q", got, schedulerModeOff)
	}

	pickRaw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "qwenwork-a.json", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("pick err: %v", err)
	}
	if resp := parsePickResponse(t, pickRaw); resp.Handled {
		t.Fatal("off mode must defer to the host scheduler")
	}
}

// TestSchedulerMode_SessionIsDefaultAndExplicit 断言 session 仍是默认值且可显式设置。
func TestSchedulerMode_SessionIsDefaultAndExplicit(t *testing.T) {
	prev := loadedSchedulerMode()
	t.Cleanup(func() { setSchedulerMode(prev) })

	raw, _ := json.Marshal(map[string]any{"config_yaml": []byte("scheduler_mode: session\n")})
	configure(raw)
	if got := loadedSchedulerMode(); got != schedulerModeSession {
		t.Fatalf("explicit session = %q", got)
	}

	// 未知值回落默认 session，而不是 off。
	raw, _ = json.Marshal(map[string]any{"config_yaml": []byte("scheduler_mode: bogus\n")})
	configure(raw)
	if got := loadedSchedulerMode(); got != schedulerModeSession {
		t.Fatalf("unknown value must fall back to session, got %q", got)
	}
}

// TestFoldCredentialIntoDoc_PreservesOperationalFields 断言令牌刷新不会抹掉
// 账号运营状态字段（手动禁用、日冷却、计数等）。
func TestFoldCredentialIntoDoc_PreservesOperationalFields(t *testing.T) {
	base := []byte(`{
		"type": "qwenwork-provider",
		"provider": "qwenwork-provider",
		"disabled": true,
		"note": "手动停用",
		"manual_disable": true,
		"exhausted_disable": false,
		"success_count": 7,
		"failed_count": 2,
		"daily_cooling_until": "2026-10-19T00:00:00Z",
		"auth": {"accessToken": "old", "refreshToken": "old-r"},
		"account": {"uid": "u1"}
	}`)
	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "new", RefreshToken: "new-r", ExpiresAt: 123},
		Account: storedAccount{UID: "u1"},
	}
	raw, err := foldCredentialIntoDoc(base, sa)
	if err != nil {
		t.Fatalf("fold: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// 1. 凭据必须被替换。
	auth, _ := doc["auth"].(map[string]any)
	if auth["accessToken"] != "new" || auth["refreshToken"] != "new-r" {
		t.Fatalf("credential not refreshed: %v", auth)
	}
	// 2. 运营状态必须原样保留。
	if doc["disabled"] != true {
		t.Fatal("disabled must survive a token refresh")
	}
	if doc["manual_disable"] != true {
		t.Fatal("manual_disable must survive a token refresh")
	}
	if doc["note"] != "手动停用" {
		t.Fatalf("note = %v", doc["note"])
	}
	if doc["success_count"] != float64(7) || doc["failed_count"] != float64(2) {
		t.Fatalf("counters lost: %v / %v", doc["success_count"], doc["failed_count"])
	}
	if doc["daily_cooling_until"] != "2026-10-19T00:00:00Z" {
		t.Fatalf("daily_cooling_until = %v", doc["daily_cooling_until"])
	}
	if doc["type"] != "qwenwork-provider" || doc["provider"] != "qwenwork-provider" {
		t.Fatal("type/provider must survive")
	}
}

// TestFoldCredentialIntoDoc_RejectsMalformedBase 断言基线损坏时返回错误而非静默重建。
func TestFoldCredentialIntoDoc_RejectsMalformedBase(t *testing.T) {
	sa := &storedAuth{Account: storedAccount{UID: "u1"}}
	if _, err := foldCredentialIntoDoc([]byte("{not json"), sa); err == nil {
		t.Fatal("malformed base must return an error")
	}
	if _, err := foldCredentialIntoDoc([]byte(`{}`), nil); err == nil {
		t.Fatal("nil storedAuth must return an error")
	}
}

// clearFailoverState 清空冷却表，测试用。
func clearFailoverState(t *testing.T) {
	t.Helper()
	failoverMu.Lock()
	failoverStates = make(map[string]*authFailoverState)
	failoverMu.Unlock()
}

// TestSchedulerPick_CoolingAccountExcluded 断言冷却中的账号被调度硬排除，
// 流量自动落到健康账号；当唯一候选在冷却时 defer 给宿主。
// 这条覆盖 CYCLE-02 的完成条件「1 个账号失败，请求自动切到另 1 个」。
func TestSchedulerPick_CoolingAccountExcluded(t *testing.T) {
	prevEnabled := failoverActive()
	setFailoverEnabled(true)
	t.Cleanup(func() {
		setFailoverEnabled(prevEnabled)
		clearFailoverState(t)
	})
	clearFailoverState(t)
	resetActiveAuth(t)

	// 1. 让 A 进入冷却（401 属账号级故障）。
	if !recordAccountFailure("qwenwork-a.json", http.StatusUnauthorized, "upstream 401") {
		t.Fatal("401 must count as account failure")
	}

	// 2. 两个候选：调度必须避开 A 选 B。
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "qwenwork-a.json", Provider: providerName},
			{ID: "qwenwork-b.json", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "qwenwork-b.json" {
		t.Fatalf("cooling account must be excluded, want qwenwork-b.json, got %+v", resp)
	}

	// 3. 只剩冷却中的 A：必须 defer，而不是把流量塞回故障账号。
	raw, err = handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "qwenwork-a.json", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp := parsePickResponse(t, raw); resp.Handled {
		t.Fatalf("all-cooling must defer to host scheduler, got %+v", resp)
	}
}
