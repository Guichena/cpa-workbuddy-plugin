// body.go 定义透传路径需要的客户端请求视图。
//
// QwenWork 走标准 OpenAI 兼容协议，请求体整体透传给上游（见 payload.go），
// 因此这里只需解析插件自己会用到的字段：模型名、流式标记与推理强度。
package main

import (
	"strings"
)

// cpaToUpstreamKey 把 CPA 侧模型名映射为上游模型 ID。
//
// QwenWork 走 OpenAI 兼容协议，模型 ID 与上游完全一致（实测 flash / pro /
// qwen3.8-max-preview 三档），因此这里只做 provider 前缀剥离，不再重写名字。
// [参数] cpaModel 客户端请求的模型名。
// [返回] 上游模型 ID（与入参一致）。
// 最近修改时间：2026-10-18 00:32:00；改动原因：Qoder 专有 key 映射在 QwenWork 不适用。
func cpaToUpstreamKey(cpaModel string) string {
	return strings.TrimSpace(cpaModel)
}

// openAIRequest 是透传路径需要读取的客户端请求字段。
type openAIRequest struct {
	Model           string `json:"model"`
	Stream          bool   `json:"stream"`
	MaxTokens       int64  `json:"max_tokens"`
	ReasoningEffort string `json:"reasoning_effort"`
}
