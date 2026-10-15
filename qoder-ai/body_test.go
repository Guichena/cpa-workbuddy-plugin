package main

import (
	"encoding/json"
	"testing"
)

// TestOpenAIRequestAcceptsContentPartsArray 验证多模态 content 部件数组
// （[{"type":"text","text":"..."}]）能被解析并提取出文本，而非直接报
// "cannot unmarshal array into ... content of type string"。
func TestOpenAIRequestAcceptsContentPartsArray(t *testing.T) {
	raw := []byte(`{"model":"Qwen3.8-Flash","stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"你好"}]}]}`)
	req := &openAIRequest{}
	if err := json.Unmarshal(raw, req); err != nil {
		t.Fatalf("content 部件数组应能解析, got error: %v", err)
	}
	if got := extractLatestUserPrompt(req.Messages); got != "你好" {
		t.Fatalf("expected 你好, got %q", got)
	}
}

// TestOpenAIRequestAcceptsPlainStringContent 验证纯字符串 content 形态仍兼容。
func TestOpenAIRequestAcceptsPlainStringContent(t *testing.T) {
	raw := []byte(`{"model":"Qwen3.8-Flash","messages":[{"role":"user","content":"hello"}]}`)
	req := &openAIRequest{}
	if err := json.Unmarshal(raw, req); err != nil {
		t.Fatalf("字符串 content 应能解析, got error: %v", err)
	}
	if got := extractLatestUserPrompt(req.Messages); got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
}

// TestOpenAIRequestJoinsMultipleTextParts 验证多个文本部件按换行拼接。
func TestOpenAIRequestJoinsMultipleTextParts(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}`)
	req := &openAIRequest{}
	if err := json.Unmarshal(raw, req); err != nil {
		t.Fatalf("多文本部件应能解析, got error: %v", err)
	}
	if got := extractLatestUserPrompt(req.Messages); got != "a\nb" {
		t.Fatalf("expected a\\nb, got %q", got)
	}
}

// TestBuildQoderBodyFromContentPartsPayload 验证多模态 payload 能走通
// handleExecExecute 的完整构建链路：解析 → 提取文本 → 渲染上游 body。
func TestBuildQoderBodyFromContentPartsPayload(t *testing.T) {
	payload := []byte(`{"model":"Qwen3.8-Flash","messages":[{"role":"system","content":"sys"},{"role":"user","content":[{"type":"text","text":"写一首诗"}]}]}`)
	req := &openAIRequest{}
	if err := json.Unmarshal(payload, req); err != nil {
		t.Fatalf("payload 解析失败: %v", err)
	}
	body, err := buildQoderBody(req, cpaToUpstreamKey(stripProviderPrefix("Qwen3.8-Flash")), "")
	if err != nil {
		t.Fatalf("buildQoderBody 失败: %v", err)
	}
	var rendered map[string]any
	if err := json.Unmarshal(body, &rendered); err != nil {
		t.Fatalf("上游 body 不是合法 JSON: %v", err)
	}
	cc, _ := rendered["chat_context"].(map[string]any)
	txt, _ := cc["text"].(map[string]any)
	if got, _ := txt["text"].(string); got != "写一首诗" {
		t.Fatalf("expected 写一首诗 in chat_context.text.text, got %q", got)
	}
	mc, _ := rendered["model_config"].(map[string]any)
	if got, _ := mc["key"].(string); got != "qfmodel" {
		t.Fatalf("expected upstream key qfmodel, got %q", got)
	}
}

// TestOpenAIRequestPreservesToolStructure 验证 UnmarshalJSON 同步解析
// tool_calls / tool_call_id / name，避免多轮 agent 往返中工具结果失去配对。
func TestOpenAIRequestPreservesToolStructure(t *testing.T) {
	raw := []byte(`{"model":"Qwen3.8-Flash","messages":[` +
		`{"role":"user","content":"列出文件"},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"Glob","arguments":"{}"}}]},` +
		`{"role":"tool","content":"a.go\nb.go","tool_call_id":"call_1","name":"Glob"}]}`)
	req := &openAIRequest{}
	if err := json.Unmarshal(raw, req); err != nil {
		t.Fatalf("含工具结构的 messages 应能解析, got error: %v", err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages 长度 = %d, want 3", len(req.Messages))
	}
	if got := len(req.Messages[1].ToolCalls); got != 1 {
		t.Fatalf("assistant.tool_calls 长度 = %d, want 1", got)
	}
	if got := req.Messages[2].ToolCallID; got != "call_1" {
		t.Fatalf("tool.tool_call_id = %q, want call_1", got)
	}
	if got := req.Messages[2].Name; got != "Glob" {
		t.Fatalf("tool.name = %q, want Glob", got)
	}
}

// TestBuildQoderBodyPreservesToolPairing 验证重建上游 messages 时保留
// tool_calls / tool_call_id / name，使工具结果与先前的工具调用保持配对。
func TestBuildQoderBodyPreservesToolPairing(t *testing.T) {
	req := &openAIRequest{
		Model: "qwen-3.8-max",
		Messages: []openAIMessage{
			{Role: "user", Content: "列出文件"},
			{Role: "assistant", ToolCalls: []any{map[string]any{"id": "call_1", "type": "function"}}},
			{Role: "tool", Content: "a.go", ToolCallID: "call_1", Name: "Glob"},
		},
	}
	parsed := buildToolsProbe(t, req)
	msgs, _ := parsed["messages"].([]any)
	var asst, tool map[string]any
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		switch mm["role"] {
		case "assistant":
			asst = mm
		case "tool":
			tool = mm
		}
	}
	if asst == nil || tool == nil {
		t.Fatal("assistant / tool 消息必须出现在重建后的 messages 中")
	}
	if _, present := asst["tool_calls"]; !present {
		t.Fatal("assistant.tool_calls 必须在重建后保留")
	}
	if got, _ := tool["tool_call_id"].(string); got != "call_1" {
		t.Fatalf("tool.tool_call_id = %q, want call_1", got)
	}
	if got, _ := tool["name"].(string); got != "Glob" {
		t.Fatalf("tool.name = %q, want Glob", got)
	}
}
