# Qoder AI Plugin Changelog

## 0.1.20

### Fix - 重建上游 messages 时保留工具调用结构，修复 agent 多轮往返中工具结果失去配对

- 变更要点:
  1. 根因：客户端多轮 agent 往返的历史里，`assistant.tool_calls`（工具调用及其 `id`）与 `role:"tool"` 消息的 `tool_call_id` 在插件重建 messages 时被丢弃，工具结果以无主文本形式留在历史中；上游无法把工具结果与先前的工具调用配对，模型难以判定「该探测的都探测完了」，倾向于重复发起同类调用，表现为「推理一小段就停、需反复追问」。与 0.1.17 修的是同一缺陷的两半：0.1.17 覆盖「客户端未声明 tools」，本版覆盖「客户端声明了 tools」（Qoder Desktop 默认即声明）。
  2. `openAIMessage` 补 `ToolCalls` / `ToolCallID` / `Name` 三个字段，`UnmarshalJSON` 同步解析——该函数是自定义实现，不同步解析则新字段恒为空。
  3. `buildQoderBody` 重建 messages 时按非空保留上述字段，不再只搬 `role` / `content`。
- 验证: 新增 `TestOpenAIRequestPreservesToolStructure`（解析层保留 tool_calls / tool_call_id / name）与 `TestBuildQoderBodyPreservesToolPairing`（重建层保留配对结构）；两组必失败哨兵反证（分别回退重建逻辑与解析赋值 → 对应测试精确 FAIL → 还原全绿）；`cgo-shim-build.py qoder-ai` build/vet/test 全绿。
- 边界说明: 严格对照实验显示「保留结构」非充分条件——探索型任务下完整结构与缺失结构均可能不收敛，单次工具即可解决的任务下两者均收敛。本版修的是明确缺陷（结构丢失），探索倾向的进一步抑制需另行评估。
- 涉及文件: qoder-ai/body.go、qoder-ai/body_test.go、qoder-ai/main.go、qoder-ai/VERSION、qoder-ai/CHANGELOG.md

## 0.1.19

### Fix - 排队帧不再误判为账号级故障，并打通宿主跨 provider 轮转的状态码通道

- 变更要点:
  1. 根因：上游排队帧（`403: 10605: {"isQueued":true,...,"serviceAvailable":true}`）此前被 remap 成 403 走硬失败通道，给健康账号推进连续失败计数 + 15s 冷却。上游排队持续期间，每个被路由到的账号都被逐个冷却，整池被冷掉后宿主只能报 `auth_unavailable`——把上游的"暂时排队"放大成 CPA 侧"完全不可用"。
  2. 排队帧识别：`policy.go` 新增 `isQueueFrame`（识别 `isQueued` / `queueCount` / 业务码 `10605`）；`accountFailover.go` 的 `recordAccountFailure` 在唯一记账入口拦截，排队帧既不推进计数也不冷却账号。依据 2026-10-12 Bug 单结论：排队是请求级 + 时段性，非账号级。
  3. 状态码通道：`envelopeError` 补 `HTTPStatus` 字段并从文本推导；排队帧带 **503**（而非 403——宿主对 403 打 30 分钟冷却，与"不冷却账号"冲突；503 只打瞬时冷却且同样在宿主跨 provider 轮转白名单内）。`marshalStreamErrorEnvelope` 同步带上 `http_status`，覆盖异步流式路径。
  4. 覆盖路径：非流式与同步流式经 `errorEnvelope` 立即生效；异步流式需宿主侧读取该字段（改动计划见 `doc/3-实施/2026-10-15_160000_CLIProxyAPI宿主流式错误通道透传状态码改动计划.md`，宿主未读时字段被忽略、行为与旧版一致）。
- 验证: 新增 `TestIsQueueFrame_Classification`（10 例边界）、`TestQueueFrameDoesNotCoolOrCount`（12 次排队不记账不冷却 + `noteAccountFailure` 同样拦截）、`TestErrorEnvelopeCarriesQueueStatus`（信封带 503 / 非排队省略）、`TestMarshalStreamErrorEnvelopeCarriesQueueStatus`（流式信封带 503）；两组必失败哨兵反证（改断言 → FAIL → 还原全绿）；`cgo-shim-build.py qoder-ai` build/vet/test 全绿。
- 涉及文件: qoder-ai/policy.go、qoder-ai/accountFailover.go、qoder-ai/main.go、qoder-ai/stream.go、qoder-ai/stream_queue_frame_test.go、qoder-ai/stream_error_envelope_test.go、qoder-ai/VERSION、qoder-ai/CHANGELOG.md

## 0.1.18

### Feat - 签到恢复每 4 小时，与其余签到插件统一节奏

- 变更要点:
  1. `usage_config.go` 的 `checkinHours` 由 `[10, 22]` 恢复为 `[0, 4, 8, 12, 16, 20]`：与 `keepaliveHours` 取值重新一致，签到节奏回到本地时间每 4 小时一次。
  2. `checkin.go` 移除 `shouldRunCheckinNow` 时段门控：该门控仅在「签到表 ≠ 保活表」时才有意义（当初收敛为 10:00/22:00 而保活仍为六槽，共享定时器会在保活时刻误触发签到）；两表取值一致后并集唤醒即等于签到表本身，门控退化为恒真死代码，删除后与 workbuddy / workbuddy-ai / traework 同构。
  3. 面板文案与 `schedule` 字段同步：`panel.go` 的 `schedule` 改为六槽、`panel.html` 副标题与开关提示改为 `00/04/08/12/16/20`。
- 验证: `checkin_schedule_test.go` 重写为锁定六槽表 + 验证 `nextCheckinTime` 并集唤醒与跨天顺延；`cgo-shim-build.py qoder-ai` build/vet/test 全绿；面板内联 JS 经 vm + DOM 桩真实执行无 ReferenceError。
- 涉及文件: qoder-ai/checkin.go、qoder-ai/usage_config.go、qoder-ai/panel.go、qoder-ai/panel.html、qoder-ai/checkin_schedule_test.go、qoder-ai/VERSION

## 0.1.17

### Fix - 对齐 workbuddy 补齐三处功能遗漏（tool_choice 规整 / 计费重试 / 推理强度上报）

- 变更要点:
  1. **tool_choice 形态规整（对齐 workbuddy `normalizeToolsInPlace`）**：`body.go` 新增 `normalizeToolChoice`，把客户端各形态的 tool_choice 归一为上游可识别的字符串——对象形 `{"type":"auto|required"}` → 字符串、`{"type":"function","function":{"name":"x"}}` → 函数名、`"none"` → 剥离整组 tools。此前 `alignToolsWithClient` 把 `tool_choice` 原样透传，而上游只认字符串形态：实测 `tool_choice:"none"` 时上游照常发出工具调用，与「禁用工具」意图相反。
  2. **计费接口瞬时重试（对齐 workbuddy `billingCall`）**：`billing.go` 新增 `billingDo` 包装（300ms/900ms 退避，仅 5xx 与传输错误重试），应用于 `fetchCheckinStatus` / `fetchUserResource` / `fetchPaymentType` / `performCheckinCall`。此前单次直调，上游 5xx 或 host 桥接抖动会让面板积分与计划整行空白（stale-while-error 仅在已有前值时才兜底）。
  3. **reasoning_effort 上报（对齐 workbuddy `reasoningEffortFromBody`）**：客户端请求的推理强度写入 usage feed，补齐 dashboard 用量明细的推理强度维度（此前恒为空，与 workbuddy 数据不可比）。该值不进上游 body（Qoder 模板无此字段位点）。
- 验证: 新增 `tools_alignment_test.go` 4 个用例（tool_choice 归一矩阵 / none 抑制 / 对象形归一）+ `billing_retry_test.go` 4 个用例（5xx 重试成功 / 4xx 不重试 / 重试重建 Body / feed 上报）；哨兵反证三组均精确 FAIL 后复绿。
- 涉及文件: qoder-ai/body.go、qoder-ai/billing.go、qoder-ai/main.go、qoder-ai/stream.go、qoder-ai/tools_alignment_test.go、qoder-ai/billing_retry_test.go、qoder-ai/VERSION

## 0.1.16

### Fix - 模板硬编码工具集致普通问答被劫持为 agent 工具调用（推理「一小段就停」）

- 缺陷: `baseprompt.json` 模板自带 Qoder CLI 的 14 个 agent 工具（Bash/BashOutput/Edit/Glob/Grep/ImageGen/KillBash/Read/Skill/Task/TodoWrite/WebFetch/WebSearch/Write）与 agent 会话上下文，而 `buildQoderBody` 对 `tools` 零处理、`openAIRequest` 也没有 `Tools` 字段，导致模板工具集**无条件、每请求**上行。上游因此把普通问答当 agent 任务作答：模型先输出一句编排语（如「我先确认本机是否有 Go 源码」）→ 发出 `Bash`/`Glob` 工具调用 → 以 `finish_reason=tool_calls` 结束本轮等工具结果；下游无工具回路，表现为「推理一小段就停、等用户继续」。`qwen-3.8-max` 的 agentic 倾向最强，流式 4/4 稳定复现。
- 修复要点:
  1. `openAIRequest` 新增 `Tools` / `ToolChoice` 字段，客户端工具声明不再被静默丢弃。
  2. 新增 `alignToolsWithClient(base, req)`：客户端未声明工具时剥离模板 `tools` 与 `tool_choice`；声明了则用客户端工具整体覆盖并透传 `tool_choice`，使真正的 agent 客户端仍获得完整工具回环。
  3. 模板 system prompt 保持不变（上游服务器行为依赖它），`agent_id` / `session_type` 亦不动——直连上游隔离实验证明**仅删 `tools` 即足够**。
- 验证: 直连上游隔离实验（复刻 COSY 签名 + QoderEncoding）——原样 20~30 帧 tool_calls、正文仅 33~44 字符；仅删 tools 后 4/4 采样 `toolcall=0`、`finish=stop`、正文 3300~4500 字符。
- 涉及文件: qoder-ai/body.go、qoder-ai/tools_alignment_test.go（新增）、qoder-ai/main.go、qoder-ai/VERSION

## 0.1.15

### Feat - max_tokens 统一 32768 最低输出底线

- 变更要点:
  1. 设立 `minMaxTokensFloor=32768` 统一最低输出底线：`model_specs.go` 新增该常量与 `applyMaxTokensFloor` 纯函数，`computeAdaptiveMaxTokens` 四个出口全部经其收敛，杜绝任何路径回落到 32768 以下的过小输出上限。
  2. 广播层同步：`models.go` 的静态模型列表、动态发现与配置覆盖路径的 `MaxCompletionTokens` 一并保底。
  3. 测试同步：`max_tokens_test.go` / `models_config_test.go` 既有断言更新为 32768 底线语义。
- 说明: 本插件无定时探活，探活相关改动不适用于本插件。
- 涉及文件: qoder-ai/model_specs.go、qoder-ai/models.go、qoder-ai/*_test.go

## 0.1.14

### Fix - 补齐 /clear-cooldown 管理路由注册（宿主分派缺失导致生产 404）

- 变更要点:
  1. 缺陷：0.1.13 已在 `handleManagement` 的 switch 中实现 `POST /clear-cooldown`，但 `managementRegistration().Routes` 未登记该路由。宿主按注册表分派请求，未声明路径被宿主层拦截，返回空 body 404（qoderwork 0.9.34 因已登记而正常）。
  2. 修复：在 `management.go` 的 `Routes` 列表中补登 `{POST, /plugins/qoder-ai-provider/clear-cooldown}`，与 switch case 及 qoderwork 保持同构。
  3. 门禁验证：`cgo-shim-build.py qoder-ai` build/vet/test 全绿。

## 0.1.13

### Feat - 上游 110 日调用上限 24 小时持久化冷却、路由排除与面板解冷

- 变更要点:
  1. 110 精准识别：`policy.go` 新增 `isDailyCountExceeded`，覆盖 `110: Billing daily count exceeded` 及 JSON `code: 110` 多种形态，避免误伤正常业务帧。
  2. 持久化冷却引擎：新增 `daily_cooldown.go`，将 `daily_cooling_until`（RFC3339，24 小时）落盘物理 auth 文件，进程重启与热重载均不丢失；内存镜像 `dailyCoolingMap` 支持归一化键检索。
  3. 路由与换号绝对排除：`isAccountCoolingDown` 首位接入每日冷却判定，联动 `scheduler.pick` / `pickNextAuth` / 会话粘性；`resetAccountFailover` 严格保留冷却，仅到期自然解除。
  4. 全链路触发：`stream.go`（HTTP 4xx、SSE 200 业务错误帧、Qoder SSE 收集）与 `main.go` 换号重试/终态错误均触发 `triggerDailyCooldown`；`watchdog.go` 定时从磁盘重建内存镜像。
  5. 面板徽章与手动解除：新增 `POST /clear-cooldown` 接口（`handleClearCooldown`），`buildDashboardEx` 与 `/credits` 输出 `daily_cooling_until` / `daily_cooling_remain`；前端新增「冷却」筛选标签、`110冷却(HH:MM)` 徽标与带二次确认的「解冷」按钮。
  6. 门禁验证：新增 `daily_cooldown_test.go`、`daily_cooling_routing_test.go`，`cgo-shim-build.py qoder-ai` 全绿。

## 0.1.12

### Feat & Fix - 设立安全缓冲额度红线 (minRoutableCredits=20)、健康平摊调度与换号过滤

- 变更要点:
  1. 设立安全缓冲额度红线 (`min_routable_credits`，默认 20，可配置)：低于阈值的残血账号硬排除出路由池，防止推理中途断开。
  2. 调度策略逆转：废除残血优先（Low-Credit-First）升序排序，存活健康候选平摊轮询（Round-Robin）。
  3. 请求内换号链过滤：`pickNextAuth` 跳过 `remain < min_routable_credits` 账号；`hardCreditMarkers` 补齐 `exceeded the quota` 与 `14018` 换号重试。
  4. 面板状态与视觉对齐：新增 `low_credits` 状态与「低额保护」黄色徽标展示。

## 0.1.11

### Feat - 引入官方规格矩阵自适应 max_tokens 计算

- 变更要点:
  1. 引入 `model_specs.go`：维护 Qoder/阿里官方及主流推理模型规格矩阵（`qfmodel`, `qmodel_38max`, `gmodel`, `dmodel`, `deepseek-v4.1-flash` 等）。
  2. 请求体动态注入自适应 `parameters.max_tokens`：客户端未指定时自动补齐官方标准上限；推理模型且客户端指定 <= 8192 时放宽至 ReasoningMax（如 DeepSeek 64k / GLM 128k），避免思考过程耗尽 token 截断。
  3. 模型目录同步：`wbModels()` 输出真实 `MaxCompletionTokens` 规格，废除全量硬编码 8192。
  4. 门禁验证：`cgo-shim-build.py qoder-ai` 编译与测试全绿。

## 0.1.10

### Fix - 移除自动定时探活、下线 test_failed 机制、签到时间收敛为 10:00/22:00

- 变更要点:
  1. 根因：上游对同账号突发调用限流，在 HTTP 200 下返回业务码 `110: Billing daily count exceeded`。自动探活每轮 5 模型轮测 + 每账号 10 分钟节流，一天产生上千次真实推理调用，稳定触发该限流并消耗真实推理的账号配额（实测健康账号连发 20 次均成功，密集轮测则触发 110，属突发限流而非日配额耗尽）。
  2. 移除自动定时探活全链路：删除 `doActivePing`（多模型轮测）、`triggerActivePing`、30 分钟节流状态、`refresh_runner` 触发点与 watchdog 探活语义。**保留面板「测试」按钮的手动单次调用**（用户主动点击才发 1 次请求）。
  3. 完整下线 `test_failed` 机制：打标/清标逻辑唯一宿主在自动探活内，探活移除后该标签将永久无法清除、导致存量账号被永久排除出调度。故一并删除机制本体（`test_failed_tag.go`）、三处调度排除判据（`scheduler.go` / `active_auth.go` / `failover_retry.go`）、面板字段与「测试」筛选标签，并在启动时剥离存量标签（`stripAnomalyKey` 扩展）。
  4. 签到时间由每 4 小时（`[0,4,8,12,16,20]`）收敛为每天 **10:00 / 22:00** 两次；新增 `shouldRunCheckinNow` 门控——共享定时器会被 keepalive 时刻唤醒，不门控会让签到在那些时刻重复触发。
  5. 面板文案同步：`09:00/21:00` → `10:00/22:00`，`schedule` 字段同步为 `["10:00","22:00"]`。
- 涉及文件：`active_ping.go`、`refresh_runner.go`、`watchdog.go`、`anomaly_purge.go`、`scheduler.go`、`active_auth.go`、`failover_retry.go`、`panel.go`、`panel.html`、`credits_handler.go`、`usage_config.go`、`checkin.go`、`active_ping_test.go`、`routing_exclusion_test.go`、`checkin_schedule_test.go`（新增）；删除 `test_failed_tag.go`、`test_failed_tag_test.go`。
- 门禁：`cgo-shim-build.py qoder-ai` build/vet/test 全绿；`checkin_schedule_test.go` 经哨兵变异（`checkinHours` 回退 6 槽）后 4 用例精确 FAIL，证明真实进编译；6-review `STYLE: PASS`；`POLLUTION: PASS`。

## 0.1.9

### Fix - 识别上游 HTTP 200 排队帧，消除大上下文请求 60 秒挂死

- 变更要点:
  1. 根因：阿里 Qoder 对超上下文请求投入排队，在 HTTP 200 下返回顶层 `code:"403"` 帧（内嵌 `10605 / isQueued:true / serviceAvailable:false / retryAfterSeconds:30`），随后保持 HTTP/2 Keep-Alive 心跳约 60 秒才由对端复位。插件 SSE 解析器不识别该形态，将其当作无关行丢弃，读不到终止信号而死等连接超时——客户端在 60 秒内零字节，new-api 侧记 `client_gone`。
  2. 解析器加固：`parseQoderSSEChunk` 新增第 4 类形态识别（顶层非空 `code` 帧），使该帧进入错误处理路径而非被丢弃。
  3. 错误语义提取：`sseErrorFrame` 扩展识别顶层 `code` + `message`，并新增 `unwrapNestedMessage` 逐层展开内嵌 JSON message（保留首层业务码如 10605），产出可读错误文本。
  4. 三路径即时收尾：`pumpUpstreamStream`（异步流式）、`collectUpstreamStreamQoder`（同步流式）、`aggregateQoderSSE`（非流式聚合）均在读取循环内命中错误帧即刻 `break`/返回，不再死等 Keep-Alive；行为统一为「立即报错」（排队帧不含 credit/rate-limit marker，`shouldRotateOnUpstreamErr` 天然为 false，不做号池内换号消耗）。
  5. 探活假成功修复：`sendActivePingQoder` 由「读到首字节即成功」改为「读到首个有效业务帧才成功」，错误帧/排队帧如实返回失败。
- 涉及文件: qoder-ai/stream.go、qoder-ai/active_ping.go、qoder-ai/stream_queue_frame_test.go、qoder-ai/VERSION、qoder-ai/main.go、qoder-ai/CHANGELOG.md

## 0.1.8

### Fix - 移除未授权自动认证成功的 Panel-import 假成功逻辑

- 变更要点:
  1. 根因：`oauth.go` 中的 `handlePollLogin` 包含一段历史遗留的 Panel-import 分支，扫描 `hostAuthList()` 中 `f.CreatedAt.UnixNano() > lc.startedAt` 的凭据直接判为登录成功；由于宿主文件 watcher 与后台 watchdog 每次刷新账号均会将 `f.CreatedAt` 赋值为 `time.Now()`，导致用户点击添加账号生成授权链接后，尚未打开页面进行授权，现有账号刷新即被误判为“新授权账号”并返回成功（假成功）。
  2. 修复：彻底删除该 Panel-import path，设备授权登录严格等待上游 `pollDeviceToken` 真实完成或 metadata 明确传入 PAT 兑换，彻底根治未点链接自动认证成功的异常。
- 涉及文件: qoder-ai/oauth.go、qoder-ai/VERSION、qoder-ai/CHANGELOG.md

## 0.1.7

### Fix - 模型推理请求终止契约与超时加固（对齐 WorkBuddy 成熟机制）

- 变更要点:
  1. 根因收敛：排查定位到 `collectUpstreamStreamQoder` 遇到 `[DONE]` 误用 `continue` 未退出循环导致 Keep-Alive 挂死至网关长超时；顶层裸 `data: [DONE]` 等多种终止符在 JSON 失败后被静默跳过；`rebuildRequestWithQoderAuth` 丢失请求级 Context 无法级联超时与取消；`doExecuteOnceQoder` 缺少 Context 超时；`buildQoderBody` 存在按字节截断损坏多字节 UTF-8 字符的隐患。
  2. 终止契约加固：抽取统一的 `parseQoderSSEChunk`，同时覆盖顶层 `data: [DONE]`、嵌套信封 `outer["body"]` 为 `[DONE]` 及扁平 OpenAI chunk 格式，遇到终止符即刻 `break` 退出并主动关闭流。
  3. 全链路 Context 与超时保护：`rebuildRequestWithQoderAuth` 继承请求 Context；`collectUpstreamStreamQoder` 与 `doExecuteOnceQoder` 施加 10 分钟预算超时；换号重试前统一调用 `evictSessionBindingsForAuth` 避免死锁。
  4. 健壮性与内存治理：`biz["name"]` 采用 `[]rune(prompt)[:30]` 字符切片；`extractLatestUserPrompt` 增加非 user 消息与 `"continue"` 降级兜底；`streamClose` 增加延迟清理消除 map 内存泄漏。
  5. 回归测试：新增 `stream_inference_hardening_test.go`，经哨兵必失败反证确证真实进入编译并全绿通过。
- 涉及文件: qoder-ai/stream.go、qoder-ai/main.go、qoder-ai/body.go、qoder-ai/failover_retry.go、qoder-ai/stream_inference_hardening_test.go、qoder-ai/VERSION、qoder-ai/CHANGELOG.md

### Fix - 补齐国际版签到 UMID 机器标识头与多账号设备指纹隔离，解决客户端可签但插件无活动且积分不增加

- 变更要点:
  1. 根因：国际版 `GET /sash/api/v1/me/campaigns` 与 `POST .../claim` 除校验 `Cosy-ClientType: 10` 外，还强制校验 `Cosy-MachineToken`（`P1gA...` UMID 令牌）与 `Cosy-MachineType`（由 `xxh64(machineToken)` 插值 `vmTypeCode` 派生）；缺少这两个头部时，上游在每日 10:00 (UTC+8) 刷新后仅返回宣传类 `VIEW_DETAILS` 活动而隐藏 `CLAIM_BENEFIT`（每日领 100 Credits）活动，导致全部生产账号被判为 `active: false`。同时，若多个账号共用同一设备的 UMID 令牌，第二个起会被上游拦截返回 `status=BLOCKED, failureCode=SAME_PERSON_ALREADY_CLAIMED`。
  2. 修复：新增纯 Go 实现的 `umid.go` / `umid_codec.go` / `umid_crypto.go`，按账号 `UID` 确定性派生独立虚拟网卡 MAC 与主机特征，向 `us-pum.alibabachengdun.com/repPc.json` 获取官方签发的独立 `P1gA...` `machineToken` 并缓存，按 `xxh64` 派生 `Cosy-MachineType` 与 `Cosy-MachineCode`；`campaignHeaders` 注入完整 `Cosy-*` 头部；`performCheckinCall` 遇 `SAME_PERSON_ALREADY_CLAIMED` 时自动递增盐值轮换新设备标识重试。
  3. 缓存与奖励字段修复：`checkinOneAccount` 兼容 `int64` / `float64` 提取 `rewardCredits`，并在签到完成后立即通过 `updateAccountCacheSnapshot` 刷新 `accountCache` 中的签到与积分快照。
- 涉及文件: qoder-ai/umid.go、qoder-ai/umid_codec.go、qoder-ai/umid_crypto.go、qoder-ai/billing.go、qoder-ai/checkin.go、qoder-ai/qoder_ai_endpoints_test.go、qoder-ai/VERSION、qoder-ai/main.go

## 0.1.5

### Fix - 批量签到统计把「无活动」账号误计为失败

- 变更要点:
  1. `handleManualCheckin` 的 summary 新增 `no_activity` 计数：`reason=global`（当前无可领取活动）的账号不再计入 `fail`。0.1.4 修复后，无活动的国际账号会返回 `reason=global`，原统计把它们算作失败，面板批量签到会误报「N 失败」。
  2. 面板 `checkinAll` 同步：使用 `no_activity` 单列，文案由「国际版无需签到」改为「当前无进行中的签到活动 / 无活动 N」。
- 涉及文件: qoder-ai/checkin.go、qoder-ai/panel.html、qoder-ai/VERSION、qoder-ai/main.go

## 0.1.4

### Fix - 国际版签到改走 campaigns（活动）API，修复每日签到恒 404

- 变更要点:
  1. 根因：国际版签到原套用 CN（qoderwork）专有端点 `/sash/api/v1/me/daily-check-in/{status,claim}`，但国际站 `openapi.qoder.sh` 根本没有该端点（与随机 bogus 路径返回完全相同的 404 `NotFound`）；0.1.3 修好宿主桥状态码解码后，签到从「假成功」变为「诚实的 404」，暴露出该端点从未可用。真实每日 100 Credits 活动走 **campaigns（活动）API**。
  2. 修复：新增 `endpointCampaigns` / `endpointCampaignClaim` 常量；新增 `campaignHeaders`（`Cosy-ClientType: 10` + `User-Agent: Qoder`，缺这两个头时 campaigns 列表被掩盖为 `showCampaign:false, campaigns:[]`）；`fetchCheckinStatus` 改为解析 campaigns 列表，用 `pickCheckinCampaign` 挑出窗口内 `CLAIM_BENEFIT` 的 CREDITS 活动；`performCheckinCall` 改为 POST `/sash/api/v1/me/campaigns/{campaignId}/claim`，按 `status=CLAIMED` 判定成功、`replayed=true` 归一为「今日已签到」。
  3. 面板：签到成功提示改用 `reward_credits`；「无可领取活动」文案修正（原「国际版无需签到」已不符实际）。
  4. 回归测试：`qoder_ai_endpoints_test.go` 更新端点契约，新增 `pickCheckinCampaign` 三用例（命中/窗口外/无活动），修复前必失败（反证 `active=false; want true`）。
- 生产实测（2026-10-11）：三账号 `GET /sash/api/v1/me/campaigns` 均返回真实活动（`campaignKey=act-20261009-118`, `benefit=CREDITS/100/30天`）；`ua554edc3` 识别 `active=True, checked=True, credit=100`，`POST .../{campaignId}/claim` 返回 200 `status=CLAIMED`。
- 涉及文件: qoder-ai/main.go、qoder-ai/billing.go、qoder-ai/checkin.go、qoder-ai/management.go、qoder-ai/panel.html、qoder-ai/qoder_ai_endpoints_test.go、qoder-ai/VERSION

## 0.1.3

### Fix - 修复宿主桥状态码解码错误导致签到假成功

- 变更要点:
  1. 根因修复：`hostHTTPDo` 用 `json:"status_code"` 解码宿主 `host.http.do` 响应，而宿主（v7.2.x）序列化 `pluginapi.HTTPResponse` 时未加 json tag，实际键名是 PascalCase `{"StatusCode":404,...}`；下划线标签既不匹配键名也不匹配大小写不敏感匹配，`StatusCode` 恒为 0，`if resp.StatusCode >= 400` 永不触发，所有 404/4xx/5xx 被当作 200 成功。抽出纯函数 `parseHostHTTPDoResult` 按真实线协议解码，并防御性兼容下划线变体。
  2. 归一化收紧：`performCheckinCall` 在响应缺 `success` 布尔字段时返回失败（契约异常），不再默认成功。
  3. 回归测试：新增 `host_bridge_test.go`（PascalCase 主用例、下划线兼容、非法载荷），修复前必失败（反证 `StatusCode = 0, want 200`）。
- 涉及文件: qoder-ai/host_bridge.go、qoder-ai/billing.go、qoder-ai/host_bridge_test.go、qoder-ai/VERSION、qoder-ai/main.go

### 已知问题（GAP-001，已在 0.1.4 解决）

- 0.1.3 时观察到国际版 OpenAPI `openapi.qoder.sh` 无 `/sash/api/v1/me/daily-check-in/{status,claim}` 端点（404 `NotFound`）。**0.1.4 已确认**：国际版签到走 campaigns API，且需 `Cosy-ClientType:10` + `User-Agent:Qoder` 头；缺头时 campaigns 列表被掩盖为空。详见 0.1.4 条目。

## 0.1.2

### Fix - 修复多模态 content 数组导致真实推理请求 payload parse 503

- 变更要点:
  1. 根因修复：openAIRequest 的 messages[].content 原声明为 string，客户端发送 OpenAI 多模态部件数组（[{"type":"text","text":"..."}]）时 json.Unmarshal 直接失败，executor 返回 "payload parse: json: cannot unmarshal array into Go struct field openAIMessage.messages.content of type string"，表现为推理请求 503；面板「测试」按钮走 sendActivePingQoder 直接构造结构体，绕过 JSON 解析，故测试通过而真实请求失败。
  2. 兼容实现：为 openAIMessage 增加自定义 UnmarshalJSON，纯字符串形态原样接收，多模态部件数组提取 text / input_text 部件文本并按换行拼接，下游 buildQoderBody 无需感知差异；非文本部件（图片等）无对应上游位置，按空处理。
  3. 回归测试：新增 body_test.go 覆盖部件数组解析、纯字符串兼容、多文本部件拼接与完整 buildQoderBody 链路，修复前必失败（已用真实报错反证）。
- 涉及文件: qoder-ai/body.go、qoder-ai/body_test.go、qoder-ai/VERSION、qoder-ai/main.go

## 0.1.1

### Feat - 官方客户端模型对齐、管理面板测试按钮与高清图标补齐

- 核心要点:
  1. 模型对齐与动态获取：依据本地 Qoder 官方客户端配置，精准映射 12 个核心模型（Cantus、Qwen3.8-Max、Qwen3.8-Flash、Qwen3.7-Max、Qwen3.7-Plus、GLM-5.3、GLM-5.3-Flash、Kimi-K3、Kimi-K2.8-Preview、DeepSeek-V4-Pro、DeepSeek-Flash、MiniMax-M3）及 cmodel/qmodel_38max 等上游代号，上下文统一至 1,000,000，并在动态获取失败时平滑兜底。
  2. 测试按钮与定时探活：管理面板卡片补齐「测试」按钮与指定模型测试模态弹窗，支持手动点选模型实时推理测试；后台打通基于 refresh_runner 的多模型自动定时探活（30分钟节流、30秒熔断）。
  3. 图标补齐：提取 Qoder 官方 1024×1024 高清图标至 assets/icons/QoderAI.png，修正 main.go 中包含空格且 404 的 pluginLogoURL，同步更新 registry.json。
- 涉及文件: qoder-ai/models.go、qoder-ai/body.go、qoder-ai/active_ping.go、qoder-ai/active_ping_test.go、qoder-ai/refresh_runner.go、qoder-ai/panel.html、qoder-ai/main.go、qoder-ai/VERSION、assets/icons/QoderAI.png、registry.json

## 0.1.0

### Feat - Qoder AI 国际版独立插件首发

- 变更要点:
  1. 架构解耦：从国内版独立为 qoder-ai-provider 插件，原生对接国际站 openapi.qoder.sh 与 OAuth 授权页（qoder.com/device/selectAccounts）。
  2. 每日签到：对接 /sash/api/v1/me/daily-check-in/claim 与 /status，每日签到领取 100 积分，支持 4 小时后台定时巡检与批量签到。
  3. 专属管理控制台：独立挂载于 /v0/management/plugins/qoder-ai-provider，支持账号状态查看、配置修改与指定模型测试弹窗。
  4. 生态集成：注册表 registry.json 登记，用量监控归一化聚合为 "Qoder AI"。
- 涉及文件: qoder-ai/*、.github/workflows/build.yml、registry.json、token-usage-tracker/usage_stats/auth_identity.go

## 0.9.22

### Feat - 生命周期自动停用默认关闭

- 变更要点:
  1. lifecycle_auto 默认值由 true 改为 false：新装 / 未显式配置的部署不再自动停用积分耗尽的账号，也不再自动重新启用回血账号。
  2. 三处默认值同步：包级 lifecycleAuto 初值、configure() 的 nextLifecycleAuto 默认值、ConfigField 描述文案（「默认开启」→「默认关闭」）。
  3. 显式 lifecycle_auto: true 行为不变。
- 涉及文件: qoderwork/policy.go、qoderwork/usage_config.go、qoderwork/main.go、qoderwork/VERSION

## 0.9.21

### Fix - 面板筛选标签计数随积分回填重算

- 根因：`updateFilterCounts()` 只在 `load()` 里调用一次，后台刷新回填积分后只经 `renderSummary()` 重画汇总卡，筛选标签计数不再重算，长期停在首屏旧值。
- 修复：标签计数挂到统一渲染入口 `renderSummary()`，`load()` 去掉重复调用；保留该面板既有「保号池」口径不改。
- 验证：`node test/workbuddy/panel_filter_counts_repro.mjs qoderwork` PASS；`cgo-shim-build.py` build/vet/test 全绿。
- 涉及文件: qoderwork/panel.html

## 0.9.20

### Fix - 移除管理层令牌桶限流 + 刷新改并发 10

- 变更要点:
  1. 彻底移除管理层 per-IP 令牌桶限流（v0.6.31 引入）。
  2. 账号刷新队列从串行（1 账号/秒）改为并发 10（`refreshConcurrency = 10`）。
  3. 通过 `inFlight` 标志保留幂等性。
- 涉及文件: qoderwork/management.go、qoderwork/refresh_runner.go、qoderwork/refresh_runner_test.go。

## 0.9.19

### Fix — 执行器前置冷却拦截 + 多形态账号别名规范化匹配（同步自 workbuddy 0.14.33）

- **根因**：执行器收到冷却中账号时未前置拦截直接打上游导致持续报错；`isAccountCoolingDown` 跨别名未匹配。
- **修复**（与 workbuddy 对称）：
  - `pumpUpstreamStream` / `collectUpstreamStreamQoder` / `handleExecExecute` 前置冷却拦截直接换号
  - `accountFailover.go` / `failover_retry.go` 引入 `normalizeFailoverKey` 与跨别名规范化匹配
- 测试：新增 `TestIsAccountCoolingDown_NormalizedAlias`。cgo-shim 全绿。

## 0.9.18

### Fix — SSE 错误帧 HTTP 200 不再绕过冷却与 CPAMP 状态码标注（同步自 workbuddy 0.14.32）

- **根因**：上游业务错误以 SSE 错误帧包装在 HTTP 200 响应体内，`isAccountFailure(200, body)` 在不含 rate-limit/credit 关键词时返回 false → 冷却永不触发；CPAMP 上报 `failCode` 默认 200 原样传出。
- **修复**（与 workbuddy 对称）：
  - `forwardUsageToCPAMP`：`statusCode=200` 映射为 403（`usage.go`）
  - 同步流收集失败路径：`statusCode=200` 补充 `noteAccountFailure(id, 403, err)`（`main.go`）
  - 异步泵送 SSE 错误分支：先调 `noteAccountFailure(id, 403, sseErr)`（`stream.go`）
  - 非流式完成路径：同款补充（`main.go`）
- 验证：cgo-shim build+vet+test 全绿。

## 0.9.17

### Fix — 终态错误改走宿主流错误通道（chunk.Err），请求内池耗尽可跨平台自动接管（对齐 workbuddy 0.14.31 / traework 0.1.60）

- **背景**：异步流终态错误（上游失败、池耗尽等）原以 payload 数据帧（`{"error":{"message":...}}` SSE 事件）发到宿主流，宿主 conductor 把它当**正常流内容**——请求以 HTTP 200 "成功"告终，conductor 不轮换凭据、不冷却记账、不切换其他平台账号，客户端直接收到内嵌错误的流。
- **修复**：`streamEmitError` 改为经 `host.stream.emit` 信封的 `error` 字段发送（宿主映射为执行器流 `chunk.Err`，message 保留 `redactSecrets` 脱敏）。conductor 收到真正的错误 chunk 后：首包前失败 → 同请求内换下一个账号；流中失败 → 记账 + 下发错误。
- 测试：`stream_error_envelope_test.go`（信封字段断言 + payload 泄漏哨兵）。
- 验证：cgo-shim build+vet+test 全绿。

## 0.9.16

### Fix — 模型优先级反转为「动态 > 配置 > 静态」（对齐 workbuddy 0.14.28 / traework 0.1.59）

- **背景**：原语义有两条遮蔽路径——`handleModelForAuth` 的 `fetchDynamicModelsFromStorage` 开头即 `if 配置非空 return 配置`，`handleModelStatic` 更是"配置非空就完全不查动态"；用户手工配过模型后，上游新增模型永久不可见。
- **优先级反转**：新增 `resolveModels(dynamic, configured, fallback)` 三态优先级链，**动态发现有结果时完全忽略配置与静态默认**（不做合并）；动态不可用时才用配置；配置也为空才回退静态。
- **`handleModelStatic` 改为动态优先**：先走 `fetchDynamicModels()`（命中缓存或扫描已注册凭据），失败才回退配置 / 静态，与 `handleModelForAuth` 口径一致。
- **取消静默兜底**：`fetchDynamicModels` / `fetchDynamicModelsFromStorage` 失败时由返回 `wbModels()` 改为返回 `nil`，区分"上游调用失败"与"上游确实只有这些模型"，由 `resolveModels` 统一兜底；只含空 ID 的动态列表视同"没有结果"。
- 测试：`models_config_test.go` 新增优先级链 4 态用例 + 静态路径动态优先用例，原"配置优先"注释改为"配置保底"，并新增 `resetDynamicModelsCache` 隔离全局缓存。
- 验证：cgo-shim build+vet+test 全绿。

## 0.9.15

### Feat — 耗尽自动停用 + 每 4 小时签到自动恢复（策略更新：耗尽停用保留，但必须能自愈；对齐 workbuddy 0.14.27 / traework 0.1.58）

- **策略反转**（2026-09-08 用户指令）：耗尽→停用机制保留，但增加自动恢复——CN 账号积分耗尽（remain<=0）自动写 `disabled:true` + **新标记 `exhausted_disable:true`**（独立顶层字段，不依赖 note 文本匹配）；每 4 小时签到循环的 reconcile（`processAutoCheckinAccount` → `reconcileOneAccount(force=true)`，既有挂点）刷新积分后，**积分恢复>0 即清标志对并重新启用**。手动停用（`manual_disable:true`）与宿主侧无标记停用（插件无停用路由，手动操作走宿主管理 UI）永不自动覆盖。
- **`disableAuth` 重写**：enabled→disabled 转换写 `disabled+exhausted_disable`（可自动恢复）；已停用文件分三态——带 `manual_disable` 只透传、带 `exhausted_disable` 透传、无标记不添加（sticky）；`reenableAuth`（extra=nil 重建）清除两个意图标记；`syncAuthNote` 透传双标记（note 刷新不得解除恢复武装）；`deleteAuth` 无 path fallback 同步透传。
- **`reconcileOneAccount` 恢复分支标记门控**：SESSION-DEAD/TOKEN_EXPIRE note 守卫保留；仅 `exhausted_disable:true` 的停用文件参与自动恢复；manual_disable / 无标记停用一律只刷 note。
- **新增标记读取器**（authfile.go）：`manualDisableFromAuthJSON` + `exhaustedDisableFromAuthJSON`（与 workbuddy 同构）。
- **写通道切换**：`disableAuth` / `reenableAuth` / `syncAuthNote` 从 `hostAuthPersistMigrate`（host.auth.save）改为 `persistAuthDirect`——host.auth.save 重建记录会丢未知顶层字段，exhausted_disable / manual_disable 标记会丢（与 preserve / counter 同规则）。
- 测试：新增 `lifecycle_test.go`（`TestAuthMarkerReaders` + `TestBuildAuthFileJSONExhaustedMarker`）。
- 验证：cgo-shim build+vet+test 全绿。

## 0.9.14

### Feat — 移除异常池机制：失败一律走固定 15s 冷却（对齐 workbuddy 0.14.26 / traework 0.1.55）

- 背景：用户确认异常池容易误触发（连续失败即永久隔离，需手动解冻），决定三插件同步彻底移除；账号故障统一交给固定 15s 失败冷却 + 换号兜底。
- **删除** `anomaly.go` / `anomaly_config.go`：连败冻结（`freezeAccountForAnomaly`）、异常集合（`anomalySet`）、每日 00:00 自动复活、`/unfreeze` 管理路由与 `anomaly_pool_threshold` / `anomaly_refresh_enabled` 配置全部下线。
- `accountFailover.go`：连败冻结判定删除，任何失败只推进冷却；连败计数保留（面板展示用）。
- `scheduler.go` / `active_auth.go` / `failover_retry.go` / `session_auth.go`：删除全部 anomaly 过滤层与谓词。
- `usage_config.go`：删除 anomaly 两个配置键的解析与应用。
- `preserve.go`：迁入 `authFileErr` / `errAuthIndexRequired` / `errAuthMissing`（原在 anomaly.go 定义）。
- `counter.go`：修正注释漂移——落盘节奏实际挂在 `preserveWatchdogLoop` 的 tick（watchdog.go:284 `flushCounters()`），非已删除的 anomalyRefreshLoop；首个计数落盘 tick 前新增一次性 `purgeLegacyAnomalyFlags()` 清扫。
- **新增** `anomaly_purge.go` + `anomaly_purge_test.go`：watchdog 启动时剥离物理 auth 文件遗留 `anomaly:true` 死字段（幂等，坏文件不盲写）。
- `panel.html`：删除异常徽标 / 全部解冻按钮 / `unfreezeOne` / `unfreezeAll` / 异常筛选 chip / 异常计数与汇总口径中的 anomaly 维度（JS node --check 全绿）。
- 停用复扫：qoderwork 本就无 disabled:true 写入通道（`disableAuth` 只更新 note、透传既有 disabled）；`disableAuth` 入口固化 **MANUAL-TOGGLE-ONLY POLICY** 注释。
- 验证：cgo-shim build+vet+test 全绿。

## 0.9.13

### Feat — 自动签到与 token 保活调度从每日改为每 4 小时（对齐 workbuddy 0.14.25）

- `checkin.go`：`autoCheckinTimes` 从每日两班（09:00 / 21:00）改为每 4 小时六班（00:00 / 04:00 / 08:00 / 12:00 / 16:00 / 20:00，本地时间），签到频次提升 3 倍；`nextCheckinTime` 注释与面板「schedule」展示同步。
- `keepalive.go`：`keepaliveHours` 从每日 22:00 单次改为与签到同节奏的每 4 小时（0/4/8/12/16/20）；`token_keepalive` / `checkin_auto` 面板 ConfigFields 描述同步。
- 验证：cgo-shim build+vet+test 全绿。

## 0.9.12

### Fix — 停用策略改为 manual-toggle-only：自动生命周期不再写 disabled

- 背景：2026-09-06 用户确认策略——**停用只能由面板手动控制**；账号故障（refresh token 死亡、积分耗尽等）交由 failover 换号兜底（不可用账号会被自动切换，不影响请求），不再自动停用。生产实锤：traework 侧账号 392978863762272 被 keepalive 自动停用，qoderwork 同构路径一并拆除。
- `keepalive.go`：`markSessionDead` 不再写 `disabled:true`，只更新 note（`Session expired (refresh token dead): re-login required`）并记日志。
- `lifecycle.go`：`disableAuth`（积分耗尽等自动生命周期专用）保留磁盘现有 disabled 标志不变，只更新 note（`buildAuthFileJSON(sa, existingDisabled, ...)`）；`deleteAuth` 的无 path fallback 不再强制写 disabled。面板手动停用链路不经此函数，行为不变。
- 验证：cgo-shim build+vet+test 全绿。

## 0.9.11

### Fix — 瞬时过载类失败（429/soft rate limit/零字节断流）与硬失败拆分

- 新增 `isTransientThrottle`（policy.go）：429 非 credit marker、soft rate limit 文案、上游零字节断流文案（宿主 empty_stream 口径）归为瞬时过载；429 + credit marker 仍判账号耗尽（硬）。
- `recordAccountFailure` 拆软/硬双通道（accountFailover.go）：瞬时过载只做固定 15s 冷却 + 换号（新增 `coolDownAccount`），**不推进连续失败计数、不冻结异常池**；硬失败（credit / 401/403/404/405 / 5xx / transport）维持原语义。
- 流泵零字节断流记账修正（stream.go）：成功分支 `emitted=false`（上游在首个 payload 前关闭流）从「记成功 + 重置 failover」改为按瞬时过载软失败记账（`publishUsage` 记失败 + `noteAccountFailure` 固定冷却）；流关闭行为不变，宿主 empty_stream Retryable 防线继续负责跨账号重试。
- 背景：2026-09-06 生产实证（trae 网关瞬时故障窗口内 4 账号相继零字节断流，宿主跨池兜底成功）。
- 测试：新增 `accountFailover_softfail_test.go`；既有记账测试 429 fixture 改 403 以匹配硬语义。

## 0.9.10

### Fix — HTTP 200 承载的 SSE 业务错误（配额/限流）换号（防御性同构同步）

上游可能把业务错误以 **HTTP 200 + SSE 错误帧**（OpenAI 惯例 `{"error":...}`，嵌套信封的 inner 层）下发，此前流式路径只检查 HTTP statusCode ≥ 400，200 内错误帧会被当作普通 chunk 透传。traework 生产实锤后同构加固（逐函数适配旧版嵌套解包架构，未整文件覆盖）：

- **`accountFailover.go`**：新增统一换号判定 `shouldRotateOnUpstreamErr`（200 走 body marker 分类，其余维持 `isAccountLevel4xx`）。
- **`stream.go`**：新增 `sseErrorFrame` 错误帧提取；`pumpUpstreamStream` 成功分支检测 inner 错误帧，零泄漏（`emitted=false`）+ 账号级命中 + 预算允许时 `evictSessionBindingsForAuth` + `pickNextAuth` + `rebuildRequestWithQoderAuth` 换号续试，已泄漏则透传；`collectUpstreamStreamQoder` 同路径检测换号（已收 chunks 时零泄漏判定）；`aggregateCompletion`/`aggregateQoderSSE` 加 `statusCode` 参数并在 inner 层检测错误帧 fail-fast。
- **`main.go`**：`handleExecExecute` 循环换号判定改用 `shouldRotateOnUpstreamErr`；`doExecuteOnceQoder` 向聚合层透传 statusCode。
- **测试**：`stream_errorframe_test.go` 新增 6 组表驱动测试（含嵌套信封路径）。
- 同构修复同步自 traework-provider 0.1.51（根源修复）/ workbuddy-provider 0.14.22。

## 0.9.9

### Fix — 失败冷却改为固定 15s，不再指数退避（1/3/10 分钟）

账号失败后的冷却窗口从「按连续失败次数指数退避（1/3/10 分钟封顶）」改为**每次失败一律固定 15s**。连续失败计数 count 仍保留并继续驱动异常池冻结（anomaly 阈值默认连续 10 次不变），只是 count 不再拉长冷却时间——路由层更快放行账号参与再次调度，缓解上游限流窗口比分钟级冷却更短时的可用性损失。

- 涉及文件：`accountFailover.go`（删 `failoverTiers` 档位数组，改常量 `failoverCooldown = 15 * time.Second`；`failoverCooldownFor` 固定返回）、`accountFailover_test.go`（断言同步为固定 15s）、`retry_config.go` 注释同步。
- 同构同步自 workbuddy-provider 0.14.21 / traework-provider 0.1.49。

## 0.9.8

### Fix — 面板 API 前缀缺 -provider 后缀导致账号面板全面板 404 空响应

前端硬编码 `const API = "/v0/management/plugins/qoderwork"`（panel.html:259）缺 `-provider` 后缀，与后端 `providerName = "qoder-ai-provider"`（main.go:76）注册路由不一致 → 全部面板 API 打到宿主不存在路由 → 404 + 空 body → 旧版裸 `r.json()` 抛浏览器原生 `Unexpected end of JSON input`，账号面板永远空白。认证文件管理页走宿主 API 不受影响，故"授权成功但面板为空"。

- 涉及文件：`panel.html`（前缀补齐为 `/v0/management/plugins/qoder-ai-provider`）。
- 同步加固：`api()` 对齐 workbuddy 健壮解析——空 body / 非 JSON / 坏 JSON 一律转结构化中文 error（`{error:...}`），404 空 body 不再抛无定位价值的英文异常。

### Feature — 对齐 workbuddy-provider 0.14.20 面板能力（排序 / 搜索 / 导出 / 备份恢复）

1. **积分排序三态**：工具栏「积分 ↕/↑/↓」循环切换（升序/降序/关闭），按 `credits.total_remain`，未知积分按 -1 沉底；排序激活时单卡更新与整页加载均保持有序。
2. **工具栏搜索**：昵称 / 文件名 / UID 大小写不敏感模糊过滤，与区域筛选联动，汇总卡随筛选刷新。
3. **凭据导出**：`GET /export` 返回全部凭据原始物理 JSON（wrapper：`{version, exported_at, plugin, count, accounts:[{name, auth_index, uid, nickname, credential}]}`），前端一键下载 `qoderai-credentials-YYYY-MM-DD.json`；`/export` 虽为 GET 但纳入 `mutatingManagementPath`（携带完整凭据，必须要求 management key）。
4. **备份恢复**：`POST /import-cred` 接受单个凭据 JSON（仅 parseStored 结构校验 + uid 非空，不经上游换 token），原样持久化；导入模态新增「从备份恢复」区，`expandCredentials` 支持完整备份文件 / 凭据数组 / 单凭据三种形态，逐项恢复带进度与失败明细。导出 → 恢复完整闭环。

- 不同步项：workbuddy `/trial`（平台特有，qoderwork 已有对应物 `/claim-pro`）；toast 上限与 err 6s（已一致）；`fetchAndPatchCredits`（功能等价，内联于 `pollRefreshStatus`）。
- 测试：cgo-shim build+vet+test 全绿；panel.html JS 语法校验（2 blocks）+ 10 项功能标记自检通过。

## 0.9.7

### Fix — 删除确认按钮 busy 状态泄漏导致无法连续删除账号

与 workbuddy-provider 0.14.20 同构修复（逐函数适配）：删除账号成功后确认弹窗按钮停留在「处理中…」禁用态且从未复位，弹窗 DOM 静态复用导致下次打开不可点击。

- 涉及文件：`panel.html`（`confirmDeleteAuth()` 成功分支补 `busy(btn,false)`；`openDeleteModal()` 打开时防御性复位按钮）。

## 0.9.5

### Feature — 全量对齐 workbuddy-provider（1-9 项功能同步）

与 workbuddy-provider 0.14.13 功能对齐（同步原则：逐函数适配、纯逻辑文件可整文件复制；SSE 嵌套解包 / COSY 签名等架构差异保留 qoderwork 原样）。

1. **登录轮询重复账号修复**：`oauth.go` `handlePollLogin` 三处成功路径改 `toAuthDataOpts` + `ad.ID=""`（对齐 workbuddy 0.14.12，修复同一文件双 key 重复账号）。
2. **models 配置面板化 + ConfigFields 中文化**：`models.go` 新增 `configuredModels` + `parseModelsConfig`，`usage_config.go` configure() 接入 `case "models"`（配置优先链 config > dynamic > static）。
3. **面板 5 卡片 + 异步刷新前端**：用量汇总 5 卡片口径（剩余可用/不可用/已用/额度池/占比）+ 异步节流刷新前端完整对齐。
4. **账号删除**：`POST /delete` 严格校验链 + 二次确认模态框 + `clearDeletedAccountState` 三键清理。
5. **计数持久化**：`counter.go` 内存累计真相源 + 落盘挂 `preserveWatchdogLoop`（启动 `loadCountersFromDisk` + 每次醒来 `flushCounters`，与 workbuddy 对称）。
6. **session_auth 会话粘性**：`schedulerModeSession` 默认 + `pickSessionAuth(extractSessionKey(req), cands)` 分支 + `evictSessionBindingsForAuth` 四接入点（noteAccountFailure 双路径 / freezeAccountForAnomaly 两分支 / clearDeletedAccountState / preserve 进入）。
7. **usage_feed NDJSON 通道**：`usage_feed.go` 新增（`token-usage-feed.ndjson`），`publishUsage` 8→12 参数（+reasoningEffort / ttftNS / accountLabel / sessionKey），11 处调用点全适配，`sseUsageCollector` 加 `firstByteAt` + `ttftNS`。
8. **保号池 + watchdog**：`preserve.go` / `watchdog.go` 新增（`preserveThresholdDefault=50` / 10m tick / enabled=true），面板保号展示（badge / ftag / 过滤 / 汇总统计）。
9. **ConfigFields 全量对齐**：`usage_feed_enabled` / `usage_feed_path` / `preserve_*` 等声明补齐，Description 中文化。

- 测试：`session_auth_test.go` / `usage_feed_test.go` / `watchdog_test.go` / `auth_delete_test.go` 同步 + 补 qoderwork 缺失 helper。
- 验证：cgo-shim build+vet+test 全绿；双 panel.html JS 语法校验通过。
- 未涉及：traework-provider；workbuddy-provider 本版不改动。

## 0.9.4

### Feat — 账号面板异步节流刷新（与 workbuddy-provider 0.14.9 对称）

- **后端**（`refresh_runner.go` 新增 + `refresh_runner_test.go`）：`RefreshRunner` 单例，1s/账号节流 + `pending/running/done/failed` 状态机。
- **路由**（`management.go` / `credits_handler.go`）：`POST /refresh` 改异步立即返回、新增 `GET /refresh/status`、`GET /credits?track=1` 走队列。
- **幂等**：`EnqueueAll` / `EnqueueOne` 运行中则忽略，多路触发只跑一轮。
- **前端**（`panel.html`）：`pollRefreshStatus` 2s 轮询 + 卡片三态。
- qoderwork 无 preserve watchdog，该步仅 workbuddy-provider 具备。

## 0.9.1

### Fix — `retry_on_4xx` 同请求切号循环纳入 429（Too Many Requests）

与 workbuddy-provider 0.14.2 对称：0.9.0 设计的同请求切号循环只 cover
账号级 4xx（401/403/404/405），429 / 402 / 5xx / 状态 0 全部强制走
cooldown 跨请求路径。`isAccountLevel4xx` 显式纳入
`http.StatusTooManyRequests`，让上游软限流（按账号/租户维度分配）
也可通过切下一个候选账号在**同一个请求**内恢复。cooldown 阶梯
（1/3/10 分钟）继续作用于失败账号，与 retry 循环并存。

- `accountFailover.go`：`isAccountLevel4xx` 加 `http.StatusTooManyRequests`
  case（与 workbuddy-provider 0.14.2 同源）。
- `retry_config.go`：文件头注释新增 429 说明。
- `main.go` (handleExecExecute) 循环注释更新为"401/403/404/405 或
  429"，与代码同步；行为由 `isAccountLevel4xx` 集中控制，调用点不动。
- `accountFailover_test.go`：`TestIsAccountLevel4xx_Classification` 中
  429 由 false 改为 true（与 workbuddy-provider 对称）。
- 涉及文件：`accountFailover.go` / `accountFailover_test.go` /
  `retry_config.go` / `main.go`。
- 不在范围：429 配额感知（上游共享限流时的快速失败信号）；429 → 切换但
  不计 cooldown 的开关（默认行为下 cooldown 一定累计）。

## 0.9.0

### Feature — 异常池（anomaly pool）：连续失败的账号永久冻结 + 每日刷新

新增"异常池"机制：当账号连续触发账号级 4xx（401/403/404/405）、5xx、
429 软限流、402 硬积分或传输错误达 N 次（默认 10，可通过
`anomaly_pool_threshold` 配置，范围 1-50），自动移入异常池、不再被路由
层选到；面板显示"异常"过滤区和单账号/全量"解除冻结"按钮；每日本地 0 点
自动刷新全池（可通过 `anomaly_refresh_enabled: false` 关闭）。

- 新增 `anomaly.go`：内存 `anomalySet` + 物理 auth JSON 顶层布尔
  `anomaly: true` 双镜像（qoderwork 没有 preserve/watchdog 但保留一致的
  顶层字段语义）；`isAnomaly` / `anomalySetPut` / `anomalySetClear` /
  `persistAnomalyToggle` / `refreshAnomalySetFromDisk` /
  `clearAllAnomalies`；`freezeAccountForAnomaly` 在
  `recordAccountFailure` 内 `count >= threshold` 时异步触发。
- `anomaly.go` 内自带 `writeAnomalyFileDirect`（qoderwork 原本只走
  `host.auth.save`，新增直写 helper 是为了 host rebuild 时不丢顶层字段）。
- `anomaly_config.go`：阈值常量与 `clampAnomalyThreshold` / 解析器。
- 整文件同步 `accountFailover.go`（与 workbuddy-provider 同源，保留
  字节级一致以便后续跨插件发版同步）。
- `scheduler.go` 过滤链：`disabled → anomaly → cooldown`；新增
  `isAccountAnomaly` 函数。
- `failover_retry.go`：`pickNextAuth` 加 `isAccountAnomaly` 跳过。
- `active_auth.go`：`pickActiveAuth` / `ensureDefaultActiveAuth` 加
  `isAccountAnomaly` 跳过。
- `usage_config.go`：`configure()` 仿 retry_on_4xx 的 Seen 模式增加
  `anomaly_pool_threshold` / `anomaly_refresh_enabled` 解析。
- `management.go`：新增 `POST /unfreeze` 端点（与 workbuddy 同款）：
  body 含 `auth_index` 则清单个；空 body 则清全部。
- `main.go` ConfigFields 注册两个新配置键；`version` 0.8.2 → 0.9.0。
- `panel.go` wbAccount 加 `Anomaly bool`；`buildDashboardEx` 加
  `anomaly_pool_size` / `anomaly_pool_threshold` / `anomaly_refresh_enabled`。
- `panel.html`：过滤栏新增"异常"tab；每张卡显示 `.badge.anomaly`；异常
  卡增"解除冻结"按钮；工具栏增"全部解冻"按钮；`updateFilterCounts` /
  `applyCardVisibility` / `accountsForFilter` / `renderSummary` 同步支持。
- `anomalyRefreshLoop`（init 启动）：每分钟检测本地 0 点触发
  `clearAllAnomalies`，`lastDay` 防重入；可通过
  `anomaly_refresh_enabled: false` 关闭。
- 不在范围：自动 watchdog 积分检测解冻；session 粘性（qoderwork 仍用
  retry-only 模型）；跨账号聚合指标。
