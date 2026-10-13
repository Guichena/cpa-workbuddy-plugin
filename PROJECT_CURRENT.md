# 项目当前状态

## 目标与范围

- 目标：维护 CLIProxyAPI (CPA) 的 Go 原生插件套件 `cpa-workbuddy-plugin`（涵盖 7 大 AI 服务商 Provider + 1 统一用量统计插件），提供多账号管理、动态模型发现、流式转发、专属现代管理控制台、扫码/Token 登录、保号巡检与统一 token 用量统计分析服务。
- 范围：仓库内 8 大插件体系的代码实现、单元测试、资产构建、发布流水线、registry 元数据同步等：
  1. `workbuddy-provider`：腾讯 CodeBuddy（国内版与 Global 版）；
  2. `workbuddy-ai-provider`：WorkBuddy AI（独立国际版）；
  3. `traework-provider`：Trae SOLO（字节跳动海外/国内 AI IDE 客户端）；
  4. `qoderwork-provider`：QoderWork（阿里通义灵码衍生国内版）；
  5. `qoder-ai-provider`：Qoder AI（国际版 qoder.com，含 UMID 协议支持）；
  6. `cursor-provider`：Cursor（cursor.sh，Connect-RPC 协议与会话连续性）；
  7. `zcode-provider`：ZCode（BigModel Coding Plan，Anthropic 兼容与客户端真实指纹 mimic）；
  8. `workbuddy-token-usage`：统一 Token 用量统计与实时看板服务。
- 非范围：CLIProxyAPI 核心服务本体；跨项目文件修改。

## 项目概况

- 状态：活跃维护中。8 大插件全景全量编译与单测验证全绿（cgo-shim 零错误）。已彻底废除并删除 Gemini 插件（因依赖庞大导致二进制膨胀翻倍、GCP Project 门槛过高、缺乏调度与管理面板）。最新发布/就绪版本：
  - `zcode-provider`: **0.1.22**（纠正「激活开通」错误承诺，改为如实的 Start Plan 领取指引；0.1.21 定时探活全量按序 any-success + max_tokens 32768 底线 + 官方规格矩阵全扩）
  - `cursor-provider`: **0.1.6**（修复 Query 参数反序列化丢失导致真实发现模型被屏蔽、建立点号模型别名归一化根治上游 not_found、测试弹窗支持实时搜索过滤与秒开交互）
  - `qoderwork-provider`: **0.9.35**（max_tokens 统一 32768 最低输出底线；0.9.34 上游 110 日调用上限 24 小时持久化冷却 + 路由/换号排除 + 面板解冷；签到 10:00/22:00；移除自动探活与 test_failed）
  - `qoder-ai-provider`: **0.1.15**（max_tokens 统一 32768 最低输出底线；0.1.14 上游 110 日调用上限 24 小时持久化冷却 + 补齐 /clear-cooldown 路由注册；签到 10:00/22:00；移除自动探活与 test_failed）
  - `traework-provider`: **0.2.11**（max_tokens 32768 底线 + payload 层同步；定时探活改全量模型按序测试；0.2.8 测试探活流式首包即停 + Keep-Alive TLS 预热，拦截 HTTP 200 下 `event: error`）
  - `workbuddy-provider`: **0.15.8**（max_tokens 32768 底线；定时探活改全量模型按序测试；0.15.4 移除保号池、硬排除测试标签 + 低积分优先）
  - `workbuddy-ai-provider`: **0.1.14**（max_tokens 32768 底线；定时探活改全量模型按序测试；0.1.9 补齐完整路由层单测资产、国际版签到）
  - `workbuddy-token-usage`: **0.2.8**（0.2.8 让退役实例的 feed 导入协程在连续 3 轮关闭句柄后自行退出；0.2.7 修复宿主配置重载换句柄窗口导致的用量记录丢失；0.2.6 移除四个图表与导出图片、下线 `/stats/trends` 及序列聚合以省算力与响应体积；0.2.5 起请求明细与维度汇总展示原始 provider id）
- 活动工作区：F:\cpa-plugin
- 当前时间：2026-10-13 (GMT+8)

## 活动会话进展摘要

- 当前会话（2026-10-14）：**zcode-provider 0.1.22：纠正「激活开通」错误承诺，改为如实的 Start Plan 领取指引并全链路发布生产**：
  - 需求背景：用户拍板只做「📖 领取指引」形态——授权登录本身就已完成账号绑定，每日额度的领取是官方在 ZCode 客户端内的独立一步，插件不该承诺「点击即下发额度」。
  - 根因实证（推翻上一轮「激活」语义）：官方每日 800 万 Start Plan 并非授权登录时自动下发，而是由 ZCode 桌面客户端内的服务端活动弹窗引导用户**手动领取**（`POST /api/v1/zcode-plan/billing/claim`），该接口强制校验阿里云人机验证头（`X-Aliyun-Captcha-Verify-Param`，`captcha.enabled=true`，sceneId `11xygtvd`）；无头 / 伪造验证头 / 换 token 一律 `400 3007 captcha verify failed`，网页端与插件端均无领取入口。另实测推翻旧结论：`authorize` 层不做 redirect_uri 白名单（三臂候选均 307 通过）。
  - 实施（v0.1.22，仅 5 个 zcode 文件）：
    1. 卡片未领取态徽标「未激活」→「未领取」，按钮「⚡ 激活开通」→「📖 领取指引」（`data-action="guide"`）；
    2. 新增 `guideModal` 领取指引弹窗（官方领取四步 + 「刷新额度」复查），移除失效的 `activateAccount` 一键下发交互；
    3. `handleActivatePoll` 与 OAuth 弹窗成功文案统一为「授权登录成功」语义，`quotaHTML` 未领取文案同步；
    4. 面板脚本语法、事件链（action↔handler）静态校验全通过；`python scripts/cgo-shim-build.py zcode` build/vet/test 全绿。
  - 发布与生产闭环：
    1. **提交链**：`9655225`(fix 5 文件) → CI run `38439920222` 64/64 success → `d859616`(8 assets) → `2d6d7f8`(registry) → `0cd3060`(prune 0.1.21)；
    2. **远端验证**：0.1.22 七平台 artifacts sha256+size 7/7 ALL PASS；远端 0.1.22 raw URL 200、0.1.21 已 404；
    3. **生产热重载**：`plugin-store/zcode-provider/install?version=0.1.22` → `status=installed, restart_required=false`；宿主日志 `plugin hot reloaded active_version=0.1.22 retired_version=0.1.21`，**零容器重启**（容器 Up 4 hours）；落盘 `.so` sha256 `d5d10e85…` 与本地 linux_amd64 zip 逐字节一致；
    4. **生产验收**：`/v0/resource/plugins/zcode-provider/panel` 200（70,280 字节）且新文案生效（`领取指引`×7、`guideModal`×7、旧文案 `激活开通`/`activateAccount` 均为 0）；accounts 200（3 账号）。
  - 说明：上一轮 0.1.21 已由并行会话发布完毕（registry/GitHub release/release-assets 三处一致），故本轮直接发 0.1.22，无版本冲突。

- 上一会话（2026-10-13）：**六插件 max_tokens 统一 32768 最低输出底线 + zcode 规格矩阵全扩 + 四插件定时探活改全量按序并全链路发布生产**：
  - 需求背景：① 用户要求收口「六插件兜底 ReasoningMax 8192 与 traework 20000 口径不一」并对齐；② 进一步明确「所有模型最小 max_tokens 都应是 32768，这是底线」；③ 定时探活由「随机取 ≤5 个模型」改为「全量模型按序测试，任一成功即健康、不再打 test_failed，仅全败才打标」，且 qoder 两插件不需要探活、zcode 需新增探活。
  - 架构设计与实现：
    1. **统一最低输出底线**：六插件 `model_specs.go` 新增 `minMaxTokensFloor=32768` 常量与 `applyMaxTokensFloor` 纯函数；兜底 `ReasoningMax` 8192→32768；`computeAdaptiveMaxTokens` 四个出口（未传补足 / 保守放宽 / 超限 clamp / 区间保留）全部经 floor 收敛；广播层 `models.go` 的 `MaxCompletionTokens` 与 traework `upstream.go` payload 层同步保底；
    2. **zcode 规格矩阵全扩**：规格表由 14 条扩至 64 条（覆盖 GLM 4.5/4.6/4.7 全系 + Claude/GPT/Gemini/DeepSeek/Qwen 通用家族），匹配逻辑改为最长前缀优先并支持 `vendor/` 前缀剥离；
    3. **定时探活全量按序**：workbuddy / workbuddy-ai / traework 废除「随机 ≤5 + 30s 整轮预算熔断」，`orderedAllWorkbuddyModels` / `orderedAllTraeModels` 返回全量候选按序测试，任一成功即终止并清 `test_failed`，全败才打标；zcode 新增定时探活（30 分钟节流 + 20s 单请求超时 + 全量按序 any-success），抽出 `pingUpstreamOnce` 纯传输内核供面板测试与定时探活复用，避免调度探活污染用量/冷却；
    4. **测试**：新增 `TestDoActivePingAllModelsBeyondLegacyCap`（三插件）与 zcode `TestDoActivePingAllModelsInOrderAnySuccess` / `...FailTagsTestFailed`、`TestResolveOfficialModelSpec_Extended`；六插件 `cgo-shim` build/vet/test 全绿。
  - 发布与生产闭环：
    1. **一轮提交发布**：`7ec23c0`(feat 52 文件) → `0aaaa65`/`b2b82e7`(assets) → `02e7375`(registry) → `82e437f`(prune 6 个旧版本)；
    2. **CI 处置**：六插件并发 dispatch，workbuddy-provider 首个 run 因 `token-usage-tracker` 既有 flaky 测试（`TestFeedImporterLoopSurvivesHandleSwapAndResumes`，本轮未改该目录、本地全绿）失败 → 单插件重跑成功；其余 5 个 run 一次通过；
    3. **远端验证**：六插件各 7/7 artifacts sha256+size ALL PASS，无旧版本残留；
    4. **生产热重载**：六插件 `plugin-store install` 均 `restart_required=false` 且日志确认 `plugin hot reloaded`（active_version 全为目标版本、retired 为上一版），零容器重启；落盘 `.so` sha256 与本地 release zip 逐字节一致；
    5. **生产健康**：六插件 accounts/panel 全 200。
  - 并行会话保护：`scheduler.go`（High-Credit-First 注释）等为其他会话在途改动，按「非本轮代理产生的改动默认保护」只提交本轮文件（路径限定 `git commit -- <路径>`）；`zcode/panel.html`/`management.go` 及 0.1.22 bump 当时属在途，已于 2026-10-14 由本插件会话完成并发布（见上）。

- 上一会话（2026-10-13）：**Qoder 110 日调用上限 24 小时持久化冷却引擎实现、两插件镜像同步与全链路发布生产部署（qoder-ai-provider 0.1.14 / qoderwork-provider 0.9.34）**：
  - 需求背景：Qoder 服务商特殊性——积分余额与每日请求调用次数相互独立，满积分账号仍会因突发/高频调用触发 `110: Billing daily count exceeded`（区别于本仓库其他 provider 的额度模型）。用户明确要求：账号触发 110 后进入 24 小时持久化冷却、排除出路由与换号、仅自然到期解除（成功推理不清除），面板展示剩余时间徽标并支持人工解冷。
  - 架构设计与实现：
    1. **110 精确识别**：`policy.go` 新增 `isDailyCountExceeded(status, body)`，覆盖全小写文本 / `"110"` / `code: 110` / `code:"110"` / `110:`，避免误伤正常业务帧；
    2. **持久化冷却引擎**：新增 `daily_cooldown.go`——`daily_cooling_until`（RFC3339）经 `persistAuthDirect` 原子落盘物理 auth 文件，内存镜像 `dailyCoolingMap` + `normalizeFailoverKey` 双向归一化键检索，`refreshDailyCoolingSetFromDisk` 由 watchdog tick 定期重建；
    3. **路由与换号绝对排除**：`isAccountCoolingDown` 首位接入 `isAccountDailyCooling`，联动 `scheduler.pick` / `pickNextAuth` / 会话粘性；`resetAccountFailover` **刻意保留**每日冷却，守「仅到期自然解除」不变量；
    4. **全链路触发**：`stream.go`（HTTP 4xx、SSE 200 业务错误帧、Qoder SSE 收集）与 `main.go`（换号重试与终态错误）均触发 `triggerDailyCooldown`；
    5. **面板与解冷接口**：新增 `POST /clear-cooldown`（`handleClearCooldown`），`buildDashboardEx` / `/credits` 输出 `daily_cooling` / `daily_cooling_until` / `daily_cooling_remain`；前端新增「冷却」筛选标签、`110冷却(HH:MM)` 徽标与带二次确认的「解冷」按钮；
    6. **测试**：新增 `daily_cooldown_test.go` / `daily_cooling_routing_test.go`，两插件 `cgo-shim` build/vet/test 全绿。
  - 发布与生产闭环：
    1. **发布链**：`0c4511a`(feat) → CI `38409285358`/`38409280524` success → `ce1589f`(assets) → `5516422`(registry) → `af359f9`(0.1.14 fix) → CI `38413314321` success → `496c0c0`(assets) → `ca9439f`(registry) → `904ad5d`(prune 0.1.13)；远端 raw URL 两插件各 7/7 哈希 ALL PASS，无旧版本残留；
    2. **发布中发现并前向修复 0.1.13 缺陷**：qoder-ai 的 `managementRegistration().Routes` 漏登记 `clear-cooldown`（qoderwork 已登记）→ 宿主按注册表分派，未声明路径被宿主层拦截返回**空 body 404**（与插件层 JSON 错误可区分）→ 补登路由发 **qoder-ai 0.1.14**；
    3. **生产热重载**：两插件 `plugin-store install` 均 `restart_required=false` 且热重载生效（qoder-ai active=0.1.14 retired=0.1.13；qoderwork active=0.9.34 retired=0.9.33），零容器重启；落盘 .so sha256 与本地 zip 一致（`585de9f1…` / `ad1dcd06…`）；
    4. **生产验收**：两插件 accounts/panel 均 200，`clear-cooldown` 返回插件层 JSON 200，`daily_cooling` 字段已现身生产 accounts；
    5. **并行阻断处置**：验证期间发现并行会话未完成的 `model_specs.go`/`models.go` 改动致 `cgo-shim` 失败，按分离发布规则移走隔离验证（证明失败与本轮修复无关），发布完成后原样恢复。

- 当前会话（2026-10-13）：**按模型官方规格自适应放宽与截断 max_tokens 彻底根除长思考链 8192 截断缺陷（workbuddy 双插件完成与回归）**：
  - 故障现场：用户在 `deepseek-v4.1-flash 高` 思考强度下发生工具调用失败与 57s 长卡顿；底层用量排查发现 `output_tokens: 8192`、`reasoning_tokens: 8192`，正文 0 token 被上游 `finish_reason: length` 截断；new-api 记录 `上游没有返回计费信息，无法扣费`；
  - 根因定位：Qoder 客户端在前端代码中将自定义模型 `max_output_tokens` 写死为 `8192`；CPA 宿主原样透传给上游；长思维链在超长上下文下产生上万字思考，吃光 8192 配额导致正文未输出即被掐断；
  - 架构升级（拒绝一刀切，采用官方规格自适应模型表）：
    1. 新增 `workbuddy/model_specs.go` 与 `workbuddy-ai/model_specs.go`，内置对齐宿主 `models.json` 及各厂商官方文档的权威规格表（覆盖 DeepSeek、GLM、Kimi、Qwen、Claude、GPT、Gemini、MiniMax、Hunyuan 等全主流家族）；
    2. 针对思考/推理模式（`reasoning_effort != ""` 或推理模型），自动将客户端保守过小的 8192 提升到模型官方推理上限（DeepSeek 64k、GLM 128k、Claude 64k、Qwen 32k 等）；
    3. 支持智能安全区间截断（clamp），当客户端请求超大值时自动收敛至官方上限避免上游网关返回 400；
    4. 动态模型元数据保底对齐官方规格；双插件单测 100% PASS 并完成提交推送。

- 上一会话（2026-10-12）：**ZCode 0.1.15 新增插件自带账号激活功能开通每日额度（无需客户端登录，全链路发布与生产热重载验收通过）**：
  - 用户反馈与目标：用户指出新导入的账号（如「ZCode 旅行者1463」）只有做激活动作才会有每日额度出现，现在刷新额度没有激活效果；官方需用该账号在客户端登录触发激活，希望插件自带激活账号功能，不需要客户端登录，新账号就能刷新出每日额度。
  - 核心排查与逆向锁定：
    1. 逆向客户端 `out/host/index.js` 与 `out/renderer` 确认官方激活链路：通过 `POST https://zcode.z.ai/api/v1/zcode-plan/billing/claim` 激活 `zcode-v3-start-plan`，上游下发每日 800 万额度；
    2. 上游要求安全验证：直接调 claim 端点报 `code: 3007, msg: "captcha verify failed"`，由阿里云智能验证码 SDK 产出 `X-Aliyun-Captcha-Verify-Param` 并以新加坡区域 `X-Aliyun-Captcha-Verify-Region: sgp` 请求。
  - 实施修复与系统实现（v0.1.15）：
    1. **后端新增 `POST /claim` 端点**：调用官方 claim 开通套餐，支持注入验证码参数与设备指纹；当上游要求验证时返回结构化配置；激活成功自动清除旧缓存并拉取最新额度，动态同步 Models 字段；
    2. **刷新额度静默激活联动**：在后台刷新或拉取额度遇新账号无生效计划时，后端自动先尝试一次静默激活，免验证时直接回填每日额度；
    3. **管理面板未激活识别与一键激活开通**：卡片识别未开通状态并展示「未激活」Badge，提供「⚡ 激活开通」高亮按钮；点击或单卡刷新未开通时，按需动态加载阿里云验证码 SDK 完成无感/滑块验证并自动调用激活，无需安装登录桌面客户端；
    4. 新增 2 项套餐激活单元测试并通过哨兵反证。
  - 生产热重载与实测验收：
    - `python scripts/cgo-shim-build.py zcode` 全绿通过；CI Run `38286802545` 65/65 jobs 全绿打包；8 资产下载校验 `ALL CHECKSUMS OK`；registry 同步、远端 raw URL 7/7 200 验证；prune 0.1.14；
    - 生产服务器调用 `plugin-store install zcode-provider?version=0.1.15` 热重载生效（`restart_required: false`）；
    - 生产实测调用 `POST /claim` 返回 200 及结构化验证参数；面板静态资源回读确认「⚡ 激活开通」与 `activateAccount` 均已就绪。

- 当前会话（2026-10-12）：**ZCode 0.1.14 全量修复用量管道、模型别名解析与额度刷新稳定性（全链路发布与生产热重载验收通过）**：
  - 用户反馈与目标：
    1. 「zcode测试成功, 但是token用量面板其实没有识别到请求, 是不是说明是请求推理失败的?」
    2. 「但是我们用GLM-5.3是可以测试成功出请求记录的, 但是我们用glm-5.3就不行了。」
    3. 「并且我们的ZCode 旅行者2435是有额度的, 有时候确刷新不出额度。」
    4. 「zcode的差距需要全量测试一下, 还是有很多问题的。」
  - 核心根因锁定（实测铁证）：
    1. **用量面板无记录**：`usage_feed.go` 将日志写到孤立路径 `/root/.antigravity_cockpit/data/` 且 JSON 缺少 `tokens` 嵌套与 `source` 账号标签，未被 `token-usage-tracker` 消费；面板 `/test-active` 未接入上报。
    2. **小写模型名与别名 400 失败**：`main.go` 误声明 `ExecutorModelScopeStatic` 导致宿主 OAuth 模型别名完全未绑定到插件；且插件内部缺失 `cacheModelAliases` / `resolveUpstreamModel`，未将小写 `glm-5.3` 归一化为上游 Anthropic 端点识别的权威大写 `GLM-5.3`。
    3. **额度刷新频控与卡顿**：前端后台轮询收尾时二次调用 `fetchAndPatchQuota` 带有 `&force=1` 重复轰炸上游频控；后端缺少旧有效快照兜底；未获取时永久转圈假死。
  - 实施修复与系统实现（v0.1.14）：
    1. **重写用量流管道**：自动探测宿主数据目录 `<CLIProxyAPI root>/data/token-usage-feed.ndjson`，对齐完整嵌套 Schema、账号昵称 `source` 与 128MB 截断；在 `/test-active` 探活成功/失败时完整埋点。
    2. **模型别名反向解析与 GLM 大小写归一化**：`main.go` 改为 `ExecutorModelScopeOAuth`；`models.go` 接入 `cacheModelAliases` / `resolveUpstreamModel`；实现 `normalizeOfficialGLMModelID` 自动将 `glm-` 开头的小写模型规范化为官方标准大写；`MethodModelForAuth` 自动剥离 `.json` 后缀并从生效套餐 entitlements 动态发现模型。
    3. **优化流式换号重试与退避**：`handleExecStream` 增加 3 次换号重试；换号判据纳入并发超限（`3009/concurrency`）；退避冷却优化为 15 秒固定冷却。
    4. **额度刷新稳定性**：前端轮询收尾改为读取缓存；`quota.go` 增加旧缓存兜底；OAuth 落盘与导入后自动加入刷新队列；优化前端空态提示。
  - 门禁与发布验收：
    - `python scripts/cgo-shim-build.py zcode` 与 `token-usage-tracker` 全部 all green，新增 `models_alias_test.go` 并经受必失败哨兵反证；`node` 前端语法检测全绿；
    - CI Run `38271303121` 65/65 jobs 全绿，生成 Release 资产；下载 8 资产校验 `ALL CHECKSUMS OK`；registry 同步并通过校验；远端 raw URL 7/7 平台 200 且哈希一致；prune 0.1.13；
    - 生产服务器调用 `plugin-store install zcode-provider?version=0.1.14` 热重载生效（`restart_required: false`，零容器重启）；
    - **生产全链路实测验收通过**：
      1. 面板探活「ZCode 旅行者2435」返回 200，用量日志与 Token 用量统计面板 `/requests` 立刻出现 `Source: ZCode 旅行者2435, Model: GLM-5.3` 真实记录；
      2. 生产网关 `/v1/models` 成功注册 `glm-5.3` 与 `glm-5.3-flash` 归属 `zcode-provider`；请求小写 `glm-5.3` 和 `glm-5.3-flash` 稳定以流式打字机吐出思考链并返回 200；
      3. `GET /credits` 成功拉取「ZCode 旅行者2435」每日 800 万额度（GLM-5.3 300万 + GLM-5.3-Flash 500万），`POST /refresh` 2 秒内全部 `done`，首屏 accounts 自动附带完整 quota。
  - 核心根因：上游对**同账号突发调用**限流，在 HTTP 200 下返回业务码 `110: Billing daily count exceeded`。自动探活每轮 5 模型轮测 + 每账号 10 分钟节流，一天上千次真实推理调用稳定触发限流，并挤占真实推理的账号配额。（**修正上一轮“日配额耗尽”误判**：实测健康账号连发 20/20 成功、同账号 ok/bad 交替，属突发限流而非日额度打满。）
  - 实施落地（两插件镜像，逐函数适配）：
    1. **移除自动探活全链路**：删除 `doActivePing`（多模型轮测）、`triggerActivePing`、30 分钟节流状态、`refresh_runner.doFetchOne` 触发点与 watchdog 探活语义；后台只拉取额度快照与活动状态。**保留面板「测试」按钮的单次手动调用**（用户主动点击才发 1 次）。
    2. **完整下线 `test_failed` 机制**：打标/清标唯一宿主在自动探活内，探活移除后存量标签永久无法清除、账号会被永久排除出调度。故一并删除机制本体 `test_failed_tag.go`（+测试）、三处调度排除判据（`scheduler.go` / `active_auth.go` / `failover_retry.go`，`accountRoutable` 现只剩 `!cooling`）、`panel.go` 字段与 `panel.html` 「测试」筛选标签；启动时由 `stripAnomalyKey` 剥离存量 `test_failed` 标签。
    3. **签到时间收敛**：`checkinHours` 由 `[0,4,8,12,16,20]` 改为 `[10,22]`；新增 `shouldRunCheckinNow` 门控——`nextCheckinTime` 取 checkin 与 keepalive 并集唤醒，不门控会让签到在 keepalive 时刻（04:00/16:00 等）误触发。面板文案与 `schedule` 字段同步为 10:00/22:00。
  - 验证全绿：`cgo-shim-build.py` 两插件 build/vet/test 全绿（各 ~7.0s）；`checkin_schedule_test.go`（新增，两插件逐字相同）经**哨兵变异**（`checkinHours` 回退 6 槽）后 4 用例精确 FAIL，证明真实进编译；6-review `STYLE: PASS`；`POLLUTION: PASS`。
  - 文档：`doc/3-实施/2026-10-12_220000_Qoder去掉自动探活与签到时间调整实施总览.md`、`doc/4-bugs/2026-10-12_214500_Qoder上游110每日计费次数上限致账号误判测试失败根因诊断.md`、`doc/5-tests/2026-10-12_230000_*`、`doc/6-review/2026-10-12_231000_*`。
  - 待办：CI dispatch → 下载 assets → registry 更新 → 生产 `plugin-store install` 热重载 → 观测无新 `active ping` 日志、签到仅在 10:00/22:00 触发。

- 当前会话（2026-10-12）：**qoder-ai-provider 0.1.9 / qoderwork-provider 0.9.30 修复「上游 HTTP 200 顶层 code 排队帧未被识别致 new-api 调用 qwen3.8 系列 60s 挂死」**：
  - 用户反馈与目标：用户报「还是不能从 new-api 请求 cpa 的 qwen-3.8-flash、qwen-3.8 模型，但 cpa 本身测试可以」；拍板「两个插件都改，行为都选立即报错」。
  - 根因（数据面解析器侧，上一轮仅收敛到探活侧）：`parseQoderSSEChunk` 只认 `[DONE]`/嵌套 `body`/扁平 `choices`·`id`/`error` 四类形态，阿里 Qoder 对超上下文请求在 HTTP 200 下返回的顶层 `code:"403"` 帧（内嵌 `10605/isQueued:true/retryAfterSeconds:30`）落入兜底被 `continue` 跳过 → 读不到终止信号死等 Keep-Alive 约 60s 后对端 `INTERNAL_ERROR`；面板探活因 prompt 极小不触发排队 + 首字节盲判而显示成功，形成「测试通过、真实不可用」假象。
  - 生产直测判据：同一模型名仅 prompt 长度不同行为相反（`hi` 1.47s / 67KB 3.49s 通过；112KB **3/3 稳定挂死 60.5s**）；挂死响应体首行即排队帧 + 3 条 `: keep-alive`；`strings <so>|grep isQueued` 无命中确认线上无识别逻辑。排队性质经 feed 259 条确认为**请求级 + 时段性**（同账号 ok/bad 混合、成功请求 input 达 289,424）。
  - 实施：解析器增第 4 类顶层 `code` 形态；`sseErrorFrame` 增顶层 `code`+`message` + 新增 `unwrapNestedMessage` 逐层展开（保留首层业务码）；`pumpUpstreamStream`/`collectUpstreamStreamQoder`/`aggregateQoderSSE` 三路径循环内即刻收尾；`sendActivePingQoder` 首帧内容校验。行为统一「立即报错」（`shouldRotateOnUpstreamErr` 对排队帧天然 false，不换号）。
  - 门禁：两插件 `cgo-shim-build.py` build/vet/test 全绿；新增 `stream_queue_frame_test.go`（`hangingReader` 模拟 Keep-Alive），**哨兵反证移除识别分支后该用例耗时 10.00s 精确复现挂死**；对抗验证（正文含 code 字样不误判）；6-review `STYLE: PASS`；`POLLUTION: PASS`；两插件四个关键函数逐字同构（md5 两两相同）。
  - 文档与沉淀：Bug 主文档 `doc/4-bugs/2026-10-12_001641_*`、测试主文档 `doc/5-tests/2026-10-11_164802_*`、6-review `doc/6-review/2026-10-11_164802_*`；skill `project-cpa-workbuddy-plugin-sse-and-ping-optimization-rules` 规则一新增契约；知识库 `HTTP200不等于流式协议成功-有效终止契约.md` 追加 2026-10-12 补充。
  - 提交：`62c16a5`（代码+文档）、`d433e9f`（skill）、`f20dbcc`（记忆）、`c1af36e`（测试基线）、`ec145de`（assets）、`b9863ff`（registry）、`a31e8b2`（prune）已推送 main。
  - **发布与生产验收（已完成）**：CI run `38157167623`/`38157165743` 均 success，tag 与 Release 各 8 资产；registry 同步 0.1.9/0.9.30、远端 14/14 平台 size+sha256 ALL PASS；生产 `plugin hot reloaded active_version=0.1.9 retired_version=0.1.8` / `0.9.30 retired=0.9.29`、**零容器重启**、落盘 `.so` sha256 与本地 zip 一致（`8fd3aa13…` / `9758ca2e…`）、accounts 200；修复后 112KB prompt 由 **60.57s 挂死 → 3.90s 正常返回**，`qwen-3.8-flash`/`qwen-3.8` 经渠道 key 直测均 200（1.9s）；探活侧已实证能如实报 `upstream 200: 112:` 失败（旧版盲判成功）；prune 清理 0.1.8/0.9.29（约 40.6 MB）。
  - 局限：验收期间上游未排队，排队帧路径的「快速报错」未在生产自然复现，由本地哨兵反证（10.00s 精确复现）+ 真实帧复刻覆盖；待排队窗口回看日志确认。

- 当前会话（2026-10-11）：**彻底废弃并删除 Gemini 插件（gemini-provider），全套配置与文档对齐 8 大插件体系**：
  - 用户反馈与决策：用户指出当前 Gemini 插件体积极大（动态库达 14.4MB，比含全套 UI 的 WorkBuddy 还要大 80%），且在实际使用中几乎不可用（强制要求带有 Cloud Project 与结算绑定的 GCP 开发者账号，缺少 Web 管理面板与容灾调度）；经评估确认其投入产出比过低且严重违背轻量化原则，决定彻底放弃并不再维护该插件。
  - 清理与实施动作：
    1. **源码与发布资产清理**：使用 `git rm` 彻底删除 `gemini/` 源码目录、`release-assets/gemini-provider-0.1.1/` Release 资产目录，以及 `assets/icons/Gemini.png`、`assets/icons/Google.png` 图标文件；
    2. **元数据与流水线同步**：从 `registry.json` 中移除 `gemini-provider` 插件配置（插件源总数从 9 个回退至 8 个）；从 `.github/workflows/build.yml` 中彻底剔除 `gemini` 的构建、交叉编译、Release 触发与 DISPLAY 映射；
    3. **文档与规范全面更新**：更新 `README.md`、`PROJECT_CURRENT.md`、`PROJECT_MEMORY.md`、`PROJECT_HISTORY.md` 与《九大插件全景架构与演进计划总览》，明确记录 Gemini 插件的废弃与移除历史，更新全景矩阵为 8 大插件体系；
    4. **全量回归验证**：`validate-registry.py` 校验 8 插件通过，全量兄弟插件 `cgo-shim-build.py` 编译与单测全绿。

- 当前会话（2026-10-11 至 10-12）：**ZCode 授权从误抄 API Key 重构为账号授权，再根治为官方 CLI 轮询 OAuth 链路（0.1.5 → 0.1.8）**：
  - 0.1.5：反编译 `D:\zcode\resources\app.asar`（ZCode 3.15.1）锁定官方 OAuth 契约（授权页 `chat.z.ai/auth`、客户端 ID `client_P8X5CMWmlaRO9gyO-KSqtg`）；`StoredAuth` 扩展 token/access_token/user 并保持 `sa.APIKey` 向下兼容；发布链 `d92c37b`→`24570ac`→`ef6523d`→`d6f23c2`，生产热重载生效。
  - 0.1.8：改用官方 CLI 轮询链路（`POST /api/v1/oauth/cli/init` + `GET /api/v1/oauth/cli/poll/<flow_id>`），`handleStartLogin` 原样透传服务端签发的 `authorize_url`（不改写 `redirect_uri`），`handlePollLogin` 轮询取结果，移除本地回调桥接；发布链 `fb24235`→`4e8a925`→`9362c3f`→`9171562`，生产 0.1.8 热重载、落盘 sha256 `aa82c93a…` 一致。
  - 知识沉淀：知识库《Z.ai-ZCode-OAuth授权契约-CLI轮询模式》+ 项目记忆 `feedback-cpa-oauth-redirect-uri`。
  - **2026-10-14 更正**：当时记的「`chat.z.ai` 对 `client_id` 执行 redirect_uri 严格白名单」经三臂实测推翻——`authorize` 层三个候选 `redirect_uri` 均 307 通过，白名单不在该层；真因是桌面端 deep-link 会唤起本地客户端、CPA 拿不到授权码，故仍应复用官方 CLI 轮询链路（结论不变，因果已修正）。

- 当前会话（2026-10-11）：**Qoder 403 首包假成功与全链路流式诊断，完成生产 Docker Nginx proxy_buffering off 重载验证**：
  - 根因（实测铁证）：① 阿里 Qoder 上游对 `qfmodel`/`qmodel_38max` 在 HTTP 200 下返回 `data: {"code":"403",...10605...isQueued:true...}` 业务报错，且返回后连接不关闭、持续 `: keep-alive` 僵死 60s 后上游 `INTERNAL_ERROR`；② CPA 面板探活仅盲读前 512 字节不校验内容即判成功（219ms 假成功）；③ Token 用量插件 TTFT 334ms 恰为错误帧到达时间，实为 `failed:true`/`latency_ms:60100`/`total_tokens:0`；④ New-API 渠道测试按钮强制 `stream:false` 不能用于流式验证。运维闭环：生产 `docker exec nginx nginx -t` PASS + `nginx -s reload` 平滑重载（已含 `proxy_buffering off; proxy_cache off;`），外网 `api.luode.vip/v1` 实测 1.35s 端到端流式；经验固化到项目 skill `project-cpa-workbuddy-plugin-sse-and-ping-optimization-rules`（首包业务报错帧白名单校验 + 全链路流式不缓冲配置与推理延迟判读规范）。

- 当前会话（2026-10-11）：**deepseek-v4.1-flash 推理断开根因诊断 + traework 补齐 oauth-excluded-models 治理入口（修复随 traework-provider 0.2.7 上线，现承载于 0.2.8）**：
  - 用户诉求：`new-api → CPA(https://api.luode.vip/v1) → 服务商` 链路上 `deepseek-v4.1-flash` 推理会断开，要求先定位原因并修复，再继续 Token 用量插件（图表移除）任务。
  - 根因（三层叠加，证据见 `doc/4-bugs/2026-10-11_212700_deepseek多服务商同名认领导致推理零首字节断开根因诊断.md`）：
    1. 同名裸模型多服务商认领：`deepseek-v4.1-flash` 由 workbuddy-provider 与 traework-provider 同时动态认领（生产日志 14:06–19:20 内该模型 163 次绑定到 `traework-*.json`），且 traework 是唯一未实现 `filterExcludedModels` 的 OAuth 插件（workbuddy / workbuddy-ai / qoder-ai / qoderwork 均已实现），导致宿主 `oauth-excluded-models` 对它只生效一半——宿主只在 `model.for_auth` 按账号注册路径过滤（`applyExcludedModels`），`model.static` 返回列表被原样登记进全局模型目录（`internal/pluginhost/adapters.go` 静态注册不过滤）。
    2. 会话亲和钉死故障服务商：Trae 号池配额枯竭（16 次 `upstream 200: Your requests have exceeded the quota`）后仍被同一会话反复选中，宿主失败后重绑仍落到另一个 Trae 账号。
    3. 零字节长挂：`traework/host_bridge.go:283 hostBridgeOpenTimeout=30s` 与降级直连 `ResponseHeaderTimeout=30s`（`:430`）**串行**相加，单号最坏 60s 完全不产出字节，而 `handleExecStream`（`traework/executor.go:316`）已先把响应头返回宿主 → 客户端只看到"连上但零输出"。new-api 侧 4 次断开全部 `end_reason=client_gone`、`frt=-1000`、`completion_tokens=0`，耗时 69/80/103/104s；对照成功请求首字节仅 3.7–5.5s。
  - 本轮修复：`traework/models.go` 新增 `filterExcludedModels` 并在 `handleModelStatic` + `handleModelForAuth` 全部返回出口应用（未配置时原样返回，零行为变化；返回新切片避免污染动态缓存）。新增 `traework/models_excluded_test.go` 5 例，含"排除项永不命中"语义变异反证。`gofmt`/`cgo-shim-build.py traework` 全绿，6-review `STYLE: PASS`。
  - 文档：Bug 主文档 + 测试主文档 + 6-review 已落盘；知识库笔记 `20-Knowledge/工程实践/CPA同名模型多服务商认领治理与零首字节断开判读法.md`（含判读法与 `PATCH /v8/management/oauth-excluded-models` 安全写入路径）。
  - 现状：症状已随 Trae 流量退去自然平息（近 2h 该模型 599 次全部 workbuddy-provider、`client_gone` 0 次），但同名认领风险未消除。**0.2.7 发布与生产上线已完成**（后续 0.2.8 由并行会话发布并携带本修复，已在生产 `.so` 内确认 `filterExcludedModels` 存在）：CI run `38143777373` success、registry 同步、生产 `plugin-store install` 热重载 `registered version=0.2.7`、落盘 `.so` sha256 `23ea904c5bec…` 与产物一致、零容器重启、accounts 200（7 账号）、无 panic/fuse。
  - 待办（需用户拍板，已扩为批量）：生产治理动作 `PATCH /v0/management/oauth-excluded-models`（实测前缀只能是 `/v0`，`/v8` 下该路由 404；PATCH 是整表覆盖该 provider，须先 GET 合并）。按 24:03 实测的实时认领矩阵，traework 与 workbuddy / workbuddy-ai 各共享 **6** 个裸名（`deepseek-v4.1-flash`、`glm-5.2`、`glm-5.3`、`glm-5.3-flash`、`kimi-k2.6`、`minimax-m3`/`kimi-k3`），故建议一次决策整批而非只摘一个；`auto` 被四家同时认领属治理盲区，排除前须单独评估。当前触发面活跃：Trae 账号实时列表仍含该名字、7 账号中仅 1 个 active（剩 83 积分）、2 个 exhausted、近 60 分钟仍有 9 次配额错误，而该模型近 24h 无新请求 ⇒ 属已上膛未击发。

- 当前会话（2026-10-11）：**Token 用量面板移除四个图表与导出图片并下线图表专用接口与聚合（workbuddy-token-usage 0.2.6 已发布并生产热重载验收通过）**：
  - 用户诉求：图表功能全部移除、相关统计逻辑一并去掉以节省算力与响应体积；拍板边界为"只移除 4 个图表 + 同步移除图表专用接口与聚合"。
  - 实现：`dashboard.go` 删除 visual/advanced 两个 grid section、tooltip、导出图片按钮、聚合粒度下拉、41 个图表函数 + 6 个占比口径函数 + canvas 导出及其状态与监听；`selectedModel`/`hiddenModels` 随唯一写入者消失而移除；来源筛选锚点改挂隐藏的 `#range`；Go 替换器删 3 条并改写 2 条模式串防静默失配。后端下线 `/stats/trends` 与 `StatsTrendResponse`/`queryStatsTrendByFilter`/`statsQueryTrend`，`/stats/initial` 去 `series`+`bucket_seconds`、`/costs` 去 `series`；另清理 72 条无主 CSS 与四语言包各 56 个图表文案键。收益：`dashboard.go` −21%（269,709 → 212,658 字节）。
  - 门禁：`cgo-shim-build.py token-usage-tracker` 全绿；新增 `chart_endpoints_removed_test.go` 4 用例与 `test/token-usage/panel_chart_removal_repro.mjs` 38 项静态断言全 PASS（旧版本反证 24 项 FAIL）；两个执行型脚本的 trends 桩改为命中即抛错后仍全绿；字段变异反证触发 2 个 FAIL 后还原复绿。文档：`doc/2-需求/…222600_*`、`doc/5-tests/…222630_*`、`doc/6-review/…222710_*`（STYLE: PASS）。
  - 发布与生产验收：commits `d7494dd`/`bbb142b`/`61a29a0`/`a7a7778`，CI run `38147308550` success（8 资产 ALL CHECKSUMS OK），`.so` sha256 `5feaf5bd2bd4…`；生产 `plugin-store install` 返回 `installed restart_required=false`，宿主日志 `host.go:408 plugin hot reloaded active_version=0.2.6 retired_version=0.2.5`，零容器重启。线上面板 217,901 字节回读：图表标记全 0、卡片与 `groups`/`requestRows`/`key:'provider'` 保留；接口层 `/stats/trends` 404、`/stats/initial`+`/costs` 无 `series`，`/stats`+`/stats/groups`+`/requests` 均 200。0.2.5 资产定向 prune（远端 0.2.6=200、0.2.5=404）。
  - 后续发现并部分修复（0.2.7）：`store is closed` 失败分两类——A 类由宿主配置重载引发的换句柄窗口，每次带走 7–14 条真实丢失（`cfg_reload=0 且 reconfig=0` 的分钟 fail 恒为 0），0.2.7 已修（`ErrStoreClosed` 哨兵 + 配置指纹未变即复用句柄 + 关闭态不推进偏移，变异反证 `consumed = 1272/1284, want 0`）；B 类来自退役实例泄漏的 `feedImporterLoop`（`importerOnce` 每实例一份、关闭 store 不通知导入循环退出），实测 15 分钟内 0 次 reload/reconfigure/热重载仍有 248 条失败，但 `feed.ndjson.offset` 与文件字节数逐字节相等（94438247==94438247）、store 24h 11,114 ≥ new-api 5,046，证明是纯噪声与 CPU 空转、不丢数据，尚未修。据此撤回此前按 A+B 混算的「约 22%/最高 54% 持续丢失」高估。
  - 0.2.7 发布与生产验收（2026-10-11 23:53–24:01）：CI run `38151946948` success（30 作业全绿）；`.so` 8,388,992 字节 sha256 `7e08d2d44adb…`，二进制内含新日志串且 `0.2.6` 计数为 0；commits `cbc1e74`/`dfa4bfb`/`29c9591`；生产 `plugin-store install` 前两次 502（CDN 边缘陈旧，重试即通）第三次 `installed restart_required:false`；宿主 `hot reloaded active_version=0.2.7 retired_version=0.2.6`；上线分钟出现 `reuse=1` 证明换句柄窗口已消除；`offset==size`、读接口与面板 217,901 字节与 0.2.6 完全一致；0.2.6 资产定向 prune（远端 200/404）。
  - 0.2.8 发布与生产验收（2026-10-12 00:38–00:42）：CI run `38155105772` 30/30 全绿；`.so` 8,393,088 字节 sha256 `270cd96de2af…`，二进制内 `closed-store polls`=1 且 `store config unchanged`=1（两项修复同体）；commits 资产入库 + `89ddff0`(registry) + `f2e1c7c`(prune 0.2.7)；生产 `install?version=0.2.8` 首次即 `installed`，`active_version=0.2.8 retired_version=0.2.7`，`panic/fuse`=0，面板 217,901 字节与 0.2.7 逐字节同尺寸（图表标记仍全 0、`groups`/`requestRows`/`cards`/`provider` 保留）；远端 0.2.8=200、0.2.7=404。⚠️ `importer stopping after` 当前计数为 0 属**预期**：被退役的是无退出逻辑的 0.2.7 实例，该日志最早在下一次升级退役 0.2.8 时出现；同时泄漏代价已量化（安装后 2 分钟 `ok=72 fail=484`，比值随当天发布次数线性增长），旧协程需一次容器重启才能回收，交用户裁定。

- （更早的 13 条会话进展摘要已按 51200 字节上限裁剪，历史见 git log 与 PROJECT_HISTORY.md）

<!-- BEGIN RECENT PROJECT SESSIONS -->
## 最近 5 个同项目会话

| 会话 ID | 标题 / 意图 | 状态 | 活跃时间 | 关键改动 / 影响 |
|---|---|---|---|---|
| `01a11b9b-eb7d-7830-afed-7c8dd64c6629` | WorkBuddy AI 国际版签到改造实施 | completed | 2026-10-09 05:46 | 面板「领取专家加油包」失效入口替换为国际版每日签到，新增自动签到开关与批量签到；0.1.8 发布部署验收通过，上游活动离线登记为 GAP-001 |
| `01a11ba7-3193-7ab0-9618-11075ecaba63` | Token 用量面板性能与图标修复 | completed | 2026-10-09 03:06 | token-usage 0.2.4 移除面板 SSE 短连接轮询与 15s 定时轮询、改手动刷新并补插件图标；发布部署验收通过 |
| `01a115b7-7f04-7b81-8e96-cb269a144fa4` | Cursor 插件移植与 Token 导入实施 | completed | 2026-10-07 23:41 | 新增第六个插件 cursor-provider 0.1.0，支持会话 Token 导入账号；全链路发布、生产热重载与端到端验收均通过 |
| `01a11624-7389-7fe2-ac2b-82eb433267bd` | release-assets 3.75 GB 清理旧发布缓存 | completed | 2026-10-07 19:34 | release-assets 从 3.75 GB 清理至 0.13 GB；发布后清理规则固化为项目 skill 与 prune 脚本 |
| `01a110f3-1891-72c1-914e-7600d0e8b6df` | WorkBuddy AI 彻底清空写死模型与 0.1.3 全链路发布部署 | completed | 2026-10-07 03:20 | workbuddy-ai 彻底剔除硬编码模型，完全动态拉取；0.1.3 发布并生产部署热重载验证通过 |
<!-- END RECENT PROJECT SESSIONS -->
<!-- BEGIN TASK PLAN PROJECTION -->
```json
{
  "version": 5,
  "registry_schema": "task_plan_projection_registry",
  "registry_updated_at": "2026-10-13T18:09:27.704934Z",
  "projections": [
    {
      "projection_id": "SESSION/30608b3616fa2311bfb720a2776c09ac96b2738601ec3752b03a252c810cdb70",
      "session_id": "01a115da-13cb-7f90-ac94-25c133fdc31e",
      "projection_origin": "synthesized",
      "synthesis_mode": "exact",
      "state": "active",
      "plan_key": "IMPL-CURSOR-PROVIDER-PORT-20261007",
      "source_document": "doc/3-实施/2026-10-07_Cursor插件移植与Token导入实施总览.md",
      "plan_fingerprint": "9eea12fd114fb3e34cb60dd2a60eceb2eacd227a7e9a481c5b3f73c954a2a3b6",
      "updated_at": "2026-10-07T12:25:35Z",
      "steps": [
        {
          "id": "TASK-001",
          "step": "[TASK-001] 移植源码并统一标识",
          "status": "completed"
        },
        {
          "id": "TASK-002",
          "step": "[TASK-002] 令牌解析与导入",
          "status": "completed"
        },
        {
          "id": "TASK-003",
          "step": "[TASK-003] 导出删除启停接口",
          "status": "completed"
        },
        {
          "id": "TASK-004",
          "step": "[TASK-004] 面板与本地回归收口",
          "status": "completed"
        },
        {
          "id": "TASK-005",
          "step": "[TASK-005] 发布与部署",
          "status": "in_progress"
        }
      ]
    },
    {
      "projection_id": "SESSION/df86ce8afa18a9515072ffdfb460ee7e85402285d28ea505ed37f9133e10c0e8",
      "session_id": "01a115b7-7f04-7b81-8e96-cb269a144fa4",
      "projection_origin": "synthesized",
      "synthesis_mode": "exact",
      "state": "active",
      "plan_key": "IMPL-CURSOR-PANEL-AUTOENTRY-20261008",
      "source_document": "doc/3-实施/2026-10-08_Cursor面板免密直入与workbuddy对齐实施总览.md",
      "plan_fingerprint": "16acf9f349058ff187e5a3a7fb9bcc2f856cc447c47c98da76c8206319350112",
      "updated_at": "2026-10-07T17:15:57Z",
      "steps": [
        {
          "id": "TASK-001",
          "step": "[TASK-001] 面板免密直入改造：密钥区默认隐藏、启动自动加载、移除订阅额度说明区块",
          "status": "in_progress"
        },
        {
          "id": "TASK-002",
          "step": "[TASK-002] 测试与本地回归：management_test 断言更新、面板自动进入回归脚本、cgo-shim-build 全绿",
          "status": "pending"
        },
        {
          "id": "TASK-003",
          "step": "[TASK-003] 文档与版本收口：DESIGN/CHANGELOG/VERSION 0.1.1、6-review 风格回归",
          "status": "pending"
        },
        {
          "id": "TASK-004",
          "step": "[TASK-004] 发布与生产部署验证：commit/push/CI/资产入库/registry/热重载验收",
          "status": "pending"
        }
      ]
    },
    {
      "projection_id": "SESSION/20e8ec46dba59302295da066dda5bf5a2a66b4529a4bae4bb9cdc4698f67c8d1",
      "session_id": "01a11ba7-3193-7ab0-9618-11075ecaba63",
      "projection_origin": "synthesized",
      "synthesis_mode": "exact",
      "state": "inactive",
      "plan_key": "IMPL-TOKEN-USAGE-PERF-ICON-20261008",
      "source_document": "doc/3-实施/2026-10-08_Token用量面板性能优化与插件图标修复实施总览.md",
      "plan_fingerprint": "e41673af031195fb8992a957a1a20efa15ce3eac1bf8bc3dee3e5f2c8de406cc",
      "updated_at": "2026-10-08T17:23:47.766782Z",
      "steps": [
        {
          "id": "TASK-001",
          "step": "[TASK-001] 后端移除 SSE 通道（路由、函数、通知序列、测试同步）",
          "status": "completed"
        },
        {
          "id": "TASK-002",
          "step": "[TASK-002] 面板前端改造（去自动刷新、新增 last_1_hour、本地化）",
          "status": "completed"
        },
        {
          "id": "TASK-003",
          "step": "[TASK-003] 图标资产与注册（新图标入库、Logo URL、registry logo）",
          "status": "completed"
        },
        {
          "id": "TASK-004",
          "step": "[TASK-004] 版本与文档收口（VERSION/main.go bump、6-review、测试文档）",
          "status": "completed"
        },
        {
          "id": "TASK-005",
          "step": "[TASK-005] 发布与生产部署验证",
          "status": "completed"
        }
      ]
    },
    {
      "projection_id": "SESSION/6d9b35f8481be4fb66fbe631557cd5e155caef128cb55e64f7b4d110746dca96",
      "session_id": "01a11b9b-eb7d-7830-afed-7c8dd64c6629",
      "projection_origin": "synthesized",
      "synthesis_mode": "exact",
      "state": "inactive",
      "plan_key": "IMPL-WBAI-CHECKIN-20261008",
      "source_document": "doc/3-实施/2026-10-08_WorkBuddyAI国际版签到改造实施总览.md",
      "plan_fingerprint": "6cd82fd431385b7bf05b43a41d7600cfc93bd4126cc463516ef891fb986687b5",
      "updated_at": "2026-10-08T20:50:04.091579Z",
      "steps": [
        {
          "id": "TASK-001",
          "step": "[TASK-001] 后端签到数据链路：新增 checkin.go，扩展缓存 3 路与面板回填",
          "status": "completed"
        },
        {
          "id": "TASK-002",
          "step": "[TASK-002] 后端控制面与调度：/checkin 路由、配置开关、调度器合并",
          "status": "completed"
        },
        {
          "id": "TASK-003",
          "step": "[TASK-003] 面板签到改造与 vm 回归脚本",
          "status": "completed"
        },
        {
          "id": "TASK-004",
          "step": "[TASK-004] 文档与版本收口：bump 0.1.8、6-review",
          "status": "completed"
        },
        {
          "id": "TASK-005",
          "step": "[TASK-005] 发布与生产部署验收",
          "status": "completed"
        }
      ]
    },
    {
      "projection_id": "SESSION/5818ea886692422d8a462f2eb89e9b453f44422b609457bddc7ce1391942abe6",
      "session_id": "01a1219b-6aff-7fb2-822f-d9160626c22c",
      "projection_origin": "synthesized",
      "synthesis_mode": "exact",
      "state": "inactive",
      "plan_key": "IMPL-QODERWORK-CHECKIN-FALSE-SUCCESS-20261010",
      "source_document": "doc/3-实施/2026-10-10_QoderWork签到假成功修复实施总览.md",
      "plan_fingerprint": "7aab74187dac06f2d24f3b680a2913cfb6f68784eaf7f6a7f403d3a5d2607581",
      "updated_at": "2026-10-11T00:30:00.000000Z",
      "steps": [
        {
          "id": "TASK-001",
          "step": "[TASK-001] 取证与根因冻结：宿主桥状态码解码错误",
          "status": "completed"
        },
        {
          "id": "TASK-002",
          "step": "[TASK-002] 修复 host_bridge.go：抽出 parseHostHTTPDoResult",
          "status": "completed"
        },
        {
          "id": "TASK-003",
          "step": "[TASK-003] 收紧 billing.go：performCheckinCall 缺 success 字段返回失败",
          "status": "completed"
        },
        {
          "id": "TASK-004",
          "step": "[TASK-004] 新增 host_bridge_test.go 回归并完成反证与本地全绿",
          "status": "completed"
        },
        {
          "id": "TASK-005",
          "step": "[TASK-005] 版本与文档收口：bump 0.9.26、CHANGELOG、5-tests、6-review",
          "status": "completed"
        },
        {
          "id": "TASK-006",
          "step": "[TASK-006] 发布链：commit、push、CI、assets、registry、远端验证",
          "status": "completed"
        },
        {
          "id": "TASK-007",
          "step": "[TASK-007] 生产部署与账号 u09a5b6ab 行为验收",
          "status": "completed"
        }
      ]
    },
    {
      "projection_id": "SESSION/7226fa6af0bcd53d1de3ea0168019a75d3e69d438a2e508583aa50b16c3a005e",
      "session_id": "4a50f1f2-f554-4fcd-bb84-216778147dd2",
      "projection_origin": "synthesized",
      "synthesis_mode": "exact",
      "state": "inactive",
      "plan_key": "RELEASE-ZCODE-CLAIM-GUIDE-0.1.22",
      "source_document": "zcode/CHANGELOG.md",
      "plan_fingerprint": "7d0ab4c92600e8679dcb8ddfa5a628622ec80d2cc8a5e54f2ba1b61c543edec3",
      "updated_at": "2026-10-13T18:09:27.704552Z",
      "steps": [
        {
          "id": "TASK-001",
          "step": "[TASK-001] 提交 zcode 0.1.22 领取指引改动（仅 5 个 zcode 文件）",
          "status": "completed"
        },
        {
          "id": "TASK-002",
          "step": "[TASK-002] 推送 main 并派发 zcode-provider 0.1.22 CI 构建",
          "status": "completed"
        },
        {
          "id": "TASK-003",
          "step": "[TASK-003] 轮询 CI 运行并下载 8 个发布资产",
          "status": "completed"
        },
        {
          "id": "TASK-004",
          "step": "[TASK-004] 提交发布资产并运行 registry 发布脚本",
          "status": "completed"
        },
        {
          "id": "TASK-005",
          "step": "[TASK-005] 提交 registry.json 并验证远端 raw URL 可达",
          "status": "completed"
        },
        {
          "id": "TASK-006",
          "step": "[TASK-006] 生产环境 plugin-store 热重载安装并验收",
          "status": "completed"
        }
      ]
    }
  ],
  "quarantined_projections": []
}
```
<!-- END TASK PLAN PROJECTION -->
