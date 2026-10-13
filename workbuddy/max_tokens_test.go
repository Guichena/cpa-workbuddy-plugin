package main

import (
	"encoding/json"
	"testing"
)

// TestAdaptiveMaxTokens 验证按模型权威官方规格自适应计算的核心逻辑。
// [参数] t: 测试上下文
// [返回] 无
// 最近修改时间：2026-10-13 02:00:00；改动原因：验证官方规格自适应放宽与截断。
func TestAdaptiveMaxTokens(t *testing.T) {
	cases := []struct {
		name        string
		model       string
		requested   int64
		isReasoning bool
		wantTokens  int64
	}{
		{
			name:        "DeepSeek 思考模式 + Qoder 写死 8192 -> 提升至官方推理上限 64000",
			model:       "deepseek-v4.1-flash",
			requested:   8192,
			isReasoning: true,
			wantTokens:  64000,
		},
		{
			name:        "DeepSeek 非思考模式 + 未传 -> 抬升至 32768 最低输出底线",
			model:       "deepseek-v4.1-flash",
			requested:   0,
			isReasoning: false,
			wantTokens:  32768,
		},
		{
			name:        "GLM-5.3 思考模式 + 传 8192 -> 提升至官方推理上限 128000",
			model:       "glm-5.3",
			requested:   8192,
			isReasoning: true,
			wantTokens:  128000,
		},
		{
			name:        "Claude 3.7 Sonnet 思考模式 + 传 8192 -> 提升至官方思考上限 64000",
			model:       "claude-3-7-sonnet",
			requested:   8192,
			isReasoning: true,
			wantTokens:  64000,
		},
		{
			name:        "Claude 3.5 Sonnet 常规模式 + 传 8192 -> 抬升至 32768 最低输出底线",
			model:       "claude-3-5-sonnet",
			requested:   8192,
			isReasoning: false,
			wantTokens:  32768,
		},
		{
			name:        "客户端传超大值 200000 -> 安全截断至官方允许上限 64000 避免网关 400",
			model:       "deepseek-v4.1-flash",
			requested:   200000,
			isReasoning: true,
			wantTokens:  64000,
		},
		{
			name:        "Kimi-K2.6 思考模式 + 未传 -> 补足为官方上限 65536",
			model:       "kimi-k2.6",
			requested:   0,
			isReasoning: true,
			wantTokens:  65536,
		},
		{
			name:        "未收录模型 思考模式 + 传 8192 -> 兜底放宽至 32768",
			model:       "some-unlisted-model",
			requested:   8192,
			isReasoning: true,
			wantTokens:  32768,
		},
		{
			name:        "未收录模型 常规模式 + 未传 -> 兜底抬升至 32768 最低输出底线",
			model:       "some-unlisted-model",
			requested:   0,
			isReasoning: false,
			wantTokens:  32768,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeAdaptiveMaxTokens(tc.model, tc.requested, tc.isReasoning)
			if got != tc.wantTokens {
				t.Fatalf("computeAdaptiveMaxTokens(%s, %d, %v) = %d, want %d",
					tc.model, tc.requested, tc.isReasoning, got, tc.wantTokens)
			}
		})
	}
}

// TestRelaxMaxTokensInPlace_Adaptive 验证 payload 反序列化后自适应放宽/截断行为。
// [参数] t: 测试上下文
// [返回] 无
// 最近修改时间：2026-10-13 02:00:00；改动原因：验证入参 map 修改与归一化。
func TestRelaxMaxTokensInPlace_Adaptive(t *testing.T) {
	// 场景 1：DeepSeek-V4.1-Flash + reasoning_effort: high + max_tokens: 8192
	body := map[string]any{
		"model":            "deepseek-v4.1-flash",
		"reasoning_effort": "high",
		"max_tokens":       float64(8192),
	}
	changed := relaxMaxTokensInPlace(body)
	if !changed {
		t.Fatal("expected changed = true")
	}
	if got := jsonI64(body, "max_tokens"); got != 64000 {
		t.Fatalf("max_tokens = %d, want 64000", got)
	}

	// 场景 2：带 max_completion_tokens 8192 -> 提升为 64000 且移除旧字段
	body2 := map[string]any{
		"model":                 "deepseek-v4.1-flash",
		"reasoning_effort":      "high",
		"max_completion_tokens": float64(8192),
	}
	relaxMaxTokensInPlace(body2)
	if got := jsonI64(body2, "max_tokens"); got != 64000 {
		t.Fatalf("max_tokens = %d, want 64000", got)
	}
	if _, exists := body2["max_completion_tokens"]; exists {
		t.Fatal("expected max_completion_tokens to be removed")
	}
}

// TestPrepareUpstreamBody_AdaptiveMaxTokens 验证端到端 prepareUpstreamBody 的自适应结果。
// [参数] t: 测试上下文
// [返回] 无
// 最近修改时间：2026-10-13 02:00:00；改动原因：验证完整序列化流式输出。
func TestPrepareUpstreamBody_AdaptiveMaxTokens(t *testing.T) {
	in := []byte(`{"model":"deepseek-v4.1-flash","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}],"max_tokens":8192}`)
	out := prepareUpstreamBody(in, nil, nil, "deepseek-v4.1-flash")

	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := jsonI64(obj, "max_tokens"); got != 64000 {
		t.Fatalf("max_tokens = %d, want 64000; raw: %s", got, string(out))
	}
}
