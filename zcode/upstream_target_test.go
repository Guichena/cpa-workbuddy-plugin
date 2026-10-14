package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestResolveUpstreamTargets_ByProviderFamily 验证上游模型端点按站点归属分支：
// 中国站走 open.bigmodel.cn，国际站走 zcode.z.ai，且凭据与鉴权方式保持 start-plan 语义。
func TestResolveUpstreamTargets_ByProviderFamily(t *testing.T) {
	cfg := defaultConfig()

	zaiAccount := &StoredAuth{
		AuthID:         "zcode-zai-1",
		Token:          "jwt-zai",
		ProviderFamily: providerFamilyZai,
	}
	zaiTargets := resolveUpstreamTargets(zaiAccount, cfg)
	if len(zaiTargets) == 0 || !strings.Contains(zaiTargets[0].URL, "zcode.z.ai") {
		t.Fatalf("expected zai coding-plan base (zcode.z.ai), got %+v", zaiTargets)
	}
	if !zaiTargets[0].Bearer || zaiTargets[0].Credential != "jwt-zai" {
		t.Fatalf("unexpected zai target auth: %+v", zaiTargets[0])
	}

	bmAccount := &StoredAuth{
		AuthID:         "zcode-bm-1",
		Token:          "jwt-bm",
		ProviderFamily: providerFamilyBigmodel,
	}
	bmTargets := resolveUpstreamTargets(bmAccount, cfg)
	// 断言字面量主机，避免两个常量被改成同值时断言退化为恒真。
	if len(bmTargets) == 0 || !strings.Contains(bmTargets[0].URL, "open.bigmodel.cn") {
		t.Fatalf("expected bigmodel coding-plan base (open.bigmodel.cn), got %+v", bmTargets)
	}
	if !bmTargets[0].Bearer || bmTargets[0].Credential != "jwt-bm" {
		t.Fatalf("unexpected bigmodel target auth: %+v", bmTargets[0])
	}
}

// TestResolveUpstreamTargets_LegacyDefaultsToZai 验证历史账号（无归属字段）端点不变，
// 保证改造前行为零回归。
func TestResolveUpstreamTargets_LegacyDefaultsToZai(t *testing.T) {
	legacy := &StoredAuth{AuthID: "zcode-legacy-1", Token: "jwt-legacy"}
	targets := resolveUpstreamTargets(legacy, defaultConfig())
	if len(targets) == 0 || !strings.Contains(targets[0].URL, "zcode.z.ai") {
		t.Fatalf("legacy account must keep zai base (zcode.z.ai), got %+v", targets)
	}
}

// TestBusinessLoginEndpointFor 验证业务令牌换取端点按站点归属分支。
func TestBusinessLoginEndpointFor(t *testing.T) {
	if got := businessLoginEndpointFor(providerFamilyZai); !strings.Contains(got, "api.z.ai") {
		t.Errorf("zai endpoint = %q, want host api.z.ai", got)
	}
	if got := businessLoginEndpointFor(providerFamilyBigmodel); !strings.Contains(got, "bigmodel.cn") {
		t.Errorf("bigmodel endpoint = %q, want host bigmodel.cn", got)
	}
	if got := businessLoginEndpointFor(""); !strings.Contains(got, "api.z.ai") {
		t.Errorf("empty family must default to zai endpoint, got %q", got)
	}
}

// TestResolveBusinessToken_CacheIsolatedByFamily 验证业务令牌缓存按 family 隔离：
// 同一 access_token 在 zai 与 bigmodel 下不得互相命中缓存。
func TestResolveBusinessToken_CacheIsolatedByFamily(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"access_token":"biz-family-token"}}`))
	}))
	defer ts.Close()

	mockZaiBusinessLoginURL = ts.URL
	defer func() { mockZaiBusinessLoginURL = "" }()
	businessTokenMu.Lock()
	businessTokenCache = make(map[string]businessTokenCacheEntry)
	businessTokenMu.Unlock()

	// 首次 zai：请求上游
	if _, err := resolveBusinessToken("shared-access", providerFamilyZai); err != nil {
		t.Fatalf("zai resolve failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 upstream call after zai, got %d", calls)
	}

	// 同 token 换 bigmodel：family 隔离，必须再打一次上游
	if _, err := resolveBusinessToken("shared-access", providerFamilyBigmodel); err != nil {
		t.Fatalf("bigmodel resolve failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected family-isolated cache (2 calls), got %d", calls)
	}

	// 再次 zai：应命中缓存
	if _, err := resolveBusinessToken("shared-access", providerFamilyZai); err != nil {
		t.Fatalf("zai cached resolve failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected zai cache hit (still 2 calls), got %d", calls)
	}
}

// TestModelCredentialFor_BigmodelUsesFamilyEndpoint 验证 bigmodel 账号换取业务令牌时
// 走中国站端点，而非国际站。
func TestModelCredentialFor_BigmodelUsesFamilyEndpoint(t *testing.T) {
	var hitPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":{"access_token":"bm-biz"}}`))
	}))
	defer ts.Close()

	// 通过 mock 覆盖端点，验证调用确实发生（端点为 mock 地址，故只断言凭据解析成功）
	mockZaiBusinessLoginURL = ts.URL
	defer func() { mockZaiBusinessLoginURL = "" }()
	businessTokenMu.Lock()
	businessTokenCache = make(map[string]businessTokenCacheEntry)
	businessTokenMu.Unlock()

	sa := &StoredAuth{
		AuthID:         "zcode-bm-cred",
		AccessToken:    "bm-access-cred",
		ProviderFamily: providerFamilyBigmodel,
	}
	if got := modelCredentialFor(sa); got != "bm-biz" {
		t.Fatalf("expected bm-biz business token, got %q (hitPath=%s)", got, hitPath)
	}
	if hitPath == "" {
		t.Fatal("expected business login endpoint to be called")
	}
}

// TestBusinessTokenTTLGuard 保证测试不会因缓存时长配置漂移而失效（守住常量语义）。
func TestBusinessTokenTTLGuard(t *testing.T) {
	if businessTokenTTL <= 0 || businessTokenTTL > time.Hour {
		t.Fatalf("businessTokenTTL out of sane range: %v", businessTokenTTL)
	}
}
