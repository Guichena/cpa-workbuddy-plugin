package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthFile_NamingAndSafety(t *testing.T) {
	key := "607c3abcdef.1234567890"
	fileName := authFileNameFor(key)
	if fileName == "" {
		t.Fatalf("expected non-empty fileName")
	}
	if !isZCodeAuthFileName(fileName) {
		t.Fatalf("expected valid auth file name, got %s", fileName)
	}

	authID := authIDFor(key)
	if authID == "" {
		t.Fatalf("expected non-empty authID")
	}

	if !isSafeZCodeAuthPath(fileName) {
		t.Fatalf("expected safe path for %s", fileName)
	}
	if isSafeZCodeAuthPath("../etc/passwd") {
		t.Fatalf("expected unsafe path for ../etc/passwd")
	}
	if isSafeZCodeAuthPath("other.json") {
		t.Fatalf("expected unsafe path for other.json")
	}
}

func TestAuthFile_CRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_test_accounts_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	apiKey := "test_api_key_12345.secret"
	authID := authIDFor(apiKey)
	fileName := authFileNameFor(apiKey)
	filePath := filepath.Join(tempDir, fileName)

	sa := &StoredAuth{
		Type:       "zcode",
		Provider:   zcodeProviderID,
		AuthID:     authID,
		APIKey:     apiKey,
		Label:      "Test Account",
		Disabled:   false,
		Models:     []string{"GLM-5.3"},
		TestFailed: false,
	}

	if err := writeAuthFileDirect(filePath, sa); err != nil {
		t.Fatalf("writeAuthFileDirect failed: %v", err)
	}

	readSA, err := readAuthFile(filePath)
	if err != nil {
		t.Fatalf("readAuthFile failed: %v", err)
	}
	if readSA.APIKey != apiKey || readSA.Label != "Test Account" {
		t.Fatalf("readSA content mismatch: %+v", readSA)
	}

	list, err := listAllAuthFiles()
	if err != nil {
		t.Fatalf("listAllAuthFiles failed: %v", err)
	}
	if len(list) != 1 || list[0].AuthID != authID {
		t.Fatalf("expected 1 account, got %d", len(list))
	}

	if err := deleteAuthFileDirect(authID); err != nil {
		t.Fatalf("deleteAuthFileDirect failed: %v", err)
	}

	listAfter, _ := listAllAuthFiles()
	if len(listAfter) != 0 {
		t.Fatalf("expected 0 accounts after delete, got %d", len(listAfter))
	}
}

func TestMaskAPIKey(t *testing.T) {
	if maskAPIKey("short") != "******" {
		t.Fatalf("expected ****** for short key")
	}
	masked := maskAPIKey("1234567890abcdef")
	if masked != "1234....cdef" {
		t.Fatalf("unexpected mask: %s", masked)
	}
}

func TestAuthFile_ZaiAccount(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_test_zai_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	user := &ZaiUserInfo{
		UserID: "10086",
		Name:   "张三",
		Email:  "zhangsan@z.ai",
		Avatar: "https://avatar.z.ai/1.png",
	}
	sa, err := saveZaiAccount("mock_token_12345678", "mock_access_token", "mock_biz_token", user, time.Now().Add(30*24*time.Hour), "测试Zai账号", providerFamilyZai)
	if err != nil {
		t.Fatalf("saveZaiAccount failed: %v", err)
	}
	if sa.UserID != "10086" || sa.Username != "张三" || sa.Email != "zhangsan@z.ai" {
		t.Fatalf("user fields mismatch: %+v", sa)
	}
	if sa.APIKey != "mock_token_12345678" || sa.Token != "mock_token_12345678" {
		t.Fatalf("token mismatch: %+v", sa)
	}

	list, err := listAllAuthFiles()
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 account, got %d (err=%v)", len(list), err)
	}
	if list[0].Username != "张三" {
		t.Fatalf("expected username 张三, got %s", list[0].Username)
	}
}

// TestBusinessToken_ResolveAndCache 验证业务令牌换取与缓存命中。
func TestBusinessToken_ResolveAndCache(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req zaiBusinessLoginRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Token != "zai-access-1" {
			http.Error(w, "bad token", http.StatusBadRequest)
			return
		}
		resp := zaiBusinessLoginResponse{Code: 200}
		resp.Data.AccessToken = "biz-token-abc"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	mockZaiBusinessLoginURL = ts.URL
	defer func() { mockZaiBusinessLoginURL = "" }()
	businessTokenMu.Lock()
	businessTokenCache = make(map[string]businessTokenCacheEntry)
	businessTokenMu.Unlock()

	got, err := resolveBusinessToken("zai-access-1", providerFamilyZai)
	if err != nil || got != "biz-token-abc" {
		t.Fatalf("expected biz-token-abc, got %q err=%v", got, err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 upstream call, got %d", calls)
	}

	// 第二次应命中缓存，不再请求上游
	if _, err2 := resolveBusinessToken("zai-access-1", providerFamilyZai); err2 != nil {
		t.Fatalf("cached resolve failed: %v", err2)
	}
	if calls != 1 {
		t.Fatalf("expected cached hit (still 1 call), got %d", calls)
	}
}

// TestBusinessToken_Invalidate 验证缓存失效后重新换取。
func TestBusinessToken_Invalidate(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		resp := zaiBusinessLoginResponse{Code: 200}
		resp.Data.AccessToken = "biz-2"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()
	mockZaiBusinessLoginURL = ts.URL
	defer func() { mockZaiBusinessLoginURL = "" }()
	businessTokenMu.Lock()
	businessTokenCache = make(map[string]businessTokenCacheEntry)
	businessTokenMu.Unlock()

	if _, err := resolveBusinessToken("zai-access-2", providerFamilyZai); err != nil {
		t.Fatalf("first resolve failed: %v", err)
	}
	invalidateBusinessToken("zai-access-2", providerFamilyZai)
	if _, err := resolveBusinessToken("zai-access-2", providerFamilyZai); err != nil {
		t.Fatalf("resolve after invalidate failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 upstream calls after invalidate, got %d", calls)
	}
}

// TestBusinessToken_UpstreamFailure 验证上游失败时返回错误而非静默降级。
func TestBusinessToken_UpstreamFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":401,"msg":"bad"}`, http.StatusUnauthorized)
	}))
	defer ts.Close()
	mockZaiBusinessLoginURL = ts.URL
	defer func() { mockZaiBusinessLoginURL = "" }()
	businessTokenMu.Lock()
	businessTokenCache = make(map[string]businessTokenCacheEntry)
	businessTokenMu.Unlock()

	if _, err := resolveBusinessToken("zai-access-3", providerFamilyZai); err == nil {
		t.Fatalf("expected error on upstream failure")
	}
}

// TestModelCredentialFor_Fallback 验证换取失败时回退到账号自身 Key。
func TestModelCredentialFor_Fallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer ts.Close()
	mockZaiBusinessLoginURL = ts.URL
	defer func() { mockZaiBusinessLoginURL = "" }()
	businessTokenMu.Lock()
	businessTokenCache = make(map[string]businessTokenCacheEntry)
	businessTokenMu.Unlock()

	sa := &StoredAuth{APIKey: "static-key-123", AccessToken: "zai-access-4"}
	if got := modelCredentialFor(sa); got != "static-key-123" {
		t.Fatalf("expected fallback to static key, got %q", got)
	}

	// 无 access_token 的静态账号直接使用自身 Key，不触发网络请求
	staticOnly := &StoredAuth{APIKey: "static-only"}
	if got := modelCredentialFor(staticOnly); got != "static-only" {
		t.Fatalf("expected static key, got %q", got)
	}
}

// TestResolveUpstreamTargets_StartPlanUsesJWT 验证 start-plan（含 JWT）优先走 zcode-plan + Bearer。
func TestResolveUpstreamTargets_StartPlanUsesJWT(t *testing.T) {
	cfg := defaultConfig()
	sa := &StoredAuth{APIKey: "jwt-token-x", Token: "jwt-token-x"}
	targets := resolveUpstreamTargets(sa, cfg)
	if len(targets) == 0 {
		t.Fatalf("expected at least one target")
	}
	first := targets[0]
	if first.URL != zaiCodingPlanBaseURL {
		t.Fatalf("expected zcode-plan endpoint first, got %s", first.URL)
	}
	if !first.Bearer || first.Credential != "jwt-token-x" {
		t.Fatalf("expected Bearer jwt credential, got %+v", first)
	}
}

// TestResolveUpstreamTargets_ConfiguredBaseWins 验证用户显式配置 base_url 时尊重配置。
func TestResolveUpstreamTargets_ConfiguredBaseWins(t *testing.T) {
	cfg := defaultConfig()
	cfg.BaseURL = "https://custom.example.com/anthropic"
	sa := &StoredAuth{APIKey: "static-key"}
	targets := resolveUpstreamTargets(sa, cfg)
	if len(targets) != 1 || targets[0].URL != "https://custom.example.com/anthropic" {
		t.Fatalf("expected configured base only, got %+v", targets)
	}
	if targets[0].Bearer {
		t.Fatalf("configured base should use x-api-key by default")
	}
}

// TestApplyTargetAuth_BearerVsApiKey 验证两种鉴权头的互斥写入。
func TestApplyTargetAuth_BearerVsApiKey(t *testing.T) {
	h := http.Header{}
	applyTargetAuth(h, upstreamTarget{Credential: "tok", Bearer: true})
	if h.Get("Authorization") != "Bearer tok" || h.Get("x-api-key") != "" {
		t.Fatalf("bearer auth mismatch: %+v", h)
	}
	applyTargetAuth(h, upstreamTarget{Credential: "key"})
	if h.Get("x-api-key") != "key" || h.Get("Authorization") != "" {
		t.Fatalf("api-key auth mismatch: %+v", h)
	}
}
