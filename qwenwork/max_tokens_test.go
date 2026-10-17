package main

import (
	"encoding/json"
	"testing"
)

// TestAdaptiveMaxTokens 验证按官方规格自适应计算 max_tokens。
func TestAdaptiveMaxTokens(t *testing.T) {
	cases := []struct {
		name        string
		model       string
		requested   int64
		isReasoning bool
		wantTokens  int64
	}{
		{
			name:        "Qwen3.8-Flash 默认非思考 -> 抬升至 32768 最低输出底线",
			model:       "Qwen3.8-Flash",
			requested:   0,
			isReasoning: false,
			wantTokens:  32768,
		},
		{
			name:        "Qwen3.8-Flash 思考模式 + 传 8192 -> 提升至官方推理上限 32768",
			model:       "Qwen3.8-Flash",
			requested:   8192,
			isReasoning: true,
			wantTokens:  32768,
		},
		{
			name:        "GLM-5.3 思考模式 + 传 8192 -> 提升至官方推理上限 128000",
			model:       "GLM-5.3",
			requested:   8192,
			isReasoning: true,
			wantTokens:  128000,
		},
		{
			name:        "超限值 200000 -> 截断至官方允许上限 32768",
			model:       "flash",
			requested:   200000,
			isReasoning: true,
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

// TestWbModels_OfficialSpecs 验证静态模型表的输出上限按官方规格填充。
func TestWbModels_OfficialSpecs(t *testing.T) {
	for _, m := range wbModels() {
		if m.MaxCompletionTokens < minMaxTokensFloor {
			t.Fatalf("model %s MaxCompletionTokens = %d, want >= %d",
				m.ID, m.MaxCompletionTokens, minMaxTokensFloor)
		}
	}
}

// TestPrepareUpstreamBody_ForcesStreamAndRelaxesTokens 验证透传改写：
// 强制 stream=true，并在思考模式下把保守的 8192 放宽到官方推理上限。
func TestPrepareUpstreamBody_ForcesStreamAndRelaxesTokens(t *testing.T) {
	raw := []byte(`{"model":"qwen3.8-max-preview","stream":false,"max_tokens":8192,` +
		`"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`)
	out := prepareUpstreamBody(raw)
	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("prepareUpstreamBody produced invalid JSON: %v", err)
	}
	if stream, _ := obj["stream"].(bool); !stream {
		t.Fatal("stream must be forced to true")
	}
	if got := jsonI64(obj, "max_tokens"); got != 32768 {
		t.Fatalf("max_tokens = %d, want 32768", got)
	}
	// 原请求的其余字段必须原样保留。
	if _, ok := obj["messages"]; !ok {
		t.Fatal("messages must be preserved by passthrough rewrite")
	}
}

// TestPrepareUpstreamBody_InvalidJSONPassthrough 验证非法 JSON 原样返回，
// 保证请求仍能发到上游由其报错，而不是被插件吞掉。
func TestPrepareUpstreamBody_InvalidJSONPassthrough(t *testing.T) {
	raw := []byte(`not-json`)
	if got := prepareUpstreamBody(raw); string(got) != string(raw) {
		t.Fatalf("invalid JSON must pass through unchanged, got %q", got)
	}
}
