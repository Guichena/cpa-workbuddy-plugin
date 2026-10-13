package main

import (
	"encoding/json"
	"testing"
)

// TestComputeAdaptiveMaxTokens_ZCode 验证 zcode 自适应 max_tokens 计算与 32768 最低输出底线。
func TestComputeAdaptiveMaxTokens_ZCode(t *testing.T) {
	// 1. 未传 max_tokens (0)：GLM-5.3 常规模式官方 16384 低于底线，抬升至 32768
	if got := computeAdaptiveMaxTokens("glm-5.3", 0, false); got != 32768 {
		t.Fatalf("glm-5.3 unconfigured non-reasoning = %d, want 32768", got)
	}

	// 2. 未传 max_tokens (0) 且为推理模式：GLM-5.3 补足至 ReasoningMax 128000
	if got := computeAdaptiveMaxTokens("glm-5.3", 0, true); got != 128000 {
		t.Fatalf("glm-5.3 unconfigured reasoning = %d, want 128000", got)
	}

	// 3. 客户端保守传入 8192 且为推理模式：放宽至 128000
	if got := computeAdaptiveMaxTokens("glm-5.3", 8192, true); got != 128000 {
		t.Fatalf("glm-5.3 8192 reasoning = %d, want 128000", got)
	}

	// 4. 超出上限 (200000)：截断回 128000
	if got := computeAdaptiveMaxTokens("glm-5.3", 200000, true); got != 128000 {
		t.Fatalf("glm-5.3 200000 = %d, want 128000", got)
	}

	// 5. 官方低上限模型常规模式传小值：抬升至 32768 底线
	if got := computeAdaptiveMaxTokens("glm-4-plus", 4096, false); got != 32768 {
		t.Fatalf("glm-4-plus 4096 non-reasoning = %d, want 32768", got)
	}
}

// TestResolveOfficialModelSpec_Extended 验证 zcode 规格表全扩后的家族匹配、最长键优先与未收录模型兜底。
func TestResolveOfficialModelSpec_Extended(t *testing.T) {
	cases := []struct {
		name          string
		model         string
		wantOutput    int64
		wantReasoning int64
	}{
		{name: "GLM-5.3 主用模型", model: "GLM-5.3", wantOutput: 16384, wantReasoning: 128000},
		{name: "glm-4.5 较新型号推理上限 64k", model: "glm-4.5", wantOutput: 16384, wantReasoning: 64000},
		{name: "glm-4.7 不被经典 glm-4 规则低估", model: "glm-4.7", wantOutput: 16384, wantReasoning: 64000},
		{name: "glm-4-plus 经典型号保守", model: "glm-4-plus", wantOutput: 8192, wantReasoning: 16384},
		{name: "kimi-k2.6 不被短键 kimi-k2 遮蔽", model: "kimi-k2.6", wantOutput: 16384, wantReasoning: 65536},
		{name: "deepseek-v4.1-flash 通用家族", model: "deepseek-v4.1-flash", wantOutput: 16384, wantReasoning: 64000},
		{name: "带 vendor 前缀剥离后命中", model: "openai/gpt-4o", wantOutput: 16384, wantReasoning: 16384},
		{name: "未收录模型兜底 ReasoningMax 32768", model: "some-unlisted-model", wantOutput: 8192, wantReasoning: 32768},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := resolveOfficialModelSpec(tc.model)
			if spec.MaxOutputTokens != tc.wantOutput || spec.ReasoningMax != tc.wantReasoning {
				t.Fatalf("resolveOfficialModelSpec(%s) = {output:%d, reasoning:%d}, want {output:%d, reasoning:%d}",
					tc.model, spec.MaxOutputTokens, spec.ReasoningMax, tc.wantOutput, tc.wantReasoning)
			}
		})
	}
}

// TestMutateModel_AdaptiveMaxTokens 验证 mutateModel 会就地重写自适应 max_tokens。
func TestMutateModel_AdaptiveMaxTokens(t *testing.T) {
	req := map[string]any{
		"model":      "glm-5.3",
		"max_tokens": 4096,
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
		},
		"reasoning_effort": "high",
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	mutated := mutateModel(raw, "GLM-5.3")
	var parsed map[string]any
	if err := json.Unmarshal(mutated, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed["model"] != "GLM-5.3" {
		t.Fatalf("model = %v, want GLM-5.3", parsed["model"])
	}

	mt, ok := parsed["max_tokens"].(float64)
	if !ok || int64(mt) < 32768 {
		t.Fatalf("max_tokens = %v, want >= 32768", parsed["max_tokens"])
	}
}
