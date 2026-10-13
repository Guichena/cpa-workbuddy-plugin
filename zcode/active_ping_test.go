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

func TestActivePing_ModelsQuery(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_ping_test_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	sa := &StoredAuth{
		AuthID:   "zcode-test-1",
		APIKey:   "key1.secret",
		Models:   []string{"GLM-5.3", "GLM-5.3-Flash"},
		Disabled: false,
	}
	_ = writeAuthFileDirect(filepath.Join(tempDir, "zcode-test-1.json"), sa)

	data, err := handleModelsQuery("zcode-test-1")
	if err != nil {
		t.Fatalf("handleModelsQuery failed: %v", err)
	}

	var res map[string][]string
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("unmarshal models failed: %v", err)
	}
	if len(res["models"]) != 2 || res["models"][0] != "GLM-5.3" {
		t.Fatalf("models mismatch: %+v", res)
	}
}

func TestActivePing_MockServer(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_ping_mock_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	// Mock 上游 server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "valid.key" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"msg-1","content":[{"type":"text","text":"pong"}]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_key"}`))
	}))
	defer server.Close()

	// 覆盖 BaseURL
	cfg := currentConfig()
	cfg.BaseURL = server.URL
	activeConfig.Store(cfg)

	saValid := &StoredAuth{AuthID: "zcode-valid", APIKey: "valid.key"}
	_ = writeAuthFileDirect(filepath.Join(tempDir, "zcode-valid.json"), saValid)

	saInvalid := &StoredAuth{AuthID: "zcode-invalid", APIKey: "invalid.key"}
	_ = writeAuthFileDirect(filepath.Join(tempDir, "zcode-invalid.json"), saInvalid)

	// 测试有效账号
	resValid := handleTestActiveWithAuth(saValid, "GLM-5.3")
	if !resValid.OK || resValid.Status != "active" {
		t.Fatalf("expected active status for valid account, got %+v", resValid)
	}

	// 测试无效账号
	resInvalid := handleTestActiveWithAuth(saInvalid, "GLM-5.3")
	if resInvalid.OK || resInvalid.Status != "failed" {
		t.Fatalf("expected failed status for invalid account, got %+v", resInvalid)
	}

	// 确认无效账号被打上 test_failed
	accAfter, _ := readAuthFile(filepath.Join(tempDir, "zcode-invalid.json"))
	if accAfter == nil || !accAfter.TestFailed {
		t.Fatalf("expected test_failed to be true, got %+v", accAfter)
	}
}

// TestActivePing_FirstChunkEarlyExit 验证探活收到首个流式数据块后立即返回，不等待后续长时间阻塞。
func TestActivePing_FirstChunkEarlyExit(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zcode_ping_early_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	unblock := make(chan struct{})
	defer close(unblock)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Let\"}}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-unblock:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	cfg := currentConfig()
	cfg.BaseURL = server.URL
	activeConfig.Store(cfg)

	sa := &StoredAuth{AuthID: "zcode-stream", APIKey: "valid.key"}
	_ = writeAuthFileDirect(filepath.Join(tempDir, "zcode-stream.json"), sa)

	start := time.Now()
	res := handleTestActiveWithAuth(sa, "GLM-5.3")
	elapsed := time.Since(start)
	if !res.OK || res.Status != "active" {
		t.Fatalf("expected active status, got %+v", res)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("expected early exit (< 1s), took %v", elapsed)
	}
}

// TestDoActivePingAllModelsInOrderAnySuccess 验证定时探活按候选顺序依次轮测全部模型，任一成功即刻终止并清标。
func TestDoActivePingAllModelsInOrderAnySuccess(t *testing.T) {
	resetActivePingTimes()
	defer resetActivePingTimes()

	tempDir, err := os.MkdirTemp("", "zcode_do_ping_succ_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	origPing := pingUpstreamOnceFn
	origModels := authModelsForIndexFn
	defer func() {
		pingUpstreamOnceFn = origPing
		authModelsForIndexFn = origModels
	}()

	authModelsForIndexFn = func(authID string) []string {
		return []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7"}
	}
	var attempted []string
	pingUpstreamOnceFn = func(sa *StoredAuth, model string) (bool, int, string, int64, uint64) {
		attempted = append(attempted, model)
		if model == "m7" {
			return true, http.StatusOK, "", 10, 1000
		}
		return false, http.StatusBadGateway, "down", 5, 0
	}

	sa := &StoredAuth{AuthID: "zcode-do-succ", APIKey: "valid.key"}
	sa.TestFailed = true
	_ = writeAuthFileDirect(filepath.Join(tempDir, "zcode-do-succ.json"), sa)

	if err := doActivePing(sa); err != nil {
		t.Fatalf("expected any-success to return nil, got %v", err)
	}
	if len(attempted) != 7 {
		t.Fatalf("expected all 7 models attempted in order, got %d: %v", len(attempted), attempted)
	}
	for i, want := range []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7"} {
		if attempted[i] != want {
			t.Fatalf("attempt order mismatch at %d: got %v, want %s", i, attempted, want)
		}
	}
	accAfter, _ := readAuthFile(filepath.Join(tempDir, "zcode-do-succ.json"))
	if accAfter == nil || accAfter.TestFailed {
		t.Fatalf("expected test_failed cleared after success, got %+v", accAfter)
	}
}

// TestDoActivePingAllModelsFailTagsTestFailed 验证全部模型失败时打上 test_failed 标签。
func TestDoActivePingAllModelsFailTagsTestFailed(t *testing.T) {
	resetActivePingTimes()
	defer resetActivePingTimes()

	tempDir, err := os.MkdirTemp("", "zcode_do_ping_fail_*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)
	setAccountsDir(tempDir)

	origPing := pingUpstreamOnceFn
	origModels := authModelsForIndexFn
	defer func() {
		pingUpstreamOnceFn = origPing
		authModelsForIndexFn = origModels
	}()

	authModelsForIndexFn = func(authID string) []string {
		return []string{"fail-1", "fail-2"}
	}
	pingUpstreamOnceFn = func(sa *StoredAuth, model string) (bool, int, string, int64, uint64) {
		return false, http.StatusBadGateway, "down", 5, 0
	}

	sa := &StoredAuth{AuthID: "zcode-do-fail", APIKey: "valid.key"}
	_ = writeAuthFileDirect(filepath.Join(tempDir, "zcode-do-fail.json"), sa)

	err = doActivePing(sa)
	if err == nil {
		t.Fatalf("expected error when all models fail")
	}
	accAfter, _ := readAuthFile(filepath.Join(tempDir, "zcode-do-fail.json"))
	if accAfter == nil || !accAfter.TestFailed {
		t.Fatalf("expected test_failed to be true after all-fail, got %+v", accAfter)
	}
}
