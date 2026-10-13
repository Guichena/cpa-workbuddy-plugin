# gemini-3.8-flash 大上下文流式请求 client_gone 断开根因诊断

- 记录时间：2026-10-13 22:20 (GMT+8)
- 生产入口：`https://api.luode.vip`（new-api）→ `https://cpa.luode.vip`（CPA，容器 `cli-proxy-api`）
- 故障样本：2026-10-13 21:53:04 / 21:53:06 两条 `gemini-3.8-flash` 流式请求
- 关联文档：`doc/4-bugs/2026-10-11_212700_deepseek多服务商同名认领导致推理零首字节断开根因诊断.md`、`doc/4-bugs/2026-10-12_001641_Qoder上游200排队帧未被识别致new-api调用qwen3.8挂死根因诊断.md`

---

## 一、现象与判据

| 观测点 | 本次实测 | 说明 |
|---|---|---|
| new-api 侧 | `end_reason=client_gone`、`end_error="context canceled"`、`received=0`、`frt=-1000` | 客户端在收到任何字节前断开 |
| new-api 耗时 | `use_time_seconds=72 / 74` | 断连发生在 72~74 秒 |
| CPA 侧同两条请求 | **HTTP 200**、耗时 `1m8s / 1m9s`（`1b3d881b`→`6009806c`、`4ab8186a`） | CPA 认为是正常结束 |
| 失败请求的 prompt | ≈ 450,000 tokens（其中约 450,000 命中 cache） | 典型大上下文 |
| 同时段成功同类请求 | `prompt_tokens=453166`、`frt=46374`（**首字 46 秒**）、`end_reason=eof` | 首字确实极慢，但最终成功 |

**核心矛盾**：CPA 记录 200 且耗时 68~69 秒，说明上游**最终产出了数据**；但 new-api 侧在 72~74 秒判定 `client_gone`。二者相差约 4~6 秒——正是「CPA 首字产出 → 客户端已放弃」的竞态窗口。

## 二、失败规律（12 小时全量统计）

```
=== client_gone 失败: 35 条 ===
模型分布: gemini-3.8-flash = 32,  gpt-5.6-luna = 3
耗时分布: <15s = 3,  15-45s = 1,  >45s = 31   ← 88.6% 集中在 >45 秒

=== eof 成功: 2276 条 ===
prompt > 300k 的成功请求: 481 条，首字中位数 17.6s
prompt ≤ 300k 的成功请求: 522 条，首字中位数  4.7s   ← 大上下文首字慢 3.7 倍
```

**两条铁证**：
1. 失败几乎完全集中在 `gemini-3.8-flash`（32/35），且 **31/35 耗时超过 45 秒**；
2. 首字延迟与上下文规模强相关：`prompt>300k` 中位 **17.6s**，`prompt≤300k` 仅 **4.7s**，极端值达 **48.8s**。

## 三、根因（三层叠加）

### 3.1 上游首字天然极慢（触发条件）

`antigravity` provider 承载的 `gemini-3.8-flash` 在 `prompt ≈ 450k`（含 45 万 cache token）时，上游生成首 token 需 **40~50 秒**。这是模型侧 prefill 大上下文的固有耗时，非故障。

### 3.2 CPA 在首字静默期不向下游发送任何字节（核心缺口）

取证（`F:/CLIProxyAPI`，只读）：

- **`/v1/responses` 路径**（本次实际路径）：`sdk/api/handlers/openai/openai_responses_handlers.go:759` 起的主循环 `case chunk, ok := <-dataChan` —— **必须等到首个完整 SSE data frame 才 `setSSEHeaders()` + `c.Writer.Write(initialOutput.Bytes())`**。此前响应头未提交、零字节下发。
- **`/v1beta` Gemini 原生路径**同构：`sdk/api/handlers/gemini/gemini_handlers.go:222` 起 `case chunk, ok := <-dataChan`，同样是「拿到首 chunk 才设头 + 写首帧」。
- **心跳仅在首帧之后才启动**：`ForwardStream` 内部的 keep-alive ticker（`sdk/api/handlers/stream_forwarder.go:83-89`）只在 peek 到首 chunk 后被调用；首字前无 ticker、无 SSE 注释行、无空 chunk。
- **生产 `keepalive-seconds` 未配置** ⇒ 默认 `0` = 关闭（`sdk/api/handlers/handlers.go:58` `defaultStreamingKeepAliveSeconds = 0`，生产 config 的 `requests: {}` 为空）。

⇒ **40~50 秒内，下游（new-api/nginx/客户端）在 TCP 层看不到任何字节**。

### 3.3 中间层/客户端在静默窗口内放弃

- nginx（`api.luode.vip.conf:24` `proxy_read_timeout 600s`）不会在 72 秒断开，可排除。
- new-api 侧：`relay/helper/stream_scanner.go:298-301` 主循环 `case <-c.Request.Context().Done()` → `SetEndReason(ClientGone, context canceled)`；`frt=-1000` 哨兵（`relay/common/relay_info.go:564` 初值 = `startTime - 1s`）证明**从未收到首帧**。
- 即：**是调用方（上游 Agent / SDK 自身的首字节超时）先取消了 HTTP 请求**，new-api 感知为 `context canceled` → `client_gone`；随后 new-api 执行 `cleanup()` 关闭到 CPA 的 `resp.Body`（`stream_scanner.go:126-129`）。

### 3.4 为什么 CPA 记 200（可观测性缺口）

CPA 的 `gin_logger` 取 `c.Writer.Status()`。首字节前断开时 `WriteHeader` 从未被调用，gin 保持默认 `http.StatusOK`（200）；且 `statusCode < 400 && isContextCanceled(c)` 被判定为非可操作错误而不记错误日志。**结果：上游侧表现完全健康，故障只在下游可见**——这解释了「CPA 日志无异常但客户反复断连」的迷惑现象。

## 四、结论

- **不是** max_tokens 问题，**不是** 账号/额度问题，**不是** 上游报错。
- 根因是 **「大上下文 ⇒ 上游首字 40~50s」 × 「CPA 首字前零字节下发」×「调用方首字节超时」** 三者叠加的静默窗口竞态。
- `gemini-3.8-flash` 之所以最突出，是因为它在生产中被用于**最大上下文**（≈450k）的会话，首字延迟被放到最大。

## 五、修复方案

> 关键约束：本仓库（`F:/cpa-plugin`）**无权修改** `F:/CLIProxyAPI`（兄弟项目，只读红线）。因此修复分「本仓库可做」与「需在 CPA 项目执行」两类。

### 方案 A（推荐，根治，需在 CLIProxyAPI 项目执行）

**在 peek 首帧之前就启动 SSE 心跳**，消除静默窗口：

- 现状：keep-alive ticker 在 `ForwardStream` 内、首 chunk 之后才启动。
- 改法：在 `openai_responses_handlers.go` / `gemini_handlers.go` 的 peek 循环（`for { select { ... } }`）中，把 `keepAliveC` 一并纳入 `select`，在等待首 chunk 期间每隔 `keepalive-seconds` 写入 `: keep-alive\n\n` 并 `flusher.Flush()`（首次 flush 会提前提交响应头，须确认与 `WriteErrorResponse` 的错误路径不冲突——**需先设头再可能写错误体，或保留一个「未提交头」的错误窗口**）。
- 配套：生产 config 显式配置 `requests.streaming.keepalive-seconds: 15`（默认 0 = 关闭，必须显式开启）。

### 方案 B（本仓库可做，降级缓解）

**降低单请求上下文规模**，把首字拉回可接受区间：

- 生产实测：`prompt≤300k` 时首字中位仅 4.7s，远低于调用方超时窗口。
- 措施：对 `gemini-3.8-flash` 启用更积极的会话压缩 / 历史截断；或将超大上下文的会话路由到首字更快的小上下文模型。
- 局限：治标不治本，且依赖调用方上下文策略。

### 方案 C（调用方侧，最快见效）

调高调用方（Agent/SDK）的**首字节超时**至 90~120 秒，覆盖最坏 48.8s 的首字延迟。

## 六、验证与防复发

1. **修复后验收判据**：`gemini-3.8-flash` 大上下文请求的 new-api 日志中，`end_reason` 应从 `client_gone` 转为 `eof`，`frt` 从 `-1000` 转为真实毫秒值，且 `prompt_tokens > 0`（可正常计费扣款）。
2. **监控**：对 `end_reason=client_gone AND frt=-1000` 建立告警——该组合等价于「首字未产出即断连」，是本次故障的指纹。
3. **回归样本**：`202610131351521588589728268d9d64M1jjvFF`（21:53:04）与 `202610131351522636497188268d9d6eedz2wYA`（21:53:06）。

## 七、当前状态

- 诊断：已完成，证据链闭合（两侧日志 + 源码 + 12 小时全量统计）。
- 修复：**未实施**。方案 A 需在 `F:/CLIProxyAPI` 项目新开会话执行（本仓库跨项目只读红线）；方案 B/C 待用户选定。
