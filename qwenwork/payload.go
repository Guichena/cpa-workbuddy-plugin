// payload.go 在透传路径上对客户端请求体做最小改写。
//
// QwenWork 走标准 OpenAI 兼容协议，客户端请求体可以原样转发；只有两处必须
// 由插件兜底：强制 stream=true（上游按 SSE 返回），以及按模型官方规格自适应
// 放宽 max_tokens，避免思考模型被客户端保守值提前截断。
package main

import (
	"encoding/json"
	"strings"
)

// prepareUpstreamBody 对客户端请求体做透传前的最小改写。
//
// [参数] bodyRaw: 客户端原始请求体。
// [返回] 改写后的请求体；解析失败时原样返回，保证请求仍可发出。
// 最近修改时间：2026-10-18 00:36:00；改动原因：模板链路删除后补回透传改写。
func prepareUpstreamBody(bodyRaw []byte) []byte {
	if len(bodyRaw) == 0 {
		return bodyRaw
	}
	var obj map[string]any
	if err := json.Unmarshal(bodyRaw, &obj); err != nil {
		return bodyRaw
	}
	// 1. 上游按 SSE 返回；同步路径同样依赖事件流聚合。
	obj["stream"] = true
	// 2. max_tokens 自适应：思考模型被客户端传入保守小值时放宽到官方上限。
	relaxMaxTokensInPlace(obj)
	out, err := json.Marshal(obj)
	if err != nil {
		return bodyRaw
	}
	return out
}

// relaxMaxTokensInPlace 原地按模型官方规格放宽 max_tokens。
//
// [参数] obj: 已解析的请求体。
// [返回] 无。
// 最近修改时间：2026-10-18 00:36:00；改动原因：从 zcode 同步透传式改写。
func relaxMaxTokensInPlace(obj map[string]any) {
	model := jsonStr(obj, "model")
	effort := strings.ToLower(strings.TrimSpace(jsonStr(obj, "reasoning_effort")))
	isReasoning := effort != "" && effort != "none"
	if !isReasoning {
		mLower := strings.ToLower(model)
		if strings.Contains(mLower, "reasoner") || strings.Contains(mLower, "thinking") {
			isReasoning = true
		}
	}
	val := jsonI64(obj, "max_tokens", "max_completion_tokens")
	finalTokens := computeAdaptiveMaxTokens(model, val, isReasoning)
	if finalTokens != val || obj["max_tokens"] == nil {
		obj["max_tokens"] = finalTokens
		// OpenAI 兼容端点只认 max_tokens；同时存在时移除另一形态避免冲突。
		delete(obj, "max_completion_tokens")
	}
}

// jsonStr 读取对象里的字符串字段。
//
// [参数] m: 请求体对象；key: 字段名。
// [返回] 字段值；缺失或类型不符时返回空串。
// 最近修改时间：2026-10-18 00:36:00；改动原因：透传改写需要读取模型名。
func jsonStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
