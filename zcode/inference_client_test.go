// inference_client_test.go 覆盖 zcode 推理客户端与元数据客户端的超时职责切分，防止长思考输出被整体超时掐断。
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestInferenceHTTPClient_HasNoOverallTimeout 验证推理客户端不设整体超时、元数据客户端保留 15 秒快速失败边界。
func TestInferenceHTTPClient_HasNoOverallTimeout(t *testing.T) {
	if inferenceHTTPClient == nil {
		t.Fatal("inferenceHTTPClient 未初始化")
	}
	if inferenceHTTPClient.Timeout != 0 {
		t.Fatalf("推理客户端不得设置整体超时，实际为 %v", inferenceHTTPClient.Timeout)
	}

	if modelHTTPClient == nil || modelHTTPClient.Timeout != 15*time.Second {
		t.Fatalf("元数据客户端应保持 15s 整体超时，实际为 %v", modelHTTPClient.Timeout)
	}

	tr, ok := inferenceHTTPClient.Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("推理客户端必须使用显式 http.Transport 承载按操作 deadline，实际类型 %T", inferenceHTTPClient.Transport)
	}
	if tr.DialContext == nil {
		t.Error("推理客户端缺少拨号 deadline（DialContext）")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Error("推理客户端缺少 TLS 握手 deadline")
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Error("推理客户端缺少响应头等待 deadline")
	}
}

// TestInferenceClientToleratesSlowBody 验证推理客户端能完整读完慢速响应体，而带整体超时的客户端会在中途失败。
func TestInferenceClientToleratesSlowBody(t *testing.T) {
	const streamSpan = 600 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 6; i++ {
			_, _ = fmt.Fprintf(w, "data: chunk-%d\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(streamSpan / 6)
		}
	}))
	defer srv.Close()

	readAll := func(client *http.Client, label string) error {
		resp, err := client.Get(srv.URL)
		if err != nil {
			return fmt.Errorf("%s: 请求失败: %w", label, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("%s: 读取响应体失败: %w", label, err)
		}
		if !strings.Contains(string(body), "chunk-5") {
			return fmt.Errorf("%s: 响应体被截断，实际为 %q", label, string(body))
		}
		return nil
	}

	// 1. 整体超时 300ms 复刻旧形态：慢响应体应在读完前失败
	legacyShape := &http.Client{Timeout: 300 * time.Millisecond}
	if err := readAll(legacyShape, "legacy(timeout=300ms)"); err == nil {
		t.Fatal("哨兵失效：带整体超时的客户端本应在慢响应体上失败，实际成功")
	}

	// 2. 推理客户端应完整读完
	if err := readAll(inferenceHTTPClient, "inferenceHTTPClient"); err != nil {
		t.Fatalf("推理客户端应容忍慢响应体，实际失败：%v", err)
	}
}
