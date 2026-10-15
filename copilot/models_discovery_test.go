package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
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

// probeServerForTest 启动一个桩上游：按模型名返回「可调用 / 不可调用」两类 400。
// [参数] t: 测试上下文。
// [返回] 桩服务端；调用方负责 Close。
// 最近修改时间：2026-10-16 01:45:00；改动原因：新增，探测链路的桩服务端。
func probeServerForTest(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		model := gjson.GetBytes(body, "model").String()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		if strings.HasPrefix(model, "gpt-4o") || strings.HasPrefix(model, "gpt-4.1") {
			_, _ = w.Write([]byte(`{"error":{"message":"messages must be non-empty","code":""}}`))
			return
		}
		_, _ = w.Write([]byte(`{"error":{"message":"The requested model is not supported.","code":"model_not_supported"}}`))
	}))
}

// TestProbeModelCallableDistinguishesEntitlement 校验探测判据能区分账号 entitlement。
//
// 判据：返回 messages must be non-empty（已过模型鉴权）视为可调用；
// 返回 model_not_supported / not accessible 视为不可调用；5xx 等不确定结果保守保留。
func TestProbeModelCallableDistinguishesEntitlement(t *testing.T) {
	srv := probeServerForTest(t)
	defer srv.Close()

	cfg := currentConfig()
	cfg.BaseURL = srv.URL

	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-4.1", true},
		{"gpt-6-luna", false},
		{"claude-haiku-4.5", false},
	}
	for _, c := range cases {
		got := probeModelCallable(context.Background(), srv.URL, "tid=probe", c.model, cfg)
		if got != c.want {
			t.Errorf("model %s: expected callable=%v, got %v", c.model, c.want, got)
		}
	}
}

// TestProbeModelCallableKeepsOnUncertainty 校验不确定结果保守保留，避免上游抖动误删模型。
func TestProbeModelCallableKeepsOnUncertainty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"internal server error"}}`))
	}))
	defer srv.Close()

	cfg := currentConfig()
	cfg.BaseURL = srv.URL
	if !probeModelCallable(context.Background(), srv.URL, "tid=probe", "gpt-4o", cfg) {
		t.Error("5xx must be treated as uncertain and keep the model")
	}
}

// TestProbeUsableModelsFiltersUnentitled 校验批量探测只保留可调用模型。
//
// 端到端判据：候选里混入账号无权模型时，出口必须只剩可调用者；
// 这是「面板暴露 25 个模型、用户逐个测试全 400」缺陷的回归判据。
func TestProbeUsableModelsFiltersUnentitled(t *testing.T) {
	srv := probeServerForTest(t)
	defer srv.Close()

	cfg := currentConfig()
	cfg.BaseURL = srv.URL

	sa := &StoredAuth{
		AuthID:       "copilot-probe",
		CopilotToken: "tid=probe;exp=9999999999",
		ExpiresAt:    "2099-01-01T00:00:00Z",
	}
	candidates := []string{"gpt-4o", "gpt-6-luna", "gpt-4.1", "claude-haiku-4.5", "gpt-4o-mini"}
	got := probeUsableModels(context.Background(), sa, cfg, candidates)
	want := map[string]bool{"gpt-4o": true, "gpt-4.1": true, "gpt-4o-mini": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d usable models, got %d: %v", len(want), len(got), got)
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("unentitled model %s must be filtered out", m)
		}
	}
}

// TestStringSliceEqual 校验切片比较辅助函数。
func TestStringSliceEqual(t *testing.T) {
	if !stringSliceEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Error("identical slices must be equal")
	}
	if stringSliceEqual([]string{"a"}, []string{"a", "b"}) {
		t.Error("different length must not be equal")
	}
	if stringSliceEqual([]string{"a", "b"}, []string{"a", "c"}) {
		t.Error("different content must not be equal")
	}
	if !stringSliceEqual(nil, nil) {
		t.Error("two nil slices must be equal")
	}
}

// TestFetchUpstreamModelsFiltersByEntitlement 覆盖 fetchUpstreamModels 完整调用链。
//
// 这是「探测结果未被消费」缺陷的回归判据——纯函数测试无法发现调用点被改坏
// （如过滤结果被丢弃、条件写反），必须经 fetchUpstreamModels 走
// /models 拉取 → 过滤 → 探测 → 缓存 全链路。
func TestFetchUpstreamModelsFiltersByEntitlement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"gpt-4o","vendor":"Azure OpenAI","capabilities":{"type":"chat","family":"gpt-4o"}},
				{"id":"gpt-4.1","vendor":"Azure OpenAI","capabilities":{"type":"chat","family":"gpt-4.1"}},
				{"id":"gpt-6-luna","vendor":"OpenAI","capabilities":{"type":"chat","family":"gpt-6-luna"},"policy":{"state":"enabled"}},
				{"id":"claude-haiku-4.5","vendor":"Anthropic","capabilities":{"type":"chat","family":"claude-haiku-4.5"},"policy":{"state":"enabled"}},
				{"id":"copilot-search-a","vendor":"Fireworks","capabilities":{"type":"chat","family":"search-agent"}}
			]}`))
		case "/chat/completions":
			body, _ := io.ReadAll(r.Body)
			model := gjson.GetBytes(body, "model").String()
			w.WriteHeader(http.StatusBadRequest)
			if model == "gpt-4o" || model == "gpt-4.1" {
				_, _ = w.Write([]byte(`{"error":{"message":"messages must be non-empty","code":""}}`))
				return
			}
			_, _ = w.Write([]byte(`{"error":{"message":"The requested model is not supported.","code":"model_not_supported"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	prevCfg := currentConfig()
	cfg := prevCfg
	cfg.BaseURL = srv.URL
	cfg.ModelCacheSecs = 300
	activeConfig.Store(cfg)
	defer activeConfig.Store(prevCfg)

	sa := &StoredAuth{
		AuthID:       "copilot-entitlement-test",
		CopilotToken: "tid=probe;exp=9999999999",
		ExpiresAt:    "2099-01-01T00:00:00Z",
	}
	got, err := fetchUpstreamModels(context.Background(), sa)
	if err != nil {
		t.Fatalf("fetchUpstreamModels: %v", err)
	}
	want := map[string]bool{"gpt-4o": true, "gpt-4.1": true}
	if len(got) != len(want) {
		t.Fatalf("expected %d entitled models, got %d: %v", len(want), len(got), got)
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("unentitled model %s must not be exposed (got %v)", m, got)
		}
	}
}

// TestFetchUpstreamModelsKeepsCandidatesWhenProbeYieldsNothing 校验探测无可用结果时不清空出口。
//
// 降级判据：探测未得到任何可用模型（上游抖动使探测整体不可用、或账号零可用）时，
// 模型出口不得被清空，必须回退到 /models 过滤结果（宁多勿空）——
// 否则一次探测异常就会让插件彻底无模型可路由。
func TestFetchUpstreamModelsKeepsCandidatesWhenProbeYieldsNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"gpt-4o","vendor":"Azure OpenAI","capabilities":{"type":"chat","family":"gpt-4o"}},
				{"id":"gpt-4.1","vendor":"Azure OpenAI","capabilities":{"type":"chat","family":"gpt-4.1"}}
			]}`))
		default:
			// 探测端点对全部模型都返回「不受支持」：探测结果为空，触发保留分支。
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"The requested model is not supported.","code":"model_not_supported"}}`))
		}
	}))
	defer srv.Close()

	prevCfg := currentConfig()
	cfg := prevCfg
	cfg.BaseURL = srv.URL
	cfg.ModelCacheSecs = 300
	activeConfig.Store(cfg)
	defer activeConfig.Store(prevCfg)

	sa := &StoredAuth{
		AuthID:       "copilot-probe-empty-test",
		CopilotToken: "tid=probe;exp=9999999999",
		ExpiresAt:    "2099-01-01T00:00:00Z",
	}
	got, err := fetchUpstreamModels(context.Background(), sa)
	if err != nil {
		t.Fatalf("fetchUpstreamModels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("empty probe result must keep the filtered candidates, got %d: %v", len(got), got)
	}
}

// TestProbeUsableModelsRunsConcurrently 校验探测并发执行，避免逐个串行拖慢模型发现。
//
// 判据：桩服务端短暂阻塞，记录同时进行中的探测请求峰值；串行实现峰值恒为 1，
// 并发实现峰值必然 ≥ 2。并发上限 6 是速度与上游压力的平衡点（实测 25 模型 3.38s）。
func TestProbeUsableModelsRunsConcurrently(t *testing.T) {
	var inFlight, peak int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt64(&inFlight, 1)
		for {
			old := atomic.LoadInt64(&peak)
			if cur <= old || atomic.CompareAndSwapInt64(&peak, old, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt64(&inFlight, -1)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"messages must be non-empty","code":""}}`))
	}))
	defer srv.Close()

	cfg := currentConfig()
	cfg.BaseURL = srv.URL
	sa := &StoredAuth{
		AuthID:       "copilot-concurrency-test",
		CopilotToken: "tid=probe;exp=9999999999",
		ExpiresAt:    "2099-01-01T00:00:00Z",
	}
	candidates := make([]string, 12)
	for i := range candidates {
		candidates[i] = "gpt-4o-" + string(rune('a'+i))
	}
	got := probeUsableModels(context.Background(), sa, cfg, candidates)
	if len(got) != len(candidates) {
		t.Fatalf("all callable models must be kept, got %d of %d", len(got), len(candidates))
	}
	if got := atomic.LoadInt64(&peak); got < 2 {
		t.Errorf("probe must run concurrently, peak in-flight=%d (serialized)", got)
	}
}
