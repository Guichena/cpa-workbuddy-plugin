package main

import (
	"encoding/json"
	"testing"
)

// buildToolsProbe 渲染一次 body 并返回根对象，供 tools 对齐断言复用。
// [参数] t: 测试句柄；req: 客户端请求。
// [返回] 渲染后的 body 根对象。
func buildToolsProbe(t *testing.T, req *openAIRequest) map[string]any {
	t.Helper()
	body, err := buildQoderBody(req, "qmodel_38max", "personal_professional_trial")
	if err != nil {
		t.Fatalf("buildQoderBody: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return parsed
}

// TestBuildQoderBody_NoClientTools_StripsTemplateTools 验证客户端未声明工具时，
// 模板 baseprompt.json 硬编码的 14 个 agent 工具被整体剥离。
// 回归背景：模板工具集无条件上行会让上游把普通问答当 agent 任务作答，以
// finish_reason=tool_calls 提前结束（表现为「推理一小段就停」）。
func TestBuildQoderBody_NoClientTools_StripsTemplateTools(t *testing.T) {
	req := &openAIRequest{
		Model:    "qwen-3.8-max",
		Messages: []openAIMessage{{Role: "user", Content: "帮我分析这个项目的调度器"}},
	}
	parsed := buildToolsProbe(t, req)
	if v, present := parsed["tools"]; present {
		t.Fatalf("未声明工具时不应有 tools 字段, got %v", v)
	}
	if v, present := parsed["tool_choice"]; present {
		t.Fatalf("未声明工具时不应有 tool_choice 字段, got %v", v)
	}
	// 模板 system prompt 必须保留（上游服务器行为依赖它）。
	msgs, _ := parsed["messages"].([]any)
	hasSystem := false
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "system" {
			hasSystem = true
		}
	}
	if !hasSystem {
		t.Fatal("模板 system prompt 必须保留")
	}
}

// TestBuildQoderBody_WithClientTools_OverridesTemplate 验证客户端声明工具时，
// 用客户端工具整体覆盖模板工具集，并透传 tool_choice，使真正的 agent 客户端
// 仍获得完整工具回环。
func TestBuildQoderBody_WithClientTools_OverridesTemplate(t *testing.T) {
	clientTool := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":       "get_weather",
			"parameters": map[string]any{"type": "object"},
		},
	}
	req := &openAIRequest{
		Model:      "qwen-3.8-max",
		Messages:   []openAIMessage{{Role: "user", Content: "北京天气"}},
		Tools:      []any{clientTool},
		ToolChoice: "auto",
	}
	parsed := buildToolsProbe(t, req)
	tools, ok := parsed["tools"].([]any)
	if !ok {
		t.Fatalf("客户端声明工具时应保留 tools 字段, got %T", parsed["tools"])
	}
	if len(tools) != 1 {
		t.Fatalf("tools 长度 = %d, want 1（模板 14 个工具必须被整体覆盖）", len(tools))
	}
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if got, _ := fn["name"].(string); got != "get_weather" {
		t.Fatalf("tools[0].function.name = %q, want get_weather", got)
	}
	if got, _ := parsed["tool_choice"].(string); got != "auto" {
		t.Fatalf("tool_choice = %v, want auto", parsed["tool_choice"])
	}
	// 模板内建工具名不得残留（防覆盖不彻底）。
	if got, _ := fn["name"].(string); got == "Bash" || got == "Glob" || got == "Read" {
		t.Fatalf("模板内建工具未被覆盖: %q", got)
	}
}

// TestBuildQoderBody_EmptyClientTools_StripsTemplateTools 验证客户端传空 tools
// 数组（等价于未声明）时同样剥离模板工具集。
func TestBuildQoderBody_EmptyClientTools_StripsTemplateTools(t *testing.T) {
	req := &openAIRequest{
		Model:    "qwen-3.8-max",
		Messages: []openAIMessage{{Role: "user", Content: "你好"}},
		Tools:    []any{},
	}
	parsed := buildToolsProbe(t, req)
	if v, present := parsed["tools"]; present {
		t.Fatalf("空 tools 数组应等价于未声明, got %v", v)
	}
}
