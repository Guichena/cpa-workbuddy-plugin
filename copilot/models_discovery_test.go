package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// upstreamItemForTest 构造一条上游 /models 条目，供过滤判据用例复用。
// [参数] id: 模型 ID；vendor: vendor 字段；capType: capabilities.type；family: capabilities.family；policyState: policy.state。
// [返回] 上游模型条目。
// 最近修改时间：2026-10-15 23:50:00；改动原因：新增，过滤判据的测试构造入口。
func upstreamItemForTest(id, vendor, capType, family, policyState string) upstreamModelItem {
	var item upstreamModelItem
	item.ID = id
	item.Vendor = vendor
	item.Capabilities.Type = capType
	item.Capabilities.Family = family
	item.Policy.State = policyState
	return item
}

// TestIsChatCapableModelKeepsRealChatModel 校验真实对话模型被保留。
// 判据：capabilities.type=chat 且 policy.state 非 disabled（含缺省）的条目必须通过。
func TestIsChatCapableModelKeepsRealChatModel(t *testing.T) {
	cases := []struct {
		name string
		item upstreamModelItem
	}{
		{"chat with enabled policy", upstreamItemForTest("gpt-5.4", "Azure OpenAI", "chat", "gpt-5.4", "enabled")},
		{"chat without policy field", upstreamItemForTest("gpt-4o", "Azure OpenAI", "chat", "gpt-4o", "")},
		{"chat with numeric family", upstreamItemForTest("gpt-4.1", "Azure OpenAI", "chat", "gpt-4.1", "enabled")},
	}
	for _, c := range cases {
		if !isChatCapableModel(c.item) {
			t.Errorf("%s: expected kept, got filtered (id=%s)", c.name, c.item.ID)
		}
	}
}

// TestIsChatCapableModelFiltersUnusable 校验不可用条目被剔除。
// 判据：embeddings / completion 类、policy.state=disabled、内部子代理工具项都必须剔除。
func TestIsChatCapableModelFiltersUnusable(t *testing.T) {
	cases := []struct {
		name string
		item upstreamModelItem
	}{
		{"embeddings type", upstreamItemForTest("text-embedding-3-small", "OpenAI", "embeddings", "", "")},
		{"completion type", upstreamItemForTest("gpt-3.5-turbo-instruct", "OpenAI", "completion", "", "")},
		{"disabled policy", upstreamItemForTest("claude-sonnet-5", "Anthropic", "chat", "claude-sonnet-5", "disabled")},
		{"search agent tool", upstreamItemForTest("copilot-search-a", "Fireworks", "chat", "search-agent", "")},
		{"exec agent tool", upstreamItemForTest("exec-agent-a", "Fireworks", "chat", "exec-agent", "")},
		{"trajectory compaction tool", upstreamItemForTest("trajectory-compaction", "Fireworks", "chat", "trajectory-compaction", "")},
	}
	for _, c := range cases {
		if isChatCapableModel(c.item) {
			t.Errorf("%s: expected filtered, got kept (id=%s)", c.name, c.item.ID)
		}
	}
}

// TestIsInternalToolModelRequiresFireworksVendor 校验内部工具项判定必须同时满足 vendor 条件。
// 判据：非 Fireworks 的同名 family 不得被误剔（避免误伤真实模型）。
func TestIsInternalToolModelRequiresFireworksVendor(t *testing.T) {
	if isInternalToolModel("search-agent", "Azure OpenAI") {
		t.Error("non-Fireworks vendor with search-agent family must not be treated as internal tool")
	}
	if !isInternalToolModel("search-agent", "Fireworks") {
		t.Error("Fireworks vendor with search-agent family must be treated as internal tool")
	}
	if !isInternalToolModel("  exec-agent  ", " fireworks ") {
		t.Error("whitespace and case must be normalized")
	}
	if isInternalToolModel("gpt-4o", "Fireworks") {
		t.Error("real model family under Fireworks vendor must not be filtered")
	}
}

// TestDetectStreamErrorInterceptsBusinessError 校验 HTTP 200 下的业务错误帧被拦截。
// 判据：event: error 行与 data 载荷内的 error 字段都必须被识别为失败，正常输出帧不得误报。
func TestDetectStreamErrorInterceptsBusinessError(t *testing.T) {
	errCases := []struct {
		name  string
		chunk string
	}{
		{"event error line", "event: error\ndata: {\"code\":\"insufficient_quota\"}\n\n"},
		{"json error field", "data: {\"error\":{\"message\":\"quota exceeded\"}}\n\n"},
	}
	for _, c := range errCases {
		if got := detectStreamError(c.chunk); got == "" {
			t.Errorf("%s: expected error detected, got empty", c.name)
		}
	}

	okCases := []struct {
		name  string
		chunk string
	}{
		{"normal content frame", "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\n"},
		{"done frame", "data: [DONE]\n\n"},
		{"empty chunk", ""},
	}
	for _, c := range okCases {
		if got := detectStreamError(c.chunk); got != "" {
			t.Errorf("%s: expected no error, got %q", c.name, got)
		}
	}
}

// TestTruncateForLogBoundsOutput 校验错误文本截断边界。
// 判据：超长文本必须被截断并追加省略标记，短文本原样返回。
func TestTruncateForLogBoundsOutput(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "abcdefghij"
	}
	if got := truncateForLog(long, 50); len(got) != 53 {
		t.Errorf("expected 50 chars + ellipsis (53), got %d", len(got))
	}
	if got := truncateForLog("short", 50); got != "short" {
		t.Errorf("expected unchanged short text, got %q", got)
	}
}

// TestReadFirstSSEFrameReadsCompleteFrame 校验按帧边界读取完整首帧。
// 判据：首帧 JSON 超过 512 字节时必须完整读出，否则截断后的错误帧无法被解析识别。
func TestReadFirstSSEFrameReadsCompleteFrame(t *testing.T) {
	// 构造一个 >512 字节的错误帧，错误信息位于帧尾。
	padding := strings.Repeat("x", 700)
	stream := "data: {\"padding\":\"" + padding + "\",\"error\":{\"message\":\"quota exceeded\"}}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"later\"}}]}\n\n"

	frame := readFirstSSEFrame(strings.NewReader(stream))
	if !strings.Contains(frame, "quota exceeded") {
		t.Errorf("first frame must contain the trailing error payload, got len=%d", len(frame))
	}
	if strings.Contains(frame, "later") {
		t.Error("first frame must stop at the frame boundary, not include the next frame")
	}
	if got := detectStreamError(frame); got == "" {
		t.Error("oversized error frame must still be detected as failure")
	}
}

// TestReadFirstSSEFrameHandlesEmptyAndUnterminated 校验边界输入。
// 判据：空流返回空串；无空行结尾的流在 EOF 处收尾且不 panic。
func TestReadFirstSSEFrameHandlesEmptyAndUnterminated(t *testing.T) {
	if got := readFirstSSEFrame(strings.NewReader("")); got != "" {
		t.Errorf("empty stream must yield empty string, got %q", got)
	}
	got := readFirstSSEFrame(strings.NewReader("data: {\"choices\":[]}\n"))
	if !strings.Contains(got, "choices") {
		t.Errorf("unterminated frame must be returned at EOF, got %q", got)
	}
}

// TestPingUpstreamOnceDetectsOversizedErrorFrame 覆盖真实调用路径：超长错误帧必须判失败。
//
// 这是截断缺陷的端到端回归判据——纯函数测试无法发现「调用点改用固定字节读取」的退化，
// 故必须经 pingUpstreamOnce 走完整 HTTP + 帧读取 + 错误判定链路。
func TestPingUpstreamOnceDetectsOversizedErrorFrame(t *testing.T) {
	padding := strings.Repeat("x", 700)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"padding\":\"" + padding + "\",\"error\":{\"message\":\"quota exceeded\"}}\n\n"))
	}))
	defer srv.Close()

	prevCfg := currentConfig()
	cfg := prevCfg
	cfg.BaseURL = srv.URL
	activeConfig.Store(cfg)
	defer activeConfig.Store(prevCfg)

	sa := &StoredAuth{
		AuthID:       "copilot-probe",
		CopilotToken: "tid=probe;exp=9999999999",
		ExpiresAt:    "2099-01-01T00:00:00Z",
	}
	ok, status, errMsg, _ := pingUpstreamOnce(sa, "gpt-4o")
	if ok {
		t.Error("oversized error frame must not be reported as success")
	}
	if !strings.Contains(errMsg, "quota exceeded") {
		t.Errorf("error message must carry the upstream detail, got %q (status=%d)", errMsg, status)
	}
}

// TestPingUpstreamOnceAcceptsNormalStream 校验正常流仍被判成功（防误杀）。
func TestPingUpstreamOnceAcceptsNormalStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\n"))
	}))
	defer srv.Close()

	prevCfg := currentConfig()
	cfg := prevCfg
	cfg.BaseURL = srv.URL
	activeConfig.Store(cfg)
	defer activeConfig.Store(prevCfg)

	sa := &StoredAuth{
		AuthID:       "copilot-probe",
		CopilotToken: "tid=probe;exp=9999999999",
		ExpiresAt:    "2099-01-01T00:00:00Z",
	}
	ok, status, errMsg, latency := pingUpstreamOnce(sa, "gpt-4o")
	if !ok {
		t.Errorf("normal stream must be reported as success, got err=%q status=%d", errMsg, status)
	}
	if latency < 0 {
		t.Error("latency must be non-negative")
	}
}

// TestPingUpstreamOnceRejectsHTTPError 校验非 2xx 响应被判失败。
func TestPingUpstreamOnceRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "bad credentials"})
	}))
	defer srv.Close()

	prevCfg := currentConfig()
	cfg := prevCfg
	cfg.BaseURL = srv.URL
	activeConfig.Store(cfg)
	defer activeConfig.Store(prevCfg)

	sa := &StoredAuth{
		AuthID:       "copilot-probe",
		CopilotToken: "tid=probe;exp=9999999999",
		ExpiresAt:    "2099-01-01T00:00:00Z",
	}
	ok, status, _, _ := pingUpstreamOnce(sa, "gpt-4o")
	if ok {
		t.Error("HTTP 401 must not be reported as success")
	}
	if status != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", status)
	}
}
