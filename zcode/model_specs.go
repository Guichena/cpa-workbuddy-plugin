// model_specs.go 维护 ZCode 插件权威模型规格表与自适应 max_tokens 计算。
package main

import (
	"fmt"
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

// ModelOfficialSpec 定义模型官方规格参数。
type ModelOfficialSpec struct {
	ContextWindow   int64 // 官方最大上下文长度
	MaxOutputTokens int64 // 官方常规最大输出 token
	ReasoningMax    int64 // 官方思维链/推理模式下的最大输出 token
}

// 常见主流模型的权威官方规格表（与其余五个 provider 插件同源，覆盖 ZCode 主用的 GLM 家族与通用家族）。
var officialModelSpecs = map[string]ModelOfficialSpec{
	// Qoder/阿里上游代号别名
	"qfmodel":       {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"qmodel_38max":  {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"qmodel_latest": {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"qmodel":        {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"cmodel":        {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"gmodel":        {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"gfmodel":       {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"dmodel":        {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"dfmodel":       {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"mmodel":        {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"kmodel_latest": {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},
	"kmodel":        {ContextWindow: 1000000, MaxOutputTokens: 16384, ReasoningMax: 32768},

	// GLM 智谱家族（ZCode 主用）
	"glm-5.3":       {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"glm-5.3-flash": {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 128000},
	"glm-5.2":       {ContextWindow: 200000, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"glm-4-plus":    {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 16384},
	"glm-4-air":     {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 16384},
	"glm-4-flash":   {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 16384},
	"glm-4.5":       {ContextWindow: 131072, MaxOutputTokens: 16384, ReasoningMax: 64000},

	// DeepSeek 家族
	"deepseek-v4.1-flash": {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"deepseek-v4-flash":   {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"deepseek-v4-pro":     {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
	"deepseek-chat":       {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 8192},
	"deepseek-reasoner":   {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 64000},

	// Kimi 家族
	"kimi-k2.6":         {ContextWindow: 262144, MaxOutputTokens: 16384, ReasoningMax: 65536},
	"kimi-k2.8":         {ContextWindow: 262144, MaxOutputTokens: 16384, ReasoningMax: 65536},
	"kimi-k2.8-preview": {ContextWindow: 262144, MaxOutputTokens: 16384, ReasoningMax: 65536},
	"kimi-k3":           {ContextWindow: 262144, MaxOutputTokens: 16384, ReasoningMax: 65536},
	"kimi-k2":           {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 32768},
	"kimi-k2-thinking":  {ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 32768},

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

// resolveOfficialModelSpec 根据模型名称解析官方规格。
// [参数] model: 模型 ID 或展示名（支持大小写归一化与 `vendor/` 前缀剥离）
// [返回] 命中的官方规格；未收录时返回通用兜底规格
// 最近修改时间：2026-10-13 14:00:00；改动原因：全扩为通用家族规格表并收紧前缀匹配。
func resolveOfficialModelSpec(model string) ModelOfficialSpec {
	m := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.Index(m, "/"); idx >= 0 {
		m = m[idx+1:]
	}
	if m == "" {
		return ModelOfficialSpec{ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 32768}
	}
	// 1. 精确命中
	if spec, ok := officialModelSpecs[m]; ok {
		return spec
	}
	// 2. 双向前缀命中并取最长键，避免短键遮蔽更长变体
	var bestKey string
	var bestSpec ModelOfficialSpec
	for k, spec := range officialModelSpecs {
		if (strings.HasPrefix(m, k) || strings.HasPrefix(k, m)) && len(k) > len(bestKey) {
			bestKey, bestSpec = k, spec
		}
	}
	if bestKey != "" {
		return bestSpec
	}
	// 3. 家族泛化兜底
	switch {
	case strings.Contains(m, "glm-5"):
		return ModelOfficialSpec{ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 128000}
	case strings.Contains(m, "glm-4.5"), strings.Contains(m, "glm-4.6"), strings.Contains(m, "glm-4.7"):
		return ModelOfficialSpec{ContextWindow: 131072, MaxOutputTokens: 16384, ReasoningMax: 64000}
	case strings.Contains(m, "glm-4"):
		return ModelOfficialSpec{ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 16384}
	case strings.Contains(m, "reasoner") || strings.Contains(m, "thinking"):
		return ModelOfficialSpec{ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 64000}
	default:
		return ModelOfficialSpec{ContextWindow: 131072, MaxOutputTokens: 8192, ReasoningMax: 32768}
	}
}

// computeAdaptiveMaxTokens 核心自适应计算算法：
// 1. 未传或 <= 0：自适应补齐为官方常规最大输出 token (如果是推理模型则为 ReasoningMax)。
// 2. 推理模型且客户端保守传入 <= 8192：自适应放宽至官方 ReasoningMax。
// 3. 超出官方上限：截断至官方支持的上限。
// 4. 处于合法区间内：保留客户端原值。
func computeAdaptiveMaxTokens(model string, requestedTokens int64, isReasoning bool) int64 {
	spec := resolveOfficialModelSpec(model)
	targetCap := spec.MaxOutputTokens
	if isReasoning && spec.ReasoningMax > targetCap {
		targetCap = spec.ReasoningMax
	}
	if targetCap <= 0 {
		targetCap = 8192
	}

	if requestedTokens <= 0 {
		return applyMaxTokensFloor(targetCap)
	}

	if isReasoning && requestedTokens <= 8192 && targetCap > requestedTokens {
		return applyMaxTokensFloor(targetCap)
	}

	if requestedTokens > targetCap {
		return applyMaxTokensFloor(targetCap)
	}

	return applyMaxTokensFloor(requestedTokens)
}

// relaxMaxTokensInPlace 对传入的 JSON payload map 执行原地自适应 max_tokens 优化。
func relaxMaxTokensInPlace(obj map[string]any) bool {
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
		if _, hasMCT := obj["max_completion_tokens"]; hasMCT {
			delete(obj, "max_completion_tokens")
		}
		return true
	}
	return false
}

func jsonI64(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return int64(t)
			case int64:
				return t
			case string:
				var n int64
				fmt.Sscanf(t, "%d", &n)
				return n
			}
		}
	}
	return 0
}

func jsonStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}
