package main

// 跨平台失败切换回归：终态错误必须经宿主 stream.emit 信封的 "error" 字段
// （宿主映射为 chunk.Err）发出，绝不能当 payload 数据帧发出。否则宿主
// conductor 视请求为成功，qoderwork 全部账号失败后宿主不会切换其他平台账号。

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalStreamErrorEnvelopeUsesErrorField(t *testing.T) {
	raw, err := marshalStreamErrorEnvelope("stream-1", "upstream account pool exhausted after 2 attempt(s)")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var errMsg string
	if err := json.Unmarshal(probe["error"], &errMsg); err != nil {
		t.Fatalf("error field must be a string: %v", err)
	}
	if errMsg != "upstream account pool exhausted after 2 attempt(s)" {
		t.Fatalf("error = %q, want exhausted message", errMsg)
	}
	if _, ok := probe["payload"]; ok {
		t.Fatalf("payload key present: error must travel as chunk.Err, not as a data frame: %s", raw)
	}
	var streamID string
	if err := json.Unmarshal(probe["stream_id"], &streamID); err != nil || streamID != "stream-1" {
		t.Fatalf("stream_id = %q err=%v, want stream-1", streamID, err)
	}
}

// TestMarshalStreamErrorEnvelopeCarriesQueueStatus 锁定第 2 步的流式侧：
// 排队帧的 stream.emit 错误信封必须额外带 http_status=503，供宿主跨 provider
// 轮转；普通错误不携带该字段（宿主忽略未知字段，行为与旧版一致）。
func TestMarshalStreamErrorEnvelopeCarriesQueueStatus(t *testing.T) {
	// 1. 排队帧 → http_status=503
	raw, err := marshalStreamErrorEnvelope("stream-q", queueFrameLivePayload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var status int
	if err := json.Unmarshal(probe["http_status"], &status); err != nil {
		t.Fatalf("http_status must be present for queue frames: %v (raw=%s)", err, raw)
	}
	if status != 503 {
		t.Fatalf("queue frame stream http_status = %d, want 503", status)
	}
	// 错误仍以 "error" 字符串字段传递（不得退化为 payload 数据帧）。
	if _, ok := probe["payload"]; ok {
		t.Fatalf("payload key present: %s", raw)
	}

	// 2. 非排队错误 → 不携带 http_status
	plain, err := marshalStreamErrorEnvelope("stream-p", "some other failure")
	if err != nil {
		t.Fatalf("marshal plain: %v", err)
	}
	if strings.Contains(string(plain), "http_status") {
		t.Fatalf("non-queue stream error must omit http_status: %s", plain)
	}
}

// TestMarshalStreamErrorEnvelopeCarriesResetStatus 锁定断流失败的信封状态码：
// 上游读取中断必须以 502 交给宿主（网关类失败），排队帧仍保持 503，普通错误
// 与已泄漏断流不携带该字段。
func TestMarshalStreamErrorEnvelopeCarriesResetStatus(t *testing.T) {
	// 1. 断流错误 → http_status=502
	resetMsg := "upstream stream read error: read tcp 10.0.0.1:443: connection reset by peer"
	raw, err := marshalStreamErrorEnvelope("stream-r", resetMsg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var status int
	if err := json.Unmarshal(probe["http_status"], &status); err != nil {
		t.Fatalf("http_status must be present for stream read errors: %v (raw=%s)", err, raw)
	}
	if status != 502 {
		t.Fatalf("stream read error http_status = %d, want 502", status)
	}
	var errMsg string
	if err := json.Unmarshal(probe["error"], &errMsg); err != nil || errMsg != resetMsg {
		t.Fatalf("error = %q err=%v, want reset message", errMsg, err)
	}
	if _, ok := probe["payload"]; ok {
		t.Fatalf("payload key present: %s", raw)
	}

	// 2. 排队帧回归 → 仍为 503
	queueRaw, err := marshalStreamErrorEnvelope("stream-q", queueFrameLivePayload)
	if err != nil {
		t.Fatalf("marshal queue: %v", err)
	}
	var queueProbe map[string]json.RawMessage
	if err := json.Unmarshal(queueRaw, &queueProbe); err != nil {
		t.Fatalf("unmarshal queue: %v", err)
	}
	if err := json.Unmarshal(queueProbe["http_status"], &status); err != nil || status != 503 {
		t.Fatalf("queue frame http_status = %d err=%v, want 503", status, err)
	}

	// 3. 已泄漏断流：带泄漏标记的文本不得携带 http_status（防宿主重复输出）
	leakedMsg := "upstream stream read error: connection reset by peer (" + streamReadErrorLeakedMarker + ")"
	leaked, err := marshalStreamErrorEnvelope("stream-l", leakedMsg)
	if err != nil {
		t.Fatalf("marshal leaked: %v", err)
	}
	if strings.Contains(string(leaked), "http_status") {
		t.Fatalf("leaked stream read error must omit http_status: %s", leaked)
	}
	// 4. 普通错误 → 不携带 http_status
	plainLeak, err := marshalStreamErrorEnvelope("stream-l2", "client disconnected after partial output")
	if err != nil {
		t.Fatalf("marshal plain leak: %v", err)
	}
	if strings.Contains(string(plainLeak), "http_status") {
		t.Fatalf("non-classified error must omit http_status: %s", plainLeak)
	}
}
