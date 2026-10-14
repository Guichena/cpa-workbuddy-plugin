package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

// TestBillingDo_RetriesOn5xxThenSucceeds 验证计费调用在 5xx 后按退避序列重试，
// 并在上游恢复时返回成功响应（对齐 workbuddy billingCall 语义）。
func TestBillingDo_RetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"err":"upstream busy"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := billingDo(req, nil)
	if err != nil {
		t.Fatalf("billingDo 应在重试后成功, got error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("上游调用次数 = %d, want 3（2 次 5xx + 1 次成功）", got)
	}
}

// TestBillingDo_NoRetryOn4xx 验证 4xx 不重试（非瞬时错误，立即返回）。
func TestBillingDo_NoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"err":"bad token"}`))
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := billingDo(req, nil)
	if err != nil {
		t.Fatalf("billingDo: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want 401", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("4xx 不应重试, 调用次数 = %d, want 1", got)
	}
}

// TestBillingDo_RebuildsBodyOnRetry 验证带 body 的请求在重试时重建 Body，
// 使第二次请求仍能读到完整请求体（hostHTTPDo 会消费 req.Body）。
func TestBillingDo_RebuildsBodyOnRetry(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		bodies = append(bodies, string(buf[:n]))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := []byte(`{"claim":"x"}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := billingDo(req, payload); err != nil {
		t.Fatalf("billingDo: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("调用次数 = %d, want 2", len(bodies))
	}
	for i, b := range bodies {
		if b != string(payload) {
			t.Fatalf("第 %d 次请求体 = %q, want %q（重试未重建 Body）", i+1, b, payload)
		}
	}
}

// TestRecordUsageFeed_ReportsReasoningEffort 验证 reasoning_effort 被写入 feed 记录。
// 回归背景：qoderwork 原先不重写也不上报该字段，feed 恒为空，导致 dashboard
// 用量明细缺失推理强度维度（与 workbuddy 数据不可比）。
func TestRecordUsageFeed_ReportsReasoningEffort(t *testing.T) {
	dir := t.TempDir()
	feedPath := filepath.Join(dir, "feed.ndjson")
	usageFeedMu.Lock()
	usageFeedEnabled = true
	usageFeedPath = feedPath
	usageFeedMu.Unlock()
	defer func() {
		usageFeedMu.Lock()
		usageFeedEnabled = true
		usageFeedPath = ""
		usageFeedMu.Unlock()
	}()

	recordUsageFeed("qwen-3.8-max", "qmodel_38max", "uid-1", time.Now(), usage.Detail{}, false, 200, "high", 0, "bob", "")

	raw, err := os.ReadFile(feedPath)
	if err != nil {
		t.Fatalf("read feed: %v", err)
	}
	lines := splitLines(string(raw))
	if len(lines) == 0 {
		t.Fatal("feed 未写入任何行")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("feed 行不是合法 JSON: %v", err)
	}
	if got, _ := m["reasoning_effort"].(string); got != "high" {
		t.Fatalf("feed reasoning_effort = %q, want high", got)
	}
}
