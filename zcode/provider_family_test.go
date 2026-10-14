package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/tidwall/gjson"
)

// TestNormalizeProviderFamily 验证 provider 取值归一化：仅 bigmodel 单独成族，
// 其余（含空值、大小写变体、非法值）一律回退默认 zai。
func TestNormalizeProviderFamily(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"zai", providerFamilyZai},
		{"bigmodel", providerFamilyBigmodel},
		{"BigModel", providerFamilyBigmodel},
		{"  BIGMODEL  ", providerFamilyBigmodel},
		{"", providerFamilyZai},
		{"unknown", providerFamilyZai},
	}
	for _, c := range cases {
		if got := normalizeProviderFamily(c.raw); got != c.want {
			t.Errorf("normalizeProviderFamily(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// TestIsValidOAuthProvider 验证只接受 zai / bigmodel 两个取值。
func TestIsValidOAuthProvider(t *testing.T) {
	for _, ok := range []string{"zai", "bigmodel", "ZAI", " BigModel "} {
		if !isValidOAuthProvider(ok) {
			t.Errorf("isValidOAuthProvider(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "zai-cn", "cn", "zhipu", "bigmodel-cn"} {
		if isValidOAuthProvider(bad) {
			t.Errorf("isValidOAuthProvider(%q) = true, want false", bad)
		}
	}
}

// TestStartAccountOAuth_RejectsUnknownProvider 验证非法 provider 在插件侧被拦截，
// 不会把无效请求打到上游，且错误信息明确。
func TestStartAccountOAuth_RejectsUnknownProvider(t *testing.T) {
	_, _, err := startAccountOAuth("", "", "zai-cn")
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
	if !strings.Contains(err.Error(), "不支持的 OAuth provider") {
		t.Fatalf("expected explicit provider error, got %v", err)
	}
}

// TestStartAccountOAuth_BigmodelProviderInjected 验证 bigmodel 取值会原样透传给
// CLI init 的 provider 字段，并把归属写入会话。
func TestStartAccountOAuth_BigmodelProviderInjected(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_provider_test_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	var gotProvider string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req zaiCLIInitRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotProvider = req.Provider
		resp := map[string]any{
			"code": 0,
			"data": map[string]any{
				"flow_id":       "flow-bigmodel-1",
				"poll_token":    "poll-token-1",
				"authorize_url": "https://bigmodel.cn/login?appId=zcode&state=state-bigmodel-1&redirect=https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel",
				"expires_at":    time.Now().Add(15 * time.Minute).Unix(),
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	mockOAuthCLIInitURL = ts.URL
	defer func() { mockOAuthCLIInitURL = "" }()

	_, state, err := startAccountOAuth("", "", providerFamilyBigmodel)
	if err != nil {
		t.Fatalf("startAccountOAuth failed: %v", err)
	}
	if gotProvider != providerFamilyBigmodel {
		t.Fatalf("expected provider=%q sent upstream, got %q", providerFamilyBigmodel, gotProvider)
	}

	rawSession, ok := oauthSessions.Load(state)
	if !ok {
		t.Fatalf("oauth session not stored for state %q", state)
	}
	session, ok := rawSession.(*oauthLoginSession)
	if !ok || session == nil {
		t.Fatalf("unexpected session type: %T", rawSession)
	}
	if session.Provider != providerFamilyBigmodel {
		t.Fatalf("expected session.Provider=%q, got %q", providerFamilyBigmodel, session.Provider)
	}
	oauthSessions.Delete(state)
}

// TestResolveOAuthProvider 验证入口层解析：空值按默认 bigmodel，合法值归一化，
// 非法值返回明确错误而**不静默回退**。
func TestResolveOAuthProvider(t *testing.T) {
	if got, err := resolveOAuthProvider(""); err != nil || got != providerFamilyBigmodel {
		t.Errorf("empty provider = (%q, %v), want (%q, nil)", got, err, providerFamilyBigmodel)
	}
	if got, err := resolveOAuthProvider("  "); err != nil || got != providerFamilyBigmodel {
		t.Errorf("blank provider = (%q, %v), want (%q, nil)", got, err, providerFamilyBigmodel)
	}
	if got, err := resolveOAuthProvider(" BigModel "); err != nil || got != providerFamilyBigmodel {
		t.Errorf("bigmodel provider = (%q, %v), want (%q, nil)", got, err, providerFamilyBigmodel)
	}
	if got, err := resolveOAuthProvider(" zai "); err != nil || got != providerFamilyZai {
		t.Errorf("zai provider = (%q, %v), want (%q, nil)", got, err, providerFamilyZai)
	}
	for _, bad := range []string{"zhipu", "zai-cn", "cn", "bigmodel-cn"} {
		got, err := resolveOAuthProvider(bad)
		if err == nil {
			t.Errorf("resolveOAuthProvider(%q) = (%q, nil), want error", bad, got)
			continue
		}
		if !strings.Contains(err.Error(), "不支持的 OAuth provider") {
			t.Errorf("resolveOAuthProvider(%q) error = %v, want explicit provider error", bad, err)
		}
	}
}

// TestHandleActivateStart_RejectsUnknownProvider 验证面板入口对非法 provider 返回 400，
// 不静默回退 zai、不发起上游请求（HTTP 层契约）。
func TestHandleActivateStart_RejectsUnknownProvider(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_activate_reject_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	upstreamHit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
	}))
	defer ts.Close()
	mockOAuthCLIInitURL = ts.URL
	defer func() { mockOAuthCLIInitURL = "" }()

	raw, err := handleActivateStart([]byte(`{"provider":"zhipu"}`))
	if err != nil {
		t.Fatalf("handleActivateStart returned error: %v", err)
	}
	resp := unwrapMgmtResp(t, raw)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider, got %d: %s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(string(resp.Body), "不支持的 OAuth provider") {
		t.Fatalf("expected explicit provider error, got %s", resp.Body)
	}
	if upstreamHit {
		t.Fatal("unknown provider must not reach upstream")
	}
}

// TestHandleActivateStart_AcceptsBigmodelProvider 验证面板入口合法 provider 正常发起授权，
// 且返回体回显生效的 provider（守住合法路径不因新增校验而回归）。
func TestHandleActivateStart_AcceptsBigmodelProvider(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_activate_ok_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"flow_id":       "flow-activate-1",
				"poll_token":    "poll-activate-1",
				"authorize_url": "https://bigmodel.cn/login?appId=zcode&state=state-activate-1&redirect=https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel",
				"expires_at":    time.Now().Add(15 * time.Minute).Unix(),
			},
		})
	}))
	defer ts.Close()
	mockOAuthCLIInitURL = ts.URL
	defer func() { mockOAuthCLIInitURL = "" }()

	raw, err := handleActivateStart([]byte(`{"provider":"bigmodel"}`))
	if err != nil {
		t.Fatalf("handleActivateStart returned error: %v", err)
	}
	resp := unwrapMgmtResp(t, raw)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for bigmodel provider, got %d: %s", resp.StatusCode, resp.Body)
	}
	if got := gjson.GetBytes(resp.Body, "provider").String(); got != providerFamilyBigmodel {
		t.Fatalf("expected provider=%q in response, got %q", providerFamilyBigmodel, got)
	}
	if state := gjson.GetBytes(resp.Body, "state").String(); state != "state-activate-1" {
		t.Fatalf("expected state-activate-1, got %q", state)
	}
	oauthSessions.Delete("state-activate-1")
}

// TestProviderFromMetadata 验证宿主登录请求的 Metadata 是 provider 的传递通道。
func TestProviderFromMetadata(t *testing.T) {
	if got := providerFromMetadata(nil); got != providerFamilyBigmodel {
		t.Errorf("nil metadata = %q, want %q", got, providerFamilyBigmodel)
	}
	if got := providerFromMetadata(map[string]any{"provider": "bigmodel"}); got != providerFamilyBigmodel {
		t.Errorf("bigmodel metadata = %q, want %q", got, providerFamilyBigmodel)
	}
	if got := providerFromMetadata(map[string]any{"provider": "zai"}); got != "zai" {
		t.Errorf("zai metadata = %q, want %q", got, "zai")
	}
	if got := providerFromMetadata(map[string]any{"provider": 123}); got != providerFamilyBigmodel {
		t.Errorf("non-string metadata = %q, want %q", got, providerFamilyBigmodel)
	}
	// 非法值必须原样透传（不在本层归一化），由 startAccountOAuth 校验后报错。
	if got := providerFromMetadata(map[string]any{"provider": "zhipu"}); got != "zhipu" {
		t.Errorf("unknown metadata = %q, want passthrough %q", got, "zhipu")
	}
}

// TestSaveZaiAccount_PersistsProviderFamily 验证账号落盘时写入站点归属。
func TestSaveZaiAccount_PersistsProviderFamily(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_family_persist_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	user := &ZaiUserInfo{UserID: "20001", Name: "bigmodel用户"}
	sa, err := saveZaiAccount("bm_token_abcdefgh", "bm_access", "", user, time.Now().Add(30*24*time.Hour), "测试bigmodel账号", providerFamilyBigmodel)
	if err != nil {
		t.Fatalf("saveZaiAccount failed: %v", err)
	}
	if sa.ProviderFamily != providerFamilyBigmodel {
		t.Fatalf("expected ProviderFamily=%q, got %q", providerFamilyBigmodel, sa.ProviderFamily)
	}

	// 回读磁盘确认字段真实落盘（而非仅内存）
	accounts, err := listAllAuthFiles()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d (err=%v)", len(accounts), err)
	}
	if accounts[0].ProviderFamily != providerFamilyBigmodel {
		t.Fatalf("expected persisted ProviderFamily=%q, got %q", providerFamilyBigmodel, accounts[0].ProviderFamily)
	}
}

// TestProviderFamilyOf_LegacyDefault 验证历史账号（无该字段）按 zai 处理，行为不变。
func TestProviderFamilyOf_LegacyDefault(t *testing.T) {
	if got := providerFamilyOf(nil); got != providerFamilyZai {
		t.Errorf("nil account family = %q, want %q", got, providerFamilyZai)
	}
	legacy := &StoredAuth{AuthID: "zcode-legacy"}
	if got := providerFamilyOf(legacy); got != providerFamilyZai {
		t.Errorf("legacy account family = %q, want %q", got, providerFamilyZai)
	}
}

// TestHandleListAccounts_ExposesProviderFamily 验证面板数据源透出站点归属字段：
// bigmodel 账号输出 `provider_family=bigmodel`，历史账号缺省为 `zai`。
func TestHandleListAccounts_ExposesProviderFamily(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_list_family_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	user := &ZaiUserInfo{UserID: "20002", Name: "bigmodel用户"}
	if _, err := saveZaiAccount("bm_list_abcdefgh", "bm_access", "", user, time.Now().Add(30*24*time.Hour), "列表归属账号", providerFamilyBigmodel); err != nil {
		t.Fatalf("saveZaiAccount failed: %v", err)
	}
	if _, err := saveZaiAccount("zai_list_abcdefgh", "zai_access", "", user, time.Now().Add(30*24*time.Hour), "国际站账号", providerFamilyZai); err != nil {
		t.Fatalf("saveZaiAccount failed: %v", err)
	}

	raw, err := handleListAccounts()
	if err != nil {
		t.Fatalf("handleListAccounts failed: %v", err)
	}
	resp := unwrapMgmtResp(t, raw)

	var payload struct {
		Accounts []accountSummary `json:"accounts"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		t.Fatalf("unmarshal accounts failed: %v", err)
	}
	if len(payload.Accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(payload.Accounts))
	}
	famByLabel := map[string]string{}
	for _, acc := range payload.Accounts {
		famByLabel[acc.Label] = acc.ProviderFamily
	}
	if got := famByLabel["列表归属账号"]; got != providerFamilyBigmodel {
		t.Errorf("bigmodel account provider_family = %q, want %q", got, providerFamilyBigmodel)
	}
	if got := famByLabel["国际站账号"]; got != providerFamilyZai {
		t.Errorf("zai account provider_family = %q, want %q", got, providerFamilyZai)
	}
}

// TestHandleMethod_StartLoginCarriesProvider 验证宿主入口经 Metadata 指定 bigmodel 时，
// 返回的授权地址指向中国站。
func TestHandleMethod_StartLoginCarriesProvider(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_host_login_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"code": 0,
			"data": map[string]any{
				"flow_id":       "flow-host-1",
				"poll_token":    "poll-host-1",
				"authorize_url": "https://bigmodel.cn/login?appId=zcode&state=state-host-1&redirect=https://zcode.z.ai/api/v1/oauth/cli/callback/bigmodel",
				"expires_at":    time.Now().Add(15 * time.Minute).Unix(),
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	mockOAuthCLIInitURL = ts.URL
	defer func() { mockOAuthCLIInitURL = "" }()

	reqBody, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"provider": "bigmodel"},
	})
	raw, err := handleMethod(pluginabi.MethodAuthLoginStart, reqBody)
	if err != nil {
		t.Fatalf("handleMethod AuthLoginStart failed: %v", err)
	}

	var envStart envelope
	if err := json.Unmarshal(raw, &envStart); err != nil || !envStart.OK {
		t.Fatalf("unmarshal AuthLoginStart failed: %v", err)
	}
	if !strings.Contains(string(envStart.Result), "bigmodel.cn/login") {
		t.Fatalf("expected China-site authorize URL, got %s", string(envStart.Result))
	}
}
