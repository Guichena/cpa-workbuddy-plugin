# WorkBuddy AI Plugin Changelog

## 0.1.15

### Chore - 定时探活降频至 4 小时并修正失败账号节流缺陷

- 变更要点:
  1. `defaultActivePingInterval` 由 30 分钟放宽至 4 小时：真实推理与探活共享账号调用配额，探活只服务 `test_failed` 标签的健康判定，30 分钟一轮过于频繁。
  2. 修正失败账号节流缺陷：`recordActivePing` 由「仅成功时刷新」改为「无论成败都刷新」。此前失败账号不计时，会在每个 watchdog tick（10 分钟）被反复全量重测，反而比健康账号更频繁地消耗账号配额。
  3. 新增 `TestDoActivePingFailureStillThrottles` 回归用例：断言失败后立即再次调用被节流拦截、不再发起模型请求。
- 涉及文件: workbuddy-ai/active_ping.go、workbuddy-ai/active_ping_test.go

## 0.1.14

### Feat - max_tokens 统一 32768 最低输出底线 + 定时探活改全量模型按序测试

- 变更要点:
  1. 设立 `minMaxTokensFloor=32768` 统一最低输出底线：规格表兜底 `ReasoningMax` 由 8192 对齐至 32768；`computeAdaptiveMaxTokens` 四个出口全部经 `applyMaxTokensFloor` 收敛，杜绝任何路径回落到 32768 以下的过小输出上限。
  2. 广播层同步：`models.go` 的静态模型列表与 `maxOutputTokens()` 输出的 `MaxCompletionTokens` 一并保底。
  3. 定时探活改为全量模型按序测试：废除「随机取 ≤5 个」与整轮预算熔断，`doActivePing` 取该账号全部候选模型依次测试；任一成功即判定健康并立即终止本轮（清 `test_failed`），仅当全部失败才打标签。
  4. 探活负载 `max_tokens` 经 `applyMaxTokensFloor` 抬升。
  5. 新增 `TestDoActivePingAllModelsBeyondLegacyCap` 回归用例。
- 涉及文件: workbuddy-ai/model_specs.go、workbuddy-ai/models.go、workbuddy-ai/active_ping.go、workbuddy-ai/*_test.go

## 0.1.13

### Fix - 修复官方规格自适应 max_tokens 未随 0.1.12 二进制发布（内嵌 vcs.revision 取证）

- 变更要点:
  1. 根因：0.1.12 发布 tag (`workbuddy-ai-provider-v0.1.12`) 指向 commit `5f12b41`，早于自适应引擎提交；构建产物内嵌 `vcs.revision=5f12b41`，二进制符号探针中 `computeAdaptiveMaxTokens` / `model_specs.go` 全部 MISS——线上运行的是旧的写死 128k 实现。
  2. 本版重新打包发布，使 `model_specs.go` 官方规格矩阵与 `computeAdaptiveMaxTokens` 真正进入线上二进制：未传补齐官方最大值，推理模型在 <= 8192 时放宽至 ReasoningMax，超限截断。
  3. 验证方式：构建产物内嵌 `vcs.revision` 必须指向本版提交，且二进制符号探针须命中 `computeAdaptiveMaxTokens`。

## 0.1.12

### Feat & Fix - 设立安全缓冲额度红线 (minRoutableCredits=20)、健康平摊轮询与官方规格自适应 max_tokens

- 变更要点:
  1. 设立安全缓冲额度红线 (`min_routable_credits`，默认 20，可配置)：剩余额度 `< 20` 的账号视为低额保护态，不再分配新推理流量。
  2. 调度策略逆转：废除残血优先（Low-Credit-First）升序排序，在 `scheduler.pick` 初筛中硬排除低额与耗尽账号，存活健康候选平摊轮询（Round-Robin）。
  3. 请求内换号链过滤：`pickNextAuth` 跳过 `remain < min_routable_credits` 账号；`hardCreditMarkers` 补齐 `exceeded the quota` 与 `14018` 触发换号重试。
  4. 按模型官方规格自适应计算 `max_tokens`：引入 `model_specs.go`，未传补齐官方最大值，推理模型在 <= 8192 时放宽至 ReasoningMax，超限截断，根治推理模型 token 耗尽问题。
  5. 面板状态与视觉对齐：新增 `low_credits` 字段与「低额保护」黄色徽标展示。

## 0.1.11

### Feat - 补齐 checkin_auto 配置声明与全套路由与基础设施回归单测

- 变更要点:
  1. 补齐 `main.go` 中 `ConfigFields` 的 `checkin_auto` 配置字段声明，打通宿主管理界面对国际版自动签到调度器的可视化配置。
  2. 全面补充 11 套核心单测资产（涵盖会话粘性路由、低积分优先调度、全灭 defer、计数持久化、Token 保活、测试标签与看门狗）。
- 涉及文件: workbuddy-ai/main.go、workbuddy-ai/VERSION、workbuddy-ai/*_test.go
