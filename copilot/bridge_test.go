package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestLoginBridgeRendersUserCode 校验桥接页把设备码与授权地址渲染进 HTML。
func TestLoginBridgeRendersUserCode(t *testing.T) {
	state := "bridge-state-1"
	oauthSessions.Store(state, &oauthLoginSession{
		CreatedAt:       time.Now(),
		DeviceCode:      "device-abc",
		UserCode:        "ABCD-1234",
		VerificationURI: "https://github.com/login/device",
		Interval:        time.Second,
	})
	defer oauthSessions.Delete(state)

	resp := handleLoginBridge(state)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if ct := resp.Headers.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type=%q, want text/html", ct)
	}
	body := string(resp.Body)
	if !strings.Contains(body, "ABCD-1234") {
		t.Fatalf("bridge page missing user code: %s", body)
	}
	if !strings.Contains(body, "https://github.com/login/device") {
		t.Fatalf("bridge page missing verification uri: %s", body)
	}
	// 逐段 WriteString 必须产出完整文档：原生字符串内的反引号会截断渲染。
	if !strings.HasSuffix(body, "</html>") {
		t.Fatalf("bridge page truncated: %s", body)
	}
}

// TestLoginBridgeGuards 校验桥接页在缺少 / 未知 / 超时 state 时渲染错误页而非空白。
func TestLoginBridgeGuards(t *testing.T) {
	// 1. 缺少 state。
	empty := handleLoginBridge("  ")
	if !strings.Contains(string(empty.Body), "缺少 state") {
		t.Fatalf("empty state page=%s", empty.Body)
	}

	// 2. 未知 state。
	unknown := handleLoginBridge("no-such-state")
	if !strings.Contains(string(unknown.Body), "授权会话不存在") {
		t.Fatalf("unknown state page=%s", unknown.Body)
	}

	// 3. 超时会话必须被清理，避免长期驻留。
	expired := "bridge-expired"
	oauthSessions.Store(expired, &oauthLoginSession{
		CreatedAt:       time.Now().Add(-2 * githubDeviceCodeTTL),
		UserCode:        "OLD-0000",
		VerificationURI: "https://github.com/login/device",
	})
	timedOut := handleLoginBridge(expired)
	if !strings.Contains(string(timedOut.Body), "登录已超时") {
		t.Fatalf("expired state page=%s", timedOut.Body)
	}
	if _, ok := oauthSessions.Load(expired); ok {
		t.Fatal("expired session should be evicted")
	}
}

// TestHandleManagementLoginBridgeRoute 校验 resource 路由把 /login-bridge 分发到桥接页。
//
// 宿主 OAuth 弹窗打开的就是该 URL，路由若未接通则弹窗只能拿到 404，
// 用户依旧看不到设备码。
func TestHandleManagementLoginBridgeRoute(t *testing.T) {
	state := "bridge-route-1"
	oauthSessions.Store(state, &oauthLoginSession{
		CreatedAt:       time.Now(),
		UserCode:        "ROUTE-9999",
		VerificationURI: "https://github.com/login/device",
	})
	defer oauthSessions.Delete(state)

	raw := mustJSON(t, map[string]any{
		"Method": http.MethodGet,
		"Path":   "/v0/resource/plugins/" + providerName + "/login-bridge",
		"Query":  url.Values{"state": []string{state}},
	})
	out, err := handleManagement(raw)
	if err != nil {
		t.Fatalf("handleManagement: %v", err)
	}

	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			StatusCode int
			Headers    http.Header
			Body       []byte
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope not ok: %s", out)
	}
	if env.Result.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", env.Result.StatusCode)
	}
	if !strings.Contains(string(env.Result.Body), "ROUTE-9999") {
		t.Fatalf("route body missing user code: %s", env.Result.Body)
	}
}
