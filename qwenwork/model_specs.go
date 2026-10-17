// model_specs.go 维护各大主流模型的权威官方规格表与自适应 max_tokens 计算逻辑。
package main

import (
	"strings"
)

// minMaxTokensFloor 是所有模型对外输出的统一最低 max_tokens 底线。
const minMaxTokensFloor int64 = 32768

// applyMaxTokensFloor 把低于统一底线的 max_tokens 抬升至底线。
// [参数] v: 待校验的 max_tokens 值
// [返回] 不低于 32768 的值
// 最近修改时间：2026-10-13 15:00:00；新增统一最低输出底线。
func applyMaxTokensFloor(v int64) int64 {
	if v < minMaxTokensFloor {
		return minMaxTokensFloor
	}
	return v
}

// ModelOfficialSpec 定义模型的官方规格参数。
type ModelOfficialSpec struct {
	ContextWindow   int64 // 官方最大上下文长度
	MaxOutputTokens int64 // 官方常规最大输出 token
	ReasoningMax    int64 // 官方思维链/推理模式下的最大输出 token
}

// 常见主流模型的权威官方规格表（对齐宿主 models.json 与各厂商权威文档）。
// QwenWork 的模型 ID 与上游一致（flash / pro / qwen3.8-max-preview 等），
// 不保留 Qoder 的 qmodel_* 内部代号。
var officialModelSpecs = map[string]ModelOfficialSpec{
	// DeepSeek 家族：推理模式官方支持深度长思考
	"deepseek-v4.1-flash": {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"deepseek-v4-flash":   {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"deepseek-v4-pro":     {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"deepseek-chat":       {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 8192},
	"deepseek-reasoner":   {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 64000},

	// GLM 智谱家族
	"glm-5.3":       {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"glm-5.3-flash": {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"glm-5.2":       {ContextWindow: 200000, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"glm-4-plus":    {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 16384},

	// Kimi 家族
	"kimi-k2.6":         {ContextWindow: 262144, MaxOutputTokens: 16384, ReasoningMax: 65536},
	"kimi-k2.8":         {ContextWindow: 262144, MaxOutputTokens: 16384, ReasoningMax: 65536},
	"kimi-k2.8-preview": {ContextWindow: 262144, MaxOutputTokens: 16384, ReasoningMax: 65536},
	"kimi-k3":           {ContextWindow: 262144, MaxOutputTokens: 16384, ReasoningMax: 65536},
	"kimi-k2":           {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 32768},
	"kimi-k2-thinking":  {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 32768},

	// QwenWork 真实档位（/api/desktop/chat-modes 实测下发）：
	// flash / pro / qwen3.8-max-preview，均为 1M 输入、推理档。
	"flash":               {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"pro":                 {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"qwen3.8-max-preview": {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},

	// Qwen 通义千问家族
	"qwen3.8-max":   {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"qwen3.8-flash": {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"qwen3.7-max":   {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"qwen3.7-plus":  {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"qwen-2.5-72b":  {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 16384},

	// MiniMax 家族
	"minimax-m3": {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},

	// Claude 家族（Thinking Beta 模式最高 64k / 128k）
	"claude-opus-4-6":   {ContextWindow: 1000000, MaxOutputTokens: 8192, ReasoningMax: 128000},
	"claude-sonnet-4-6": {ContextWindow: 200000, MaxOutputTokens: 8192, ReasoningMax: 64000},
	"claude-3-7-sonnet": {ContextWindow: 200000, MaxOutputTokens: 8192, ReasoningMax: 64000},
	"claude-3-5-sonnet": {ContextWindow: 200000, MaxOutputTokens: 8192, ReasoningMax: 8192},
	"claude-haiku-4-5":  {ContextWindow: 200000, MaxOutputTokens: 8192, ReasoningMax: 64000},

	// OpenAI / Codex 系列
	"gpt-6-luna":    {ContextWindow: 272000, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"gpt-6-astra":   {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"gpt-6-sol":     {ContextWindow: 272000, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"gpt-5.6-luna":  {ContextWindow: 372000, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"gpt-5.6-sol":   {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"gpt-5.6-terra": {ContextWindow: 372000, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"gpt-5.5":       {ContextWindow: 272000, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"o1":            {ContextWindow: 200000, MaxOutputTokens: 65536, ReasoningMax: 100000},
	"o3-mini":       {ContextWindow: 200000, MaxOutputTokens: 65536, ReasoningMax: 100000},
	"gpt-4o":        {ContextWindow: 128000, MaxOutputTokens: 16384, ReasoningMax: 16384},
	"gpt-4o-mini":   {ContextWindow: 128000, MaxOutputTokens: 16384, ReasoningMax: 16384},

	// Gemini 系列
	"gemini-3.8-flash":      {ContextWindow: 1048576, MaxOutputTokens: 8192, ReasoningMax: 65536},
	"gemini-3.8-flash-high": {ContextWindow: 1048576, MaxOutputTokens: 8192, ReasoningMax: 65536},
	"gemini-3.7-flash-high": {ContextWindow: 1048576, MaxOutputTokens: 8192, ReasoningMax: 65536},
	"gemini-2.5-pro":        {ContextWindow: 1048576, MaxOutputTokens: 8192, ReasoningMax: 65536},
	"gemini-2.5-flash":      {ContextWindow: 1048576, MaxOutputTokens: 8192, ReasoningMax: 65536},

	// 腾讯 Hunyuan 系列
	"hy3": {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
}

// resolveOfficialModelSpec 根据模型名称解析其官方规格，支持大小写归一化、前缀剥离与模糊家族匹配。
// [参数] model: 模型 ID 或展示名
// [返回] 匹配到的官方规格；未收录时返回通用兜底规格
// 最近修改时间：2026-10-13 02:00:00；改动原因：按模型官方规格自适应。
func resolveOfficialModelSpec(model string) ModelOfficialSpec {
	norm := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.Index(norm, "/"); idx >= 0 {
		norm = norm[idx+1:]
	}
	if spec, ok := officialModelSpecs[norm]; ok {
		return spec
	}
	for k, spec := range officialModelSpecs {
		if strings.HasPrefix(norm, k) || strings.HasPrefix(k, norm) {
			return spec
		}
	}
	return ModelOfficialSpec{
		ContextWindow:   131072,
		MaxOutputTokens: 8192,
		ReasoningMax:    32768,
	}
}

// computeAdaptiveMaxTokens 根据模型官方规格和推理模式，自适应计算发往上游的最佳 max_tokens。
// [参数] model: 请求模型名称；requested: 客户端请求传入的 token 数（<=0 表示未传）；isReasoning: 是否处于思考/推理模式
// [返回] 放宽且安全的最终 max_tokens 上限
// 最近修改时间：2026-10-13 02:00:00；改动原因：按模型官方规格自适应计算 max_tokens。
func computeAdaptiveMaxTokens(model string, requested int64, isReasoning bool) int64 {
	spec := resolveOfficialModelSpec(model)
	targetMax := spec.MaxOutputTokens
	if isReasoning && spec.ReasoningMax > targetMax {
		targetMax = spec.ReasoningMax
	}
	if targetMax <= 0 {
		targetMax = 8192
	}

	// 1. 客户端未传入：直接补足为该模式下的官方最大推荐值
	if requested <= 0 {
		return applyMaxTokensFloor(targetMax)
	}

	// 2. 思考模式下，若客户端传入保守过小值（<=8192），且官方推理上限更大，自动放宽破除死锁
	if isReasoning && requested <= 8192 && targetMax > 8192 {
		return applyMaxTokensFloor(targetMax)
	}

	// 3. 客户端显式传入值超过了官方允许的最大上限，截断至上限避免被上游网关报 400
	if requested > targetMax {
		return applyMaxTokensFloor(targetMax)
	}

	// 4. 处于合理区间内，原样保留客户端传入值（最终不低于统一输出底线）
	return applyMaxTokensFloor(requested)
}

func jsonI64(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int64(t)
			case int64:
				return t
			}
		}
	}
	return 0
}
