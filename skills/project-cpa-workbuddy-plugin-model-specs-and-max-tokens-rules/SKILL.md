---
name: project-cpa-workbuddy-plugin-model-specs-and-max-tokens-rules
description: 当开发、接入新 AI 服务商插件（如 workbuddy、qoder-ai、qoderwork、traework、zcode、cursor）、新增大模型接入、或排查「模型推理卡死 50~60 秒然后断开/失败」、「长思考链模型输出被截断（finish_reason=length 且正文为空）」、「客户端写死 max_output_tokens=8192 导致思维链吃满配额」、以及「上游报 400 Bad Request: max_tokens exceeds model limit」时强制自动触发。负责落实按模型权威官方规格自适应计算与放宽 max_tokens 机制，坚决杜绝盲目硬编码 8192 与无差别填 128k 两种极端反模式。
---

# cpa-workbuddy-plugin 大模型官方规格自适应与 max_tokens 放宽规范

## 1. 适用范围与触发信号

- **新插件/新模型接入**：在仓库下新增或维护任何 provider 插件（如 `workbuddy`、`workbuddy-ai`、`qoderwork`、`qoder-ai`、`traework`、`zcode`、`cursor` 等）的请求体处理（`payload.go` / `upstream.go`）与模型目录元数据（`models.go`）；
- **故障排查信号**：
  - 用户反馈长上下文任务中「推理进行到 50~60 秒突然报错或断开」；
  - 客户端报错「工具调用失败」或「生成内容为空」；
  - new-api 消费日志出现 `content: "上游没有返回计费信息，无法扣费"` 且 `completion_tokens: 0`；
  - token-usage 底层记录显示 `output_tokens: 8192, reasoning_tokens: 8192`（即输出全部被思维链耗尽，正文 0 token）；
  - 上游网关返回 `HTTP 400: max_tokens exceeds model limit` 或参数不合法。

---

## 2. 根因机理与三大极端反模式

### 2.1 思考模型（Reasoning Models）的 Token 吞噬机制
- 在 `deepseek-v4.1-flash`、`deepseek-reasoner`、`claude-3-7-sonnet (Thinking)`、`o1`、`kimi-k2.6`、`glm-5.3` 等支持深度思考的模型中，**思维链产生的 `reasoning_content` 会计入上游的单次输出总量（`output_tokens`）**；
- 当上下文庞大（如 30~50 万 tokens）且思考强度为 `high` 时，模型思考链会产生数千乃至上万 tokens；
- 若请求中限定了较小的 `max_tokens`（如 8192），上游在生成完第 8192 个思考 token 时就会因触顶被 `finish_reason: "length"` 强行截断，**导致正文输出一个字都来不及吐出**，下游客户端直接判定失败。

### 2.2 必须坚决避免的三大反模式

1. **反模式一：盲目硬编码 `8192`（客户端与早期插件通病）**
   - 典型表现：Qoder Desktop 客户端在前端代码中将所有自定义模型写死 `max_output_tokens: 8192`；部分早期插件静态列表全部写死 `8192`。
   - 后果：大模型在长思考下必然触顶截断，造成长卡顿后彻底失败。
2. **反模式二：无差别填成 `128,000`**
   - 典型表现：对所有模型不分青红皂白一律改写为 128k。
   - 后果：部分严格校验入参的上游网关（如原生 OpenAI 接口、部分聚合路由）会直接返回 `400 Bad Request: max_tokens must be <= 8192`，导致请求连首字节都无法产生。
3. **反模式三：插件层完全不干涉原样透传**
   - 典型表现：客户端传什么就发给上游什么。
   - 后果：上游原本支持 64k/128k 的大模型，被客户端自带的保守值（8192）给活活锁死。

---

## 3. 核心机制：按模型权威官方规格自适应收敛（Adaptive Max Tokens）

所有插件必须遵守 **「官方规格驱动 + 推理模式识别 + 安全区间收敛」** 算法。

### 3.1 权威规格表维护（`model_specs.go`）
每个插件必须维护一份官方规格表（见 `references/model-specs-matrix.md`），包含三个维度：
- `ContextWindow`: 上下文总长度（如 1,048,576）
- `MaxOutputTokens`: 官方常规最大输出 token（如 16,384）
- `ReasoningMax`: 官方思维链/推理模式下的最大输出 token（如 64,000 或 128,000）

### 3.2 自适应四步收敛法（`computeAdaptiveMaxTokens`）

```
                     客户端输入 requestedTokens
                               │
               ┌───────────────┴───────────────┐
               ▼                               ▼
       未传入（<= 0）                     显式传入（> 0）
               │                               │
               │                               ▼
               │                是否思考模式 且 requested <= 8192
               │                且 官方 ReasoningMax > 8192 ?
               │                     /               \
               │                  是/                 \否
               │                   ▼                   ▼
               │         提升至该模型官方 ReasoningMax  是否超出官方上限
               │         (如 DeepSeek 64k, GLM 128k)  requested > targetMax ?
               │                                       /       \
               │                                    是/         \否
               ▼                                     ▼           ▼
        补足为官方目标上限                      截断至官方上限   保留原值
      (TargetMax: 常规或推理)                 (Safety Clamp)
```

1. **未传参数补足**：客户端未传入 `max_tokens` 时，自动补足为该模式下的官方最大推荐值；
2. **破除客户端保守死锁**：处于思考/推理模式时，若客户端传入保守过小值（`<= 8192`），且官方推理上限更大，**自动提升至该模型官方推理上限（如 DeepSeek 提升至 64k，GLM 提升至 128k，Claude 提升至 64k）**；
3. **防止上游 400 截断（Safety Clamp）**：若客户端传入数值超过了该模型官方允许的最大上限，自动截断收敛到官方上限，避免上游网关报错 400；
4. **合理区间原样保留**：客户端在合法区间内指定的数值，原样尊重并保留。

### 3.3 全模型统一最低输出底线（`minMaxTokensFloor = 32768`）

在自适应收敛的**所有出口之后**，必须再套一层统一最低输出底线，保证任何路径（含官方规格本身 `< 32768` 的模型、兜底 fallback 规格、广播层元数据）都不会对外给出低于 32768 的输出上限。

```go
// minMaxTokensFloor 是所有模型对外输出的统一最低 max_tokens 底线。
const minMaxTokensFloor int64 = 32768

// applyMaxTokensFloor 将低于统一底线的输出上限抬升至底线。
func applyMaxTokensFloor(v int64) int64 {
	if v < minMaxTokensFloor {
		return minMaxTokensFloor
	}
	return v
}
```

强制契约：
1. **计算层**：`computeAdaptiveMaxTokens` 的**四个出口**（未传补足 / 保守放宽 / 超限 clamp / 区间保留）返回前**全部**经 `applyMaxTokensFloor` 收敛；
2. **兜底规格**：`lookupOfficialModelSpec` 的 fallback `ReasoningMax` 必须对齐 32768（禁止沿用历史 8192）；
3. **广播层**：`models.go` 的静态列表、动态发现、配置覆盖路径的 `MaxCompletionTokens` 一律经 `applyMaxTokensFloor` 保底；
4. **请求体层（payload 层）**：如 traework `upstream.go`，`streamDefaultMaxTokens` 与 emit 点 `payload["max_tokens"]` 必须经 floor 抬升；
5. **探活负载**：测试探活的 `max_tokens` 同样经 floor 抬升，避免过小上限被上游业务拒绝。
6. **六插件同构**：`minMaxTokensFloor` / `applyMaxTokensFloor` 在 workbuddy / workbuddy-ai / qoder-ai / qoderwork / traework / zcode 六插件中同名同义、逐字节同源。

---

## 4. 插件双层实施规范（标准落地模板）

新插件或存量插件改造时，必须在以下两个位置同时落地：

### 4.1 第一层：请求体重写层（`payload.go` / `upstream.go`）
在序列化发送给上游网关前调用，作为请求预处理的必备环节：

```go
func relaxMaxTokensInPlace(obj map[string]any) bool {
	model := jsonStr(obj, "model")
	effort := strings.ToLower(strings.TrimSpace(jsonStr(obj, "reasoning_effort")))
	isReasoning := effort != "" && effort != "none"
	if !isReasoning {
		mLower := strings.ToLower(model)
		if strings.Contains(mLower, "reasoner") || strings.Contains(mLower, "thinking") || strings.HasPrefix(mLower, "o1") || strings.HasPrefix(mLower, "o3") {
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
```

### 4.2 第二层：模型目录元数据层（`models.go`）
在向宿主暴露模型列表（`MethodModelStatic` / `MethodModelForAuth`）时：
- 对静态定义模型：必须按照官方规格填写真实 `MaxCompletionTokens`（禁止全量写死 8192）；
- 对动态拉取模型：若上游返回的 `maxOutputTokens` 为空或较小，必须用 `resolveOfficialModelSpec(model).ReasoningMax` 予以保底，确保对外广播的元数据具备充足输出上限。

---

## 5. 验收与回归标准

所有涉及模型规格或 token 处理的改动收口前，必须在 `max_tokens_test.go` 中完成以下 7 维单元测试回归：
1. **思考模式 + 8192**：断言提升至该模型官方推理上限（如 DeepSeek 64k）；
2. **常规模式 + 未传**：断言补足为官方常规上限（如 16k）；
3. **常规模式 + 8192**：断言保持原值（且不低于 32768 底线；官方上限低于 32768 的模型断言抬升至 32768）；
4. **超限传值**：断言安全截断至官方允许最大值（不超纲）；
5. **双字段归一化**：断言 `max_completion_tokens` 正确提升并清除冲突冗余键；
6. **请求体序列化端到端验证**：断言 `prepareUpstreamBody` 输出符合预期；
7. **统一底线**：断言任一出口（含兜底规格与广播层）均不低于 32768。
