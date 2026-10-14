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

// toolProbe 构造一个最小可用的客户端工具声明，供 tool_choice 用例复用。
func toolProbe() []any {
	return []any{map[string]any{
		"type":     "function",
		"function": map[string]any{"name": "get_weather", "parameters": map[string]any{"type": "object"}},
	}}
}

// TestNormalizeToolChoice 覆盖 tool_choice 各形态的归一结果。
// 回归背景：上游只认字符串形态，对象形与 "none" 会被静默忽略——实测
// tool_choice:"none" 时上游照常发出工具调用，与「禁用工具」意图相反。
func TestNormalizeToolChoice(t *testing.T) {
	cases := []struct {
		name         string
		in           any
		wantChoice   string
		wantSuppress bool
	}{
		{"nil", nil, "", false},
		{"空字符串", "", "", false},
		{"字符串 auto", "auto", "auto", false},
		{"字符串 required", "required", "required", false},
		{"字符串 NONE 大写", "NONE", "", true},
		{"对象 type=none", map[string]any{"type": "none"}, "", true},
		{"对象 type=auto", map[string]any{"type": "auto"}, "auto", false},
		{"对象 type=required", map[string]any{"type": "required"}, "required", false},
		{"对象 type=function 带 function.name", map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}, "get_weather", false},
		{"对象 type=function 带顶层 name", map[string]any{"type": "function", "name": "search"}, "search", false},
		{"对象 type=function 无名回退 auto", map[string]any{"type": "function"}, "auto", false},
		{"对象 未知 type 视为缺省", map[string]any{"type": "weird"}, "", false},
		{"非法类型（数字）视为缺省", 123, "", false},
	}
	for _, c := range cases {
		gotChoice, gotSuppress := normalizeToolChoice(c.in)
		if gotChoice != c.wantChoice || gotSuppress != c.wantSuppress {
			t.Errorf("%s: normalizeToolChoice(%v) = (%q, %v), want (%q, %v)",
				c.name, c.in, gotChoice, gotSuppress, c.wantChoice, c.wantSuppress)
		}
	}
}

// TestBuildQoderBody_ToolChoiceNone_SuppressesTools 验证客户端以 tool_choice=none
// 明确禁用工具时，tools 与 tool_choice 均被剥离（上游不认 "none"，唯一可靠抑制）。
func TestBuildQoderBody_ToolChoiceNone_SuppressesTools(t *testing.T) {
	for _, tc := range []any{"none", map[string]any{"type": "none"}} {
		req := &openAIRequest{
			Model:      "qwen-3.8-max",
			Messages:   []openAIMessage{{Role: "user", Content: "你好"}},
			Tools:      toolProbe(),
			ToolChoice: tc,
		}
		parsed := buildToolsProbe(t, req)
		if v, present := parsed["tools"]; present {
			t.Fatalf("tool_choice=%v 时应剥离 tools, got %v", tc, v)
		}
		if v, present := parsed["tool_choice"]; present {
			t.Fatalf("tool_choice=%v 时应剥离 tool_choice, got %v", tc, v)
		}
	}
}

// TestBuildQoderBody_ToolChoiceObject_Normalized 验证对象形 tool_choice 被归一为
// 上游可识别的字符串形态（原样透传对象会被上游静默忽略）。
func TestBuildQoderBody_ToolChoiceObject_Normalized(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{map[string]any{"type": "auto"}, "auto"},
		{map[string]any{"type": "required"}, "required"},
		{map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}, "get_weather"},
	}
	for _, c := range cases {
		req := &openAIRequest{
			Model:      "qwen-3.8-max",
			Messages:   []openAIMessage{{Role: "user", Content: "北京天气"}},
			Tools:      toolProbe(),
			ToolChoice: c.in,
		}
		parsed := buildToolsProbe(t, req)
		if got, _ := parsed["tool_choice"].(string); got != c.want {
			t.Fatalf("tool_choice=%v 归一为 %q, want %q", c.in, got, c.want)
		}
	}
}
