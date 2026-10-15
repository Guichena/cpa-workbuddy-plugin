# deepseek-v4.1-flash 流式请求零计费根因诊断（思维链吃满上限 + incomplete 计费路径缺失）

- 发现时间：2026-10-15 16:45（用户报告 new-api 使用日志中 `dc19480f` 会话报错）
- 报告人：luode
- 复现请求：`202610150842299783500368268d9d6FE9Bn0ps`（16:42:29 发起，16:45:41 结束）
- 影响模型：`deepseek-v4.1-flash`（workbuddy-ai-provider）
- 严重级别：中（不丢数据，但零计费 + 错误文案误导，单请求占用连接 3 分钟）

---

## 一、现象

new-api 使用日志（2026-10-15 16:45:41）：

| 字段 | 值 |
|---|---|
| 模型 | `deepseek-v4.1-flash` |
| 渠道 | #4（混合） |
| 流 | 是 |
| Tokens | `-`（无） |
| 费用 | `$0` |
| 首字 | 2.3s |
| 耗时 | 3m 12s |
| 内容 | `上游没有返回计费信息，无法扣费（可能是上游超时）` |

**关键反直觉点**：`首字 2.3s` 说明首字节**正常到达**，因此**不是**首字静默窗口问题（与 2026-10-13 gemini 那次形态完全不同，详见第四节）。

---

## 二、证据链（三层，逐层闭合）

### 2.1 new-api 侧（判定与结果）

```
[INFO] 16:42:30 | 202610150842299783500368268d9d6FE9Bn0ps | 用户 1 额度充足, 信任且不需要预扣费
[ERR]  16:45:41 | 202610150842299783500368268d9d6FE9Bn0ps | total tokens is 0, cannot consume quota, model deepseek-v4.1-flash
[INFO] 16:45:41 | 202610150842299783500368268d9d6FE9Bn0ps | stream ended: reason=eof
[INFO] 16:45:41 | ... | record consume log: params={...,"prompt_tokens":0,"completion_tokens":0,"quota":0,
       "content":"上游没有返回计费信息，无法扣费（可能是上游超时）",...,"use_time_seconds":192,
       ...,"frt":2334,...,"stream_status":{"end_reason":"eof","status":"ok"}}
[GIN]  16:45:41 | relay | ... | 200 | 3m11.239453329s | POST /v1/responses
```

- `frt=2334`：首字 2.3 秒（与面板一致）——**首字节已产生**。
- `end_reason=eof` + `status=ok`：new-api 认为流是**正常收口**的，**不是** `client_gone`。
- `prompt_tokens=0`：**连 prompt 都没拿到**，说明 new-api 侧的 usage 对象全程为空。
- 计费判定：`service/text_quota.go:75` 的 `hasBillableUsage()` 为 `TotalTokens > 0 || ToolCallSurchargeQuota != 0`，此处两者皆 0 ⇒ 走 `:443-445` 的「无法扣费」分支。

### 2.2 CPA 侧（链路与耗时）

CPA 容器 `cli-proxy-api` 中该请求的 traceId 为 `2feb298a`（16:42:30 同秒、同会话 `pck:dc19`、同模型），**全生命周期只有两行日志**：

```
[16:42:30] [2feb298a] [info] [selector.go:1063] session-affinity: cache hit | session=pck:dc19... auth=workbuddyai-b7e4db75-...json provider=mixed model=deepseek-v4.1-flash
[16:45:41] [2feb298a] [info] [gin_logger.go:108] 200 | 3m11s | 127.0.0.1 | POST "/v1/responses"
```

- 中间 **191 秒完全静默**（无 warn / error / 重试 / 换号）。
- 全窗口 `upstream execution failed` 计数 = **0**。
- 出口 `200 | 3m11s`：**CPA 最终回的是 200**，即 CPA 判定本次请求成功。

### 2.3 插件侧（决定性物证）

用量 feed（`/CLIProxyAPI/data/token-usage-feed.ndjson`）中该请求的原始记录：

```json
{"alias":"deepseek-v4.1-flash","auth_index":"b7e4db75-...","executor_type":"workbuddy-ai",
 "failed":false,"latency_ms":190880,"model":"deepseek-v4.1-flash","provider":"workbuddy-ai-provider",
 "reasoning_effort":"high","status_code":0,
 "timestamp":"2026-10-15T08:42:30.244408124Z",
 "tokens":{"input_tokens":114345,"output_tokens":64000,"reasoning_tokens":64000,"total_tokens":178345},
 "ttft_ns":2062881341}
```

**三个决定性数字**：

1. `failed=false`、`status_code=0` ⇒ **插件判定成功**，正常上报用量。
2. `output_tokens = 64000` **且** `reasoning_tokens = 64000` ⇒ **正文 token = 64000 − 64000 = 0**（判据见项目记忆 `reference-inference-chain-observability`：正文 = `output_tokens − reasoning_tokens`）。
3. `64000` **精确等于**本仓库 `workbuddy-ai/model_specs.go:44` 为 `deepseek-v4.1-flash` 声明的 `ReasoningMax`：

```go
"deepseek-v4.1-flash": {ContextWindow: 1048576, MaxOutputTokens: 16384, ReasoningMax: 64000},
```

⇒ **思维链恰好吃满我们放宽后的上限**，触发上游 `finish_reason: "length"` 强行截断，正文一字符未吐。

### 2.4 全量指纹（非孤例，7/7 同形）

feed 中 `workbuddy-ai-provider` 全部 5090 条记录里，「`output_tokens > 0 且 output_tokens == reasoning_tokens`」（即正文为 0）共 **7 条**，**全部**满足：

| 时间(UTC) | 模型 | latency_ms | failed | status | effort |
|---|---|---|---|---|---|
| 2026-10-14T12:48:11 | deepseek-v4.1-flash | 180967 | false | 0 | high |
| 2026-10-14T15:06:13 | deepseek-v4.1-flash | 187612 | false | 0 | high |
| 2026-10-14T17:48:38 | deepseek-v4.1-flash | 183621 | false | 0 | high |
| 2026-10-14T18:13:07 | deepseek-v4.1-flash | 199882 | false | 0 | high |
| **2026-10-15T08:42:30** | deepseek-v4.1-flash | 190880 | false | 0 | high |
| 2026-10-15T08:46:33 | deepseek-v4.1-flash | 192464 | false | 0 | high |
| 2026-10-15T08:49:19 | deepseek-v4.1-flash | 198596 | false | 0 | high |

- **7/7 均为 `deepseek-v4.1-flash` + `reasoning_effort=high` + `output==reasoning==64000`**。
- 耗时集中在 **180~200 秒**（对照：正常请求 3~20 秒）。
- 与 new-api 侧对账：近 12h「无法扣费」30 条中，模型分布为 `gemini-3.8-flash` 21 / `qwen-3.8-flash` 6 / `deepseek-v4.1-flash` **3** —— 这 3 条精确对应上表 10-15 的 3 条（10-14 的 4 条已出 12h 窗口）。

---

## 三、根因（两层，缺一不可）

### 3.1 触发层：思考链无节制膨胀，吃满放宽后的上限

链路：`Qoder 客户端 → new-api → CPA → workbuddy-ai 插件 → 腾讯 CodeBuddy 上游`。

1. Qoder 客户端对所有自定义模型写死 `max_output_tokens: 8192`（历史事实，见项目记忆 `reference-reasoning-token-truncation`）。
2. CPA 转译为 Chat Completions 的 `max_tokens: 8192`。
3. 插件 `relaxMaxTokensInPlace`（`workbuddy-ai/payload.go:493`）经 `computeAdaptiveMaxTokens` 自适应放宽至 `deepseek-v4.1-flash` 的 `ReasoningMax = 64000`。
4. 上游在 `high` 思考 + **超大上下文**（本次 `input_tokens = 114345`，同族故障曾达 36 万）下，思维链持续膨胀直至吃满 64000，触发 `finish_reason: "length"`，**正文零字符**。

> **重要反思：本次放宽是有害的。** 放宽把截断点从 8192 推到 64000，**故障本身没有消除，只把单次故障耗时从约 50 秒拉长到 190 秒**（对照 10-12 未放宽时的同类记录：`use_time_seconds: 50~57`）。放宽仅对「思考量正常但客户端上限过保守」的模型（如 GLM 需 128k）有益；对「思考无节制」场景，放宽等于放大故障时长与连接占用。

### 3.2 计费层：new-api 在 `response.incomplete` 分支丢弃 usage

上游 `finish_reason=length` 经 CPA 转换器映射为 **`response.incomplete`** 事件（`CLIProxyAPI/internal/translator/openai/openai/responses/openai_openai-responses_response.go:76-90`）：

```go
func incompleteByFinishReason(reason string) ([]byte, bool) {
    switch reason {
    case "length", "max_tokens":
        return []byte(`{"reason":"max_output_tokens"}`), true
    ...
}
// buildResponsesCompletedEvent:
eventType := "response.completed"
if isIncomplete { eventType = "response.incomplete"; status = "incomplete" }
...
if st.UsageSeen {                      // ← usage 照常挂载，与事件类型无关
    completed, _ = sjson.SetBytes(completed, "response.usage.input_tokens", st.PromptTokens)
    completed, _ = sjson.SetBytes(completed, "response.usage.output_tokens", st.CompletionTokens)
    ...
}
```

即 **CPA 发出的 `response.incomplete` 帧里是带 usage 的**（`UsageSeen=true`，因为上游末尾 chunk 携带 usage，插件原样透传）。

而 new-api 的 Responses 流处理器（`new-api/relay/channel/openai/relay_responses.go`）**只在 `response.completed` / `response.done` 分支提取 usage**：

```go
switch streamResponse.Type {
case "response.completed", "response.done":
    if streamResponse.Response != nil {
        if streamResponse.Response.Usage != nil {          // ← 仅此分支取 usage
            incomingUsage := relayconvert.NormalizeResponsesUsage(streamResponse.Response.Usage)
            usage = dto.MergeUsageNonZero(usage, incomingUsage)
        }
        ...
    }
case "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
    if !imageCommitted {                                   // ← 只重置图片计数，完全不读 usage
        imageCounter.Reset()
        imageCounter.Commit(info)
        imageCommitted = true
    }
...
}
```

⇒ **`response.incomplete` 携带的 usage 被静默丢弃**，`usage` 全程为 `&dto.Usage{}`，最终 `TotalTokens=0` ⇒ `hasBillableUsage()` 为 false ⇒ 零计费 + 误导文案「上游超时」。

> 佐证：new-api 在另一条路径（`relay/channel/openai/chat_via_responses.go:91-98`）**是**处理 `response.incomplete` 并取其 `Response` 的，说明该分支遗漏属 Responses 直连路径的实现缺口，而非有意设计。

### 3.3 根因一句话

**思考模型在超大上下文 + `high` 强度下思维链吃满本插件放宽后的 64000 上限，上游以 `finish_reason=length` 截断致正文为零；CPA 如实转出带 usage 的 `response.incomplete`，但 new-api 的 Responses 直连路径只在 `response.completed` 取 usage，于是计费被丢弃 —— 用户侧表现为「耗时 3 分钟、$0、报上游超时」。**

---

## 四、与 2026-10-13 gemini 那次的区别（**严禁并案**）

| 维度 | 2026-10-13 gemini-3.8-flash | 本次 deepseek-v4.1-flash |
|---|---|---|
| `frt` | **-1000**（首字节从未产生） | **2334**（首字节 2.3s 正常到达） |
| `end_reason` | `client_gone`（调用方先走） | `eof`（流正常收口） |
| 耗时 | 40~110s | 180~200s |
| 故障段 | 「调用方 ↔ new-api」首字静默窗口 | 「上游生成阶段」思考吃满 + 计费路径 |
| 根因 | 无心跳，调用方超时先断 | 思维链截断 + `incomplete` 分支漏取 usage |
| 处置 | new-api 开 `ping_interval_enabled`（已实施） | 见第五节 |

**结论：两次是两类独立故障，前者是「连接层静默」，后者是「生成层截断 + 计费层遗漏」。**

---

## 五、修复方案（按推荐度排序）

> 关键约束：本仓库（`F:/cpa-plugin`）**无权修改** `F:/new-api` 与 `F:/CLIProxyAPI`（兄弟项目，只读红线，不存在「取得授权即可执行」的例外）。以下方案给出落点，供在对应项目新开会话执行。

### 方案 A（**评估后放弃**，2026-10-15 四轮裁定）—— 插件侧「正文为空的 length 截断」降级重试

**初稿思路**：
识别到上游流正常结束、末帧 `finish_reason == "length"`、正文 token 为 0 时，重置 `reasoning_effort=none` 关思考重试一次。

**放弃原因（经客户端持久化与会话逆向、业界调研双重推翻）**：
1. **对话历史污染不可避免**：逆向 Qoder 客户端 SDK（`_worker/qoder-worker-runtime.obf.mjs`）与检索 `~/.qoder/projects/F--cpa-plugin/*.jsonl` 证实，客户端构建 reasoning 项的门禁 `zfi(A)` 是 OR 条件（`summary` 文本非空即可放行，不需要 `encrypted_content`），且 `preserve` 默认为 true。若首轮透传思考，吃满的 64000 个废弃思考会作为 `reasoning_item` 强行落盘，并在下轮会话作为输入带给模型，造成巨额上下文污染。
2. **代理层持有抑制（方案 A1）成本不可承受**：若为了不污染历史而在插件内存中暂扣首轮思考（等正文出来再决定补发还是丢弃），首轮吃满 64k token 需耗时 3~5 分钟，重试再耗时 1~2 分钟，累计耗时达 5~6 分钟，不仅首屏完全失去思考渐进流，且几乎 100% 触发上层 Nginx 或客户端 HTTP read timeout；同时并发下有内存击穿与客户端 `inconsistent_reasoning` 协议冲突风险。
3. **违背业界通用规范**：深入检索 LiteLLM、OpenRouter、One-API、Cline、Aider 开源实现证实，没有任何成熟代理网关会在流式过程中擅自做“关思考重试”拦截，全部遵循原样透传。

**结论**：方案 A 代理层黑魔法弊大于利，正式放弃，保持插件代码零改动。

### 方案 B（**最终采纳 · 业界标准方案**）—— 修复 New-API `relay_responses.go` 的 incomplete 计费与状态透传

**落点**：`F:/new-api/relay/channel/openai/relay_responses.go:115`（需在 `F:/new-api` 独立会话中执行）。

**改动**：在 `case "response.failed", "response.incomplete", ...` 分支中，补齐 `response.incomplete` 的 usage 提取与状态规范：

```go
<<<<<<< SEARCH
		case "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
=======
		case "response.completed", "response.done", "response.incomplete":
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					incomingUsage := relayconvert.NormalizeResponsesUsage(streamResponse.Response.Usage)
					usage = dto.MergeUsageNonZero(usage, incomingUsage)
				}
				if !imageCommitted {
					if relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) {
						imageCounter.Reset()
						imageCounter.Commit(info)
						imageCommitted = true
					} else {
						for i := range streamResponse.Response.Output {
							idx := i
							imageCounter.Observe(&streamResponse.Response.Output[i], &idx)
						}
						imageCounter.Commit(info)
						imageCommitted = true
					}
				}
			} else if !imageCommitted {
				imageCounter.Commit(info)
				imageCommitted = true
			}
		case "response.failed", "response.cancelled", "response.canceled":
			if streamResponse.Response != nil && streamResponse.Response.Usage != nil {
				incomingUsage := relayconvert.NormalizeResponsesUsage(streamResponse.Response.Usage)
				usage = dto.MergeUsageNonZero(usage, incomingUsage)
			}
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
>>>>>>> REPLACE
```

**依据与收益**：
1. OpenAI 官方规范明文规定：思考消耗光 `max_completion_tokens` 时，返回 `finish_reason: "length"` 且 content 为空是**符合协议预期的正常终态**，不是系统错误。
2. 修复后消除 `$0` 与误导性的“上游超时”，准确计量消耗的 64000 个思考 tokens，网关状态与 OpenAI/LiteLLM 官方对齐。
3. 客户端能正常展示完整的思考全貌，截断由客户端/用户按需点击“继续”自然闭环，零协议与时序风险。

### 方案 C（**已作废**，2026-10-15 用户明确约束）—— 回调 `deepseek-v4.1-flash` 的 `ReasoningMax`

**用户当轮明确要求：不能把最大输出改小。** 本方案（下调 `workbuddy-ai/model_specs.go:44` 的 `ReasoningMax`）**据此作废**，不再作为候选。

> 记录该约束的技术后果，供后续决策参考：`ReasoningMax` 是「思考+正文」共享的上限，改小它虽然能把单次故障耗时从 ~190s 压回 ~50s，但会同时**削掉合法长输出的能力**（`MaxOutputTokens` 目前仅 16384，正文本身也需要空间）。在「思考吃满 64000」这一根因未被约束前，上限只是一个可调的截断点 —— **改小是止痛，不是治病**，且会引入新的能力退化。用户选择不改，方向正确。

### 方案 D（**已实测，结论：不支持**）—— 探测上游是否支持 thinking budget 参数

**探测时间**：2026-10-15（只读，未写生产；16 个可用账号轮换 + 限速，避免触发上游 429）。
**探测端点**：`https://www.workbuddy.ai/v2/chat/completions`（插件真实上游，见 `workbuddy-ai/main.go:85`）。
**探测请求**：`model=deepseek-v4.1-flash`、`stream=true`、`reasoning_effort=high`、首条为 system message（缺 system 会被上游 `code 11128` 拦截）。

#### D.1 参数注入结果：**全部被静默忽略**

| 注入参数（值均取 512~1024，远小于上限） | 实测 `reasoning_tokens` | 判定 |
|---|---|---|
| （基线，仅 `reasoning_effort=high`） | 2048（= `max_tokens`，吃满） | — |
| `thinking: {type:"enabled", budget_tokens:1024}` | 2048 | **忽略** |
| `thinking_budget: 1024` | 2048 | **忽略** |
| `max_thinking_tokens: 512` | 4096 | **忽略** |
| `reasoning_budget: 512` | 4096 | **忽略** |
| `thinking: {budget: 512}` | 4096 | **忽略** |
| `reasoning: {max_tokens: 512}` | 4097 | **忽略** |
| `budget_tokens: 512`（顶层） | 4096 | **忽略** |
| `reasoning: {effort:"high", budget_tokens:1024}` | 2048 | **忽略** |
| `max_completion_tokens: 1024` | 2047 | **忽略**（且 `max_tokens` 优先） |

**结论：上游完全不识别任何 thinking budget 类参数。** 所有候选注入后 `reasoning_tokens` 恒等于 `max_tokens`（吃满），无一被限制到注入值附近。唯一的 `400` 来自非法 effort 值（`bogus` ⇒ `code 11150 the reasoning effort value is not supported by the current model`），与 budget 无关。

**旁证（上游能力清单）**：`GET /v3/config` 返回的 28 个模型定义中，**`budget` 关键词出现 0 次**；`deepseek-v4.1-flash` 的 reasoning 段为静态 `{"effort":"high","summary":"auto"}`，**既无 `supportedEfforts` 也无 `canDisableThinking`**（对照 `glm-5.3-flash`：`{"canDisableThinking":true,"defaultEffort":"high","supportedEfforts":["low","high","max"]}`）⇒ **该模型在上游根本没有暴露「思考强度」这个可调维度。**

#### D.2 附带发现一：`reasoning_effort` 对 deepseek **只有开/关两档**，且关只有 `none` 一种写法

| `reasoning_effort` | 实测（`max_tokens=2048`） | 判定 |
|---|---|---|
| 字段缺省 | `rt=0`，正文正常 | **关闭** |
| `none` | `rt=0`，正文正常 | **关闭** |
| `low` | `rt=2048` 吃满、正文 0 | **开启** |
| `minimal` | `rt=2048` 吃满、正文 0 | **开启** |
| `high` | `rt=2048` 吃满、正文 0 | **开启** |
| `xhigh` | `rt=2048` 吃满、正文 0 | **开启** |
| `bogus` | `400` `code 11150` | 白名单外 |
| `reasoning: {effort:"none"}` | `rt=0`，正文正常 | **关闭** |

> **对既有注释的重要修正**：`payload.go:423-426` 称「CodeBuddy 只认 `high`，`medium/low/max/xhigh/ultra` 全部 fallback 到无推理」。**该结论对 `deepseek-v4.1-flash` 不成立**：实测 `low`/`minimal`/`xhigh` 与 `high` **表现完全一致**（均吃满上限）。准确表述是「**该模型思考强度不可调，任何非 `none` 值一律按默认档（high）执行**」——这与其能力清单无 `supportedEfforts` 相互印证。该注释对 `hy3` 是否成立未在本轮验证，但其 `if !strings.HasPrefix(model, "hy3") { return false }` 的守卫决定了它**不影响 deepseek**。

#### D.3 附带发现二（**颠覆性**）：`max_tokens` 本身就是思考膨胀的诱导因素

同一 prompt、同一 40k 上下文（`prompt_tokens=32474`）、同一 `high` 档，**只改 `max_tokens`**：

| `max_tokens` | `reasoning_tokens` | 耗时 | 结果 |
|---|---|---|---|
| 32768 | **32768**（吃满） | 174.9s | **正文 0** |
| 64000（3 次复现） | 31254 / 36896 / 38959 | 163~193s | 正文正常 |
| 128000 | **72157** | **363.9s** | 正文正常，但思考近乎翻倍 |

同一诱导任务在小上下文（82 token）下：

| `max_tokens` | `reasoning_tokens` | 结果 |
|---|---|---|
| 2048 | 2048（吃满） | 正文 0 |
| 4096 | 4096（吃满） | 正文 0 |
| 16384 | 16384（吃满） | 正文 0 |
| 64000 | 25894 | **正常**（正文 2586 字符） |

**两点颠覆性结论**：
1. **抬高上限 = 诱导思考更长**：64k → 39k、128k → 72k，耗时从 174s 涨到 364s。上限越大，模型越倾向「把预算用满」。
2. **压低上限 = 更容易吃满**：32k 上限下同一请求**直接吃满 32768、正文 0**（而 64k 下正常）。**改小 `ReasoningMax` 不是止血，而是把故障概率抬高**——这从实测层面印证了用户「不改小最大输出」的判断是正确的。

**⇒ 因此 `max_tokens` 不是一个可用的调节旋钮**：调高诱导膨胀、调低放大截断，两个方向都不可取。

#### D.4 附带发现三：思考长度是**任务依赖**的，且吃满率极低

自然任务（`high` 档、`max_tokens=16384`）：

| 任务 | `reasoning_tokens` | `finish_reason` | 正文 |
|---|---|---|---|
| 写一个斐波那契 Python 函数 | 421 | `stop` | 404 字符 |
| 用三句话说明 MVCC | 245 | `stop` | 180 字符 |
| 「请逐步详细推理并完整写出每一步」（诱导型） | **16384**（吃满） | `length` | **0** |

生产 feed 全量（`deepseek-v4.1-flash`，11110 条）：

| `input_tokens` 桶 | 条数 | `reasoning_tokens` 均值 | `reasoning_tokens` 最大值 | 吃满率 |
|---|---|---|---|---|
| <50k | 2901 | 38 | 20976 | 0% |
| 50-100k | 1724 | 495 | 10199 | 0% |
| 100-200k | 2738 | 1115 | 64000 | 0.18% |
| 200-300k | 2412 | 624 | 14732 | 0% |
| ≥300k | 1335 | 645 | 64000 | 0.23% |

**关键判据**：`reasoning_tokens ≥ 20000` 仅 **9 条**（0.08%），`≥ 40000` 仅 **7 条**（0.06%），且这 7 条**全部**是零正文故障样本。⇒ **吃满上限是低频、任务形态驱动的偶发事件，与上下文规模无强相关**（大上下文吃满率仅 0.18%~0.23%，`<50k` 桶的 `rt` 上限反而只有 20976）。这否定了「按上下文规模预防式预判」的可行性。

#### D.5 方案 D 的最终结论

**上游不支持 thinking budget 参数 ⇒ 方案 D 不可行，回到方案 A（关思考重试），并接受其代价。**

---

## 六、验证判据

1. **方案 A 生效**：feed 中 `provider=workbuddy-ai-provider` 且 `output_tokens == reasoning_tokens > 0` 的记录数显著下降；触发重试的请求应出现 `reasoning_effort=none` 且正文非零。
2. **方案 B 生效**：new-api 近 12h「无法扣费」计数中 `deepseek-v4.1-flash` 归零；`response.incomplete` 请求的 `prompt_tokens` 不再为 0，`quota` 正常。
3. **方案 D 判据（已实测完成）**：注入 `budget_tokens: 8192` 后 `reasoning_tokens` **未被限制**（仍吃满 `max_tokens`）⇒ **不支持**，详见 5.D。
4. **回归样本**：`202610150842299783500368268d9d6FE9Bn0ps`（16:42:29）及同形 7 条（见 2.4 表）。

---

## 七、当前状态

- 诊断：**已完成**，三层证据链闭合（new-api 判定 / CPA 链路 / 插件 feed 物证 + 7/7 全量指纹）。
- 二轮补充证据（2026-10-15）：
  - `reasoning_effort` **上游确实尊重**：`none` 240 条 + `(empty)` 259 条，`reasoning_tokens` **恒为 0**；`high` 5129 条 avg=640.2、max=64000。
  - 7 条零正文分散在 **7 个不同账号**（`795c9f9f`/`726e5612`/`1ad4209d`/`841424fd`/`b7e4db75`/`7a8fc7db`/`367f1c93`）⇒ **排除账号因素**。
  - 大上下文（243k~362k）**大部分成功** ⇒ 无清晰规模阈值，预防式预判不可靠。
- 三轮方案 D 实测（2026-10-15，只读探测，16 账号轮换 + 限速）：
  - **上游不支持任何 thinking budget 参数**（10 个候选全部被静默忽略）⇒ **方案 D 不可行**。
  - 上游能力清单 `GET /v3/config` 中 `budget` 出现 **0 次**；`deepseek-v4.1-flash` 无 `supportedEfforts`、无 `canDisableThinking`。
  - `reasoning_effort` 对 deepseek **只有开/关两档**：`low`/`minimal`/`xhigh` 与 `high` **表现完全一致**（均吃满），仅 `none` 可关闭 ⇒ 修正 `payload.go:423-426` 的既有表述。
  - **`max_tokens` 是思考膨胀的诱导因素**：64k→39k、128k→72k（耗时 174s→364s）；32k 反而吃满。**调高诱导膨胀、调低放大截断，两个方向都不可取** ⇒ 从实测层面印证用户「不改小最大输出」的判断正确。
  - 吃满率极低（`rt ≥ 20000` 仅 9/11110 = 0.08%）且**与上下文规模无强相关**。
- 生产插件版本：`workbuddy-ai-provider-v0.1.15.so`（宿主落盘 `2026-10-14 03:29`；`grep -a` 已确认含 `computeAdaptiveMaxTokens` 符号 ⇒ **自适应引擎确已上线**，本次故障是引擎生效后的新形态，非「未发布」）。
- 用户约束（2026-10-15）：**不得改小最大输出** ⇒ 方案 C 作废。
- 修复：**未实施**（受跨项目只读红线约束，`F:/new-api` 与 `F:/CLIProxyAPI` 仅出方案）。
  - 方案 A（关思考重试）为**唯一剩余治本手段**，落在本仓库，待用户确认后实施。
  - 方案 B 的精确 patch 已写入本文档第五节，可在 `F:/new-api` 目录新开会话执行。
  - 方案 D 已实测否定（5.D.5）。
