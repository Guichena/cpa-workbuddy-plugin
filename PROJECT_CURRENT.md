# 项目当前状态

## 目标与范围

- 目标：维护 CLIProxyAPI (CPA) 的 Go 原生插件套件 `cpa-workbuddy-plugin`（涵盖 8 大 AI 服务商 Provider + 1 统一用量统计插件），提供多账号管理、动态模型发现、流式转发、专属现代管理控制台、扫码/Token 登录、保号巡检与统一 token 用量统计分析服务。
- 范围：仓库内 9 大插件体系的代码实现、单元测试、资产构建、发布流水线、registry 元数据同步等：
  1. `workbuddy-provider`：腾讯 CodeBuddy（国内版与 Global 版）；
  2. `workbuddy-ai-provider`：WorkBuddy AI（独立国际版）；
  3. `traework-provider`：Trae SOLO（字节跳动海外/国内 AI IDE 客户端）；
  4. `qoderwork-provider`：QoderWork（阿里通义灵码衍生国内版）；
  5. `qoder-ai-provider`：Qoder AI（国际版 qoder.com，含 UMID 协议支持）；
  6. `cursor-provider`：Cursor（cursor.sh，Connect-RPC 协议与会话连续性）；
  7. `zcode-provider`：ZCode（BigModel Coding Plan，Anthropic 兼容与客户端真实指纹 mimic）；
  8. `copilot-provider`：GitHub Copilot（设备码 OAuth / 内部令牌保活 / VS Code Copilot Chat 指纹模拟）；
  9. `workbuddy-token-usage`：统一 Token 用量统计与实时看板服务。
- 非范围：CLIProxyAPI 核心服务本体；跨项目文件修改。

## 项目概况

- 状态：活跃维护中。9 大插件全景全量编译与单测验证全绿（cgo-shim 零错误）。已彻底废除并删除 Gemini 插件（因依赖庞大导致二进制膨胀翻倍、GCP Project 门槛过高、缺乏调度与管理面板）。最新发布/就绪版本：
  - `zcode-provider`: **0.1.31**（面板额度改为逐模型独立进度条（GLM-5.3 / GLM-5.3-Flash 各一条）并移除「过期」等冗余信息；0.1.30 面板额度改用 `billing/balance` 取真实已用/剩余 + 领取失败文案友好化；0.1.29 修复面板无感验证 captcha 配置缺陷；0.1.28 面板内无感验证领取 Start Plan + OAuth 默认中国区）
  - `cursor-provider`: **0.1.6**（修复 Query 参数反序列化丢失导致真实发现模型被屏蔽、建立点号模型别名归一化根治上游 not_found、测试弹窗支持实时搜索过滤与秒开交互）
  - `qoderwork-provider`: **0.9.44**（0.9.44 流式中途断流零泄漏换号重试 + 断流信封补 `http_status=502` + 冷却落盘失败可观测与内存兜底；0.9.40 重建上游 messages 时保留工具调用结构修复 agent 多轮往返工具结果失配对；0.9.38 签到节奏恢复每 4 小时六槽 + 移除冗余门控；0.9.37 tools 对齐修复；0.9.35 max_tokens 统一 32768 最低输出底线；0.9.34 上游 110 日调用上限 24 小时持久化冷却 + 路由/换号排除 + 面板解冷）
  - `qoder-ai-provider`: **0.1.24**（0.1.24 流式中途断流零泄漏换号重试 + 断流信封补 `http_status=502`；0.1.20 重建上游 messages 时保留工具调用结构修复 agent 多轮往返工具结果失配对；0.1.18 签到节奏恢复每 4 小时六槽 + 移除冗余门控；0.1.17 tools 对齐修复；0.1.15 max_tokens 统一 32768 最低输出底线；0.1.14 上游 110 日调用上限 24 小时持久化冷却 + 补齐 /clear-cooldown 路由注册）
  - `traework-provider`: **0.2.13**（面板签到时刻文案漂移修正为 00/04/08/12/16/20；0.2.12 定时探活降频至 4 小时 + 失败也刷新节流时间戳；0.2.11 max_tokens 32768 底线 + payload 层同步，探活改全量模型按序测试）
  - `workbuddy-provider`: **0.16.5**（0.16.5 流式中途断流零泄漏换号重试 + 断流信封补 `http_status=502`；0.16.4 账号故障冷却改为 30s 递增阶梯、上限 10 分钟；0.16.3 成长中心接单失败不再虚报成功 + 错误原因可见化；0.16.0 成长中心全自动领奖 7 类玩法，`growth_auto` 默认关闭）
  - `workbuddy-ai-provider`: **0.1.16**（0.1.16 授权后自动补注册地等新能力；0.1.15 定时探活降频至 4 小时 + 失败也刷新节流时间戳；0.1.14 max_tokens 32768 底线，探活改全量模型按序测试）
  - `copilot-provider`: **0.1.2**（0.1.2 调度器对齐宿主候选集语义修复越界缺陷，已发布 `0054c28`→`ba471b0`→`bca1264`→`78b4069` 并生产验收：热重载前 5 分钟 50/50 请求告警、热重载后 0/57；0.1.1 宿主 OAuth 弹窗改开插件桥接页展示设备码；0.1.0 首发：Device Flow 登录 + 内部令牌 80% 提前保活 + VS Code Copilot Chat 指纹模拟 + 动态模型发现 + 多账号池换号重试 + 流式/非流式转发 + 额度与面板）
  - `workbuddy-token-usage`: **0.2.8**（0.2.8 让退役实例的 feed 导入协程在连续 3 轮关闭句柄后自行退出；0.2.7 修复宿主配置重载换句柄窗口导致的用量记录丢失；0.2.6 移除四个图表与导出图片、下线 `/stats/trends` 及序列聚合以省算力与响应体积；0.2.5 起请求明细与维度汇总展示原始 provider id）
- 活动工作区：F:\cpa-plugin
- 当前时间：2026-10-17 (GMT+8)

## 活动会话进展摘要

- 当前会话（2026-10-17）：**qoder 三插件流式断流换号与冷却落盘修复（CYCLE-01 + CYCLE-02 全部闭环）**：
  - 需求背景：生产会话 `5fa71566` 中 `qwen-3.8-flash` 请求（ID `202610171157242386880308268d9d6KQb4LXIN`）上游断流后未换号。根因：插件断流分支无换号路径（主）+ 断流信封无状态码（次）+ 24h 冷却 92 次触发零落盘（独立缺陷）。
  - **已实施 5 个任务**：qoderwork / workbuddy / qoder-ai 三插件新增 `isStreamReadError` + `shouldRotateOnStreamReadError` 纯函数；异步与同步断流分支接入分级换号（`emitted` 零泄漏守卫）；信封补 `http_status=502`（已泄漏时抑制）；qoderwork 冷却落盘失败补日志 + 内存冷却提前到 host RPC 之前 + watchdog 刷新合并保留未落盘条目。
  - **验证**：三插件 cgo-shim 全绿（qoderwork 10.05s / workbuddy 26.14s / qoder-ai 10.14s）；8 次哨兵反证均精确命中单例；5 份 `doc/6-review/` 记录均为 `STYLE: PASS`。
  - **已发布并生产部署（2026-10-17 22:40）**：qoderwork 0.9.44 / workbuddy 0.16.5 / qoder-ai 0.1.24；提交链 `d035e5e`(fix 23 文件) → `a47d5ad`(CI FreeBSD 14.4 镜像修复) → `f467b4d`(assets 24) → `6535ca5`(registry) → `d8faa83`(prune 0.14GB)。远端 21/21 artifacts ALL PASS 无残留；三插件 `plugin hot reloaded` 且 `active_version` 命中目标版本、落盘 `.so` sha256 与本地 zip 逐字节一致、panel/accounts 全 200，**零容器重启**（容器 Up 2 days）。
  - **CI 阻断修复（既有缺陷，非本轮引入）**：`go-cross/cgo-actions@v1` 内硬编码下载 FreeBSD `14.3-RELEASE/base.txz`，该版本已从 `download.freebsd.org/releases` 下线（404），既有 patch 指向的 `archive.freebsd.org/old-releases` 同样 404，导致全部 10 插件 freebsd-amd64 构建失败并连带 `Release` job 被 skipped。已改为在 wget 包装层把 `14.3-RELEASE` 改写为仍可用的 `14.4-RELEASE`。
  - **生产验收边界**：热重载后 15 分钟窗口内无断流事件，端到端「断流 → 换号 → 第二次成功」未能实证；已改为确认新符号 `shouldRotateOnStreamReadError` / `after partial output` / `daily cooling persist failed` 已进入三插件生产二进制（qoderwork 另含 `mergeActiveDailyCooling`），服务 `/v1/models` 200、无 error/panic 日志。后续需在真实断流发生时观察日志。
  - 文档：`doc/4-bugs/2026-10-17_202500_qoderwork流式中途断流不换号且24h冷却未落盘根因诊断.md`、`doc/3-实施/2026-10-17_203500_qoder三插件流式断流换号与冷却落盘修复实施总览.md`（§14 记录执行结果与偏差）。
- 上一会话（2026-10-15）：**跨插件同步第二轮复检（只读审计）+ 两份计划文档更新**：
  - 需求背景：用户「已经改了很多版本了，再次检查，还有哪些需要同步的，更新计划」。本轮以文件级 md5 对比 + 机制分布 grep + 版本矩阵复核对全部 9 插件做跨维度审计。
  - **审计结论**：原同步目标（批次 A~D）全部闭环且被后续演进自然覆盖；5 个 Scheduler 插件 `handleSchedulerPick` 同构，copilot 已随 0.1.2（`0054c28`）补齐 `candidateDisabled`。
  - **新登记 G1~G4**：G1（修复，建议优先）zcode `handleSchedulerPick` 越界缺陷——与 copilot 已诊断缺陷同款（忽略 `req.Candidates` 直接返回 `pickActiveAuth().AuthID`，ID 不带 `.json`），当前因调度器在任者是 copilot 而潜伏；G2（评估）http_status 通道推广；G3（需求）copilot 用量上报；G4（低优先级）token-usage-tracker 补 `traework` 显示映射分支。
  - **文档更新**：`doc/3-实施/2026-10-10_235000_*同步workbuddy路由能力实施总览.md`（新增「〇之二、第二轮复检」节：原目标复核 + 7 条新事实 + 版本矩阵 + G1~G4 登记）与 `doc/3-实施/2026-10-11_043000_九大插件全景架构与演进计划总览.md`（八大→九大插件、copilot 行新增、矩阵重写、G1~G4 路线图）。
  - 未决：G1 修复是否立即实施待用户裁定；copilot 0.1.2 与五插件冷却全灭语义统一改动待提交（并行会话在途）。

- 当前会话（2026-10-15）：**qoder 双插件重建上游 messages 保留工具调用结构（qoderwork 0.9.40 / qoder-ai 0.1.20），全链路发布生产**：
  - 需求背景：会话 `3ed4dd9f-109e-404e-8757-81b6d46d7ce1` 用 `qwen-3.8-flash` 时「推理老是突然推理一点点就结束、要不停继续提问」。根因诊断（`doc/4-bugs/2026-10-15_193641_*`）：主因是模板 `baseprompt.json` agent 会话上下文（无条件上行），放大器是插件重建 messages 只搬 `role`+`content`、丢掉 `assistant.tool_calls` 与 `tool.tool_call_id`。
  - **实施（方案 A）**：`openAIMessage` 补 `ToolCalls`/`ToolCallID`/`Name` 三字段 + **自定义 `UnmarshalJSON` 同步解析**（关键陷阱：结构体带自定义 UnmarshalJSON 时加字段必须同步赋值，否则恒空）+ `buildQoderBody` 重建时按非零值条件保留三者；双插件逐行同构。
  - **发布闭环**：`f74a5e8`(fix 10 文件) → `6d32f84`(assets 14/14 校验) → `d9e8592`(registry) → `5124003`(prune 两个旧版)；生产热重载 `active_version=0.9.40/0.1.20`、**零容器重启**（Up 42h）、落盘 `.so` sha256 与本地 zip 逐字节一致、accounts/panel 四路由全 200；远端 14/14 artifacts ALL PASS 无残留。
  - 门禁：隔离 worktree（`git worktree add --detach HEAD` + 仅复制本轮 8 文件，工作树含 20+ 并行会话文件）cgo-shim build/vet/test 全绿；两组哨兵变异各精确 FAIL 一项后恢复全绿（重建逻辑 → `TestBuildQoderBodyPreservesToolPairing`；UnmarshalJSON 赋值 → `TestOpenAIRequestPreservesToolStructure`）；提交按踩坑 57 路径限定，并行文件完好未混入。
  - **⚠️ 方案 A 判别力边界（生产 A/B 实测，诚实标注）**：单工具任务（带配对历史 + tools）PAIRED 与 BROKEN **均收敛** 6/6 ⇒ 无判别力；探索型任务（「分析所有 Python 脚本各自作用」+ 2 轮历史）PAIRED 与 BROKEN **均不收敛**，各 3/3 发 tool_calls ⇒ 方案 A 修的是明确缺陷（结构丢失），**不承诺消除探索型任务的持续工具调用**；探索倾向抑制（方案 B）需另行评估。
  - 未决：① 方案 B 待评估；② `chat_context.text` 在 agent loop 中恒回退最初提问是否致每轮从头探测（需上游裸直连验证）；③ 上游是否真正消费 `messages[].tool_calls`（未做裸直连 A/B）。
  - 文档：`doc/4-bugs/2026-10-15_193641_qwen-3.8-flash客户端声明tools致上游持续发起工具调用而推理提前结束根因诊断.md`（第七节已更新为修复已发布态）。

- 上一会话（2026-10-15）：**五插件签到节奏统一为每 4 小时，四插件全链路发布生产（qoderwork 0.9.38 / qoder-ai 0.1.18 / workbuddy 0.15.10 / traework 0.2.13）**：
  - 需求背景：用户裁定「把所有支持签到的插件改成每 4 小时尝试签到一次」。**关键事实**：5 个签到插件中 workbuddy / workbuddy-ai / traework 后端本就已是每 4 小时六槽，只有 qoderwork / qoder-ai 在 2026-10-12 被收敛为 `[10,22]`；zcode / cursor 不支持签到。
  - **行为变更（qoder 两插件）**：① `checkinHours` 由 `[10,22]` 恢复为 `[0,4,8,12,16,20]`；② **移除 `shouldRunCheckinNow` 门控**——该门控只在「签到表 ≠ 保活表」时才有意义，两表取值一致后 `nextCheckinTime` 并集恒等于签到表本身、门控退化为恒真死代码，删除后与 workbuddy 系 `schedulerLoop` 同构；③ 面板 `schedule` 与文案同步六槽。
  - **文案漂移修正（workbuddy / traework）**：`panel.html` 与 README/architecture 长期错标 `09:00/21:00`（后端实为六槽），修正为 `00/04/08/12/16/20`；`panel.html` 经 `go:embed` 进二进制，必须发版才生效。
  - **发布闭环**：提交 `91ca440`(feat 33 文件含实施/测试/6-review 文档) → 四插件 dispatch CI（`38623746537`/`38623755098`/`38623764859` success）→ `cf40437`(32 assets) → `97db81e`(registry) → `8c2ea35`(prune 4 旧版)。
  - **CI 处置**：qoderwork 首个 run `38623737653` 因既有 flaky 测试 `Test (token-usage-tracker)` 失败致 Release skipped → 单插件重发 `38626694582` success（踩坑 45/55 复现，并取消冗余 push run 释放 Runner）。
  - **生产验收**：四插件 `plugin-store install` 均 `restart_required=false`、日志确认 `plugin hot reloaded`（04:02，active=目标版 retired=旧版），**零容器重启**（容器 Up 26h），落盘 `.so` sha256 与本地 zip 逐字节一致（`eb5fed79…`/`18abd6e0…`/`e7eb3c16…`/`7a4a623f…`）；accounts/panel 全 200，新文案 `00/04/08/12/16/20` 就位、旧文案残留全为 0。远端 28/28 artifacts sha256+size ALL PASS、无旧版残留。
  - 门禁：四插件**隔离 worktree**（`git worktree add --detach HEAD` + 仅复制本轮文件，模拟 CI 构建对象，因工作树含 20 个并行会话文件）cgo-shim build/vet/test 全绿；哨兵变异精确 FAIL 后恢复全绿；面板内联 JS `node --check` + vm/DOM 桩回归全 PASS；`POLLUTION: PASS`；`STYLE: PASS`。提交全程按路径限定（踩坑 57），并行会话 20 个文件完好未混入。
  - 文档：`doc/3-实施/2026-10-15_031646_全插件签到节奏统一为每4小时实施总览.md`、`doc/5-tests/2026-10-15_031646_*`、`doc/6-review/2026-10-15_031646_*`。

- 上一会话（2026-10-15）：**zcode-provider 面板额度改为逐模型独立进度条并移除冗余信息（0.1.31），全链路发布生产**：
  - 需求背景：用户反馈截图显示一个账号的 GLM-5.3 / GLM-5.3-Flash 仍共用**一条**汇总进度条，看不出各模型各自消耗；并要求移除「过期」等与额度判断无关的信息。
  - **改造（纯前端，`zcode/panel.html`）**：`quotaHTML` 由「单条汇总条」改为按 `entitlements[]` **逐模型渲染独立进度条**——每个模型一条 `.pb-item`，标签显示「剩 X / Y」并按「剩余/授予」着色（≤30% 黄、≤10% 红）；回退通道（无真实用量）模型单独展示「授予 X」+ 半透明满格条；`card()` 移除「过期」行。CSS 新增 `.pb-item`/`.pb-name`/`.pb-used.warn|err`，移除本轮失效的 `.pb-primary`/`.pb-segment`。
  - **回归脚本同步（`test/zcode/panel_quota_render_repro.mjs`）**：新增逐模型进度条数量、逐模型剩余/授予文案、耗尽模型转红、卡片不展示过期行等断言；并修正两处**既有失配**（0.1.28 默认授权站点已改中国区、0.1.30 汇总文案已改「今日授予合计」，脚本此前仍断言旧值，长期失去判别力）。
  - **发布闭环**：提交 `0702b84`(feat 7 文件) → CI `38615248982` success → `b7b754a`(assets 7/7 sha256 OK) → `0d6c46a`(registry) → `7df26e2`(prune 0.1.29/0.1.30)；远端 raw URL 7/7 全部 200；生产热重载 `active_version=0.1.31 retired_version=0.1.30`（02:40:58），**零容器重启**；落盘 `.so` sha256 `5938fcc1…b0dd58` 与本地 zip 逐字节一致。
  - **生产面板验收**：面板资源 73,742 字节，`pb-item`/`pb-name` 各 4 处（CSS + 渲染），旧结构 `pb-primary`/`pb-segment` 与「过期」行残留均为 **0**。
  - 门禁：`cgo-shim-build.py zcode` build/vet/test 全绿；面板回归 PASS；哨兵变异（回退为单条汇总条）精确 FAIL 2 项后恢复全绿；`gofmt -l` LF 归一后空；`POLLUTION: PASS`；`STYLE: PASS`（`doc/6-review/2026-10-15_034500_*`）。
  - 文档：测试主文档 `doc/5-tests/2026-10-15_034500_zcode面板额度逐模型进度条测试.md`、6-review `doc/6-review/2026-10-15_034500_zcode面板额度逐模型进度条_6-review.md`。

- 会话（2026-10-15，已压缩）：**zcode-provider 0.1.30 面板额度改用 `billing/balance` 取真实用量**：根因是 `billing/current` 只返回授予量、无已用/剩余，且面板进度条硬编码 100%；改走 balance（强制设备三头 `X-Platform`/`X-Device-Mid`/`X-ZCode-App-Version`）+ `friendlyClaimError` 文案友好化；发布链 `a49101a`→`1b80e6f`(assets)→`6fd166d`(registry)，生产热重载零重启，`/credits` 返回 `source=balance has_usage=True` 且用量真实。Bug 文档 `doc/4-bugs/2026-10-15_023000_zcode面板额度永远显示满额根因诊断.md`。

- 会话（2026-10-14/15）：**zcode-provider 默认中国区 + 面板内无感验证领取额度（0.1.28 新增 → 0.1.29 修复配置缺陷），全链路发布生产**：
  - 需求背景：用户要求「默认中国区，OAuth 登录页也默认中国区授权」，并「通过逆向工程激活额度，不用手动去 zcode 客户端登录激活」；经确认范围收敛为「只做无感验证（不做交互滑块）」+「先预研可行性」。
  - 预研（`doc/5-tests/2026-10-14_151500_*`）：逆向 asar 3.15.1 + 真实浏览器实测，证实官方用的就是标准阿里云无感验证 SDK，**插件端可自主领取**（推翻此前「只能引导用户去桌面客户端」的结论）；识别三项此前遗漏的必需项：`region=sgp`（非 configs 字面下发的 `cn`）、`plan_id` 带批次后缀 `zcode-v3-start-plan-0817`（须由 `/billing/preview` 动态解析）、`X-Device-Mid`（UUID v4）。
  - **0.1.28 实施**：① 默认中国区——面板下拉 `bigmodel` 置首位 + `resolveOAuthProvider`/`providerFromMetadata` 空值默认 `bigmodel`（显式 `zai` 仍可用）；② claim 契约——新增 `fetchClaimablePlanID()` 动态解析 planId、`zcodeDeviceMid()` 稳定 UUID、claim 请求补 `X-Device-Mid`；③ 面板新增 `runTracelessVerification` + `claimQuota`，未领取卡片按钮改「🎁 领取额度」，原「📖 领取指引」降级兜底。发布链 `89837d9`→CI `38590147511`→`80eadd8`(assets)→`9635fa0`(registry)；生产热重载 `active_version=0.1.28 retired_version=0.1.27`，零容器重启。
  - **0.1.29 缺陷修复（本会话端到端实证中发现）**：0.1.28 面板产出的 `captchaVerifyParam` 仅 76 字符、不含 `securityToken`，`/claim` 始终返回 `captcha_required`。根因（真实浏览器 A/B/C/D/E 五组受控实验锁定）：`initAliyunCaptcha` 未传 `prefix`/`region`，且只设了 SDK **不读**的全局名 `AliyunRequestVerificationConfig`（SDK 实读 `window.AliyunCaptchaConfig` 且**仅在脚本加载期读取一次**）→ 请求打到 `undefined.captcha-open.aliyuncs.com`。修复：init 入参显式传 `prefix`/`region`（与时机无关的权威通道）+ 加载前预设 `window.AliyunCaptchaConfig`。修复后参数 **76→280 字符且含 `securityToken`**，生产 `/claim` 上游响应由 **captcha 层 `captcha_required` → 业务层 `no permission`**，证明 captcha 校验已通过。Bug 文档 `doc/4-bugs/2026-10-15_001240_*`。
  - **0.1.29 发布闭环**：提交 `c757927`(fix 5 文件) → CI `38596835494` success → `fb5f8d8`(assets, 7/7 sha256 OK) → `1a8bc97`(registry) → `db66489`(docs) → `0612449`(prune 0.1.27/0.1.28)；远端 raw URL 200；生产热重载 `active_version=0.1.29 retired_version=0.1.28`，零容器重启，落盘 `.so` sha256 `9dc184d9…b10da` 与 zip 内 `.so` 逐字节一致；**生产面板行为验收**：面板自身 `runTracelessVerification` 产出 280 字符有效参数。
  - 未覆盖：**「未领取 → 已激活」完整激活未取得实证**——上游当前对全部账号 `billing/preview` 返回 `plans:[]`（无下发可领取计划），claim 业务层返回 `no permission`，属上游活动态非插件缺陷；captcha 层已独立判据通过。
  - 门禁：`cgo-shim-build.py zcode` build/vet/test 全绿；面板 JS `node --check` 双块 rc=0；`gofmt` LF 归一后空；`POLLUTION: PASS`；`STYLE: PASS`（`doc/6-review/2026-10-15_003700_*`）。

- 当前会话（2026-10-14）：**zcode-provider 支持 bigmodel（中国区）OAuth 授权：CYCLE-1/2 已发布 0.1.25 上线，CYCLE-3 待真实账号**：
  - 需求背景：用户指出 ZCode 授权走的是国际站 `chat.z.ai`，国际版无每日额度赠送，需改走中国区以获取额度政策。期间用户两次纠正方向（先说「中国区依旧走 z.ai，不是 bigmodel」，后又更正「应该使用 bigmodel」）；实测最终确认：`provider=bigmodel` **完全复用**现有 CLI 轮询链路（共用 `zcode.z.ai/api/v1/oauth/cli/init` 与 `/cli/poll/`），仅 provider 参数、authorize 域名（`bigmodel.cn/login`）、回调路径不同 —— 是**参数化**而非重写。
  - 需求与计划文档：`doc/2-需求/2026-10-14_153951_zcode支持bigmodel中国区授权需求.md`（编号 `REQ-ZCODE-BIGMODEL-OAUTH-20261014`）、`doc/3-实施/2026-10-14_zcode支持bigmodel中国区授权实施总览.md`。
  - CYCLE-1（提交 `c84088f`，12 文件 +1017/-67）：`providerFamilyZai/Bigmodel` 常量 + `normalizeProviderFamily`/`providerFamilyOf`；`StoredAuth.ProviderFamily` 字段（缺省 `zai` 向后兼容）；`startAccountOAuth`/`settleZaiPollResult`/`saveZaiAccount` 全链 provider 参数化；`resolveUpstreamTargets` 按 family 分支（bigmodel → `open.bigmodel.cn/api/anthropic`）；业务令牌端点 `businessLoginEndpointFor` 分支 + 缓存键按 family 隔离。新增 `provider_family_test.go`（8 例）、`upstream_target_test.go`（6 例）。
  - CYCLE-2（提交 `0e88c4c`，7 文件）：面板工具栏新增 `#oauthProvider` 下拉（国际站/中国区，默认 `zai`），`openOAuthBridge()` 把选中值透传 `/activate-start`；新增 `providerBadge(a)` 渲染账号归属徽标；后端 `accountSummary` 新增 `provider_family` 字段并由 `handleListAccounts` 透出。扩展面板回归脚本（阶段 6/7）+ Go 用例 `TestHandleListAccounts_ExposesProviderFamily`。
  - **重要修正**：`quota.go` **无需** family 分支 —— 实测 `bigmodel.cn/api/v1/zcode-plan/billing/current` 返回 404、`zcode.z.ai` 同路径 401（端点存在），证明 start-plan 额度端点两家共用。已回写需求 GAP-002。
  - **0.1.25 发布闭环（本会话完成）**：bump `0.1.25` → 提交 `797d807` → push → CI run `38566121254` success → 下载 8 资产（7/7 sha256 校验 OK）→ `acbd5ed`(assets) → `0b86bef`(registry) → `170c25a`(prune 0.1.24)；远端 7/7 artifacts ALL PASS 无旧版残留；生产 `plugin-store install` **热重载成功**（`active_version=0.1.25 retired_version=0.1.24`，零容器重启），落盘 `.so` sha256 `cef55263…` 与本地 zip 逐字节一致；accounts/panel 均 200，面板含 `oauthProvider` 下拉与 `providerBadge`。
  - **0.1.26 缺陷修复（生产验证中发现，用户决策前向修复）**：生产直连三臂复测发现 `POST /activate-start` 传非法 provider（`zhipu`）**静默回退为 `zai`** 返回国际站授权地址，违反 AC-006。根因：入口先 `normalizeProviderFamily()`（语义为「非法值回退 zai」）再校验，非法值被提前改写致 `isValidOAuthProvider()` 永不触发。修复：新增 `resolveOAuthProvider(raw)` **先校验后归一化**，`handleActivateStart` 非法值返回 400，`providerFromMetadata` 改为原样透传；新增 3 用例 + 哨兵反证 3 例精确 FAIL。Bug 文档 `doc/4-bugs/2026-10-14_210000_*`。
  - **0.1.26 发布闭环（本会话完成）**：提交 `cbbca35`（fix 9 文件）→ push → CI run `38569617734` success → 8 资产下载 7/7 校验 OK → `0bd01d7`(assets) → `287e490`(registry) → `334bef9`(prune 0.1.25)；远端 7/7 ALL PASS 无旧版残留；生产热重载 `active_version=0.1.26 retired_version=0.1.25`，零容器重启；落盘 `.so` sha256 `a951e3fa…` 与本地 zip 逐字节一致；**生产三臂复测**：`bigmodel`→`bigmodel.cn`、`zai`→`chat.z.ai`（3/3）、`zhipu`→**HTTP 400 + 明确错误**（缺陷确认修复）。
  - **0.1.27 修复（GAP-006，本会话完成）**：CYCLE-3 发现 bigmodel 推理稳定 401。根因（asar 逆向 + 生产对照实验双重证据）：0.1.25 为 bigmodel 选的 `open.bigmodel.cn/api/anthropic` **不接受 ZCode OAuth JWT**（实测该端点对 bigmodel 与 zai 两类账号 JWT 均 401）；同一 bigmodel JWT 打 `zcode.z.ai/api/v1/zcode-plan/anthropic` 则**鉴权通过**（同一 IP 对照：无效 JWT→401、有效 JWT→405/3012 或 429/3009，证明鉴权先于风控）。asar 佐证：`open.bigmodel.cn` 仅服务 `bigmodelIndividualCodingPlan`（付费个人版），`bigmodelStartPlan`（免费版）走默认端点且 `resolveCurrent` 中 start-plan **不分 family**。修复：`resolveUpstreamTargets` 移除 bigmodel 专用端点分支（删除 `bigmodelCodingPlanBaseURL`），start-plan 形态下两站共用 `zcode.z.ai`。提交链 `c19493b`(fix 8 文件) → CI run `38576894736` success → `bcb1f76`(assets) → `22ec7da`(registry) → `72498f1`(prune 0.1.26)；远端 7/7 ALL PASS；生产热重载 `active_version=0.1.27 retired_version=0.1.26`，零容器重启，落盘 `.so` sha256 `8b153250…` 逐字节一致；**生产复测**：bigmodel 探活由 **401 → 429/3009 与 405/3012**（鉴权通过），与 zai 账号行为**完全一致**。Bug 文档 `doc/4-bugs/2026-10-14_220000_*`。
  - 门禁：`cgo-shim-build.py zcode` build/vet/test 全绿；面板回归 PASS；哨兵变异精确 FAIL 后恢复全绿；`gofmt -l` 空；`POLLUTION: PASS`；`STYLE: PASS`。
  - 待办：**GAP-004 / GAP-005 / GAP-006 均已解除**；上游 `zcode.z.ai` 对生产 IP 存在时段性 WAF（405/3012）与限流（429/3009），zai 账号同样受影响，属独立于本插件的外部因素，待观测（13:20 曾有 zai 推理成功 200/970ms 证明非永久封禁）。

- 当前会话（2026-10-14）：**四插件（workbuddy/workbuddy-ai/traework/zcode）定时探活降频至 4 小时并修正失败账号节流缺陷，全链路发布生产**：
  - 需求背景：用户先问「真的需要定时探测吗」，选定「移除探活 + test_failed 机制」后中途改口为「探活还是留下，但 4 小时一次就够了，不要那么频繁」；最终方向为**保留探活与 test_failed 标签全链路，仅把节流间隔 30 分钟 → 4 小时**。
  - 关键实现与真实缺陷修复：
    1. 四插件 `defaultActivePingInterval` 30 分钟 → 4 小时（workbuddy/workbuddy-ai/traework/zcode 的 `active_ping.go`），同步 4 处 `// 最近修改时间：2026-10-14 02:50:00`；
    2. **修正失败账号节流缺陷**：`recordActivePing` 原先仅在探活**成功**时刷新节流时间戳，失败账号不计时 → 会在每个 watchdog tick（10 分钟）被反复全量重测，反而比健康账号更频繁消耗配额；改为「无论成败都先刷新时间戳」后再轮测；
    3. 四插件各补 `TestDoActivePingFailureStillThrottles` 回归用例（断言失败后立即再次调用被节流拦截、不再发起模型请求），节流测试的时间回拨改为 `-defaultActivePingInterval - time.Minute` 自适应。
  - 并行会话保护：本轮曾误把工作树回退到 HEAD，需回贴并行会话的 `HIGH-CREDIT-FIRST` / cooldown-defer 注释修正（qoder-ai/qoderwork/workbuddy/workbuddy-ai/traework 的 scheduler/session_auth/watchdog/routing_test）；按路径限定 `git commit -- <20 路径>` 只提交本轮文件。
  - 发布与生产闭环：
    1. **提交链**：`22bf7fe`(feat 20 文件) → 四插件各自 assets/registry 提交 → `368a63e`(prune 5 个旧版本)；
    2. **CI 处置（可复用教训）**：① zcode 首个 run 因既有 flaky 测试 `Test (token-usage-tracker)` 失败致其 Release job skipped → 单插件重发成功；② traework 首个 run 卡在 `darwin/amd64` 的 `actions/checkout` 超 35 分钟不动 → 取消并重发；③ 取消自己 push 触发的冗余 push run（assets/registry 提交各触发一个）释放免费 Runner 后 traework 才获得执行位（踩坑 55 复现）；
    3. **远端验证**：四插件各 7/7 artifacts sha256+size ALL PASS；
    4. **生产热重载**：四插件 `plugin-store install` 均 `restart_required=false`、日志确认 `plugin hot reloaded`（workbuddy 0.15.8→0.15.9、workbuddy-ai 0.1.14→0.1.15、traework 0.2.11→0.2.12、zcode 0.1.22→0.1.24 直上），落盘 `.so` sha256 与本地 zip 逐字节一致，accounts/panel 全 200，**零容器重启**。
  - 门禁：四插件 `cgo-shim-build.py` build/vet/test 全绿 + 哨兵反证测试真实进编译。

- 会话（2026-10-14，已压缩）：**zcode-provider 0.1.22 领取指引**：纠正「激活开通」错误承诺，改为如实 Start Plan 领取指引（`guideModal`），提交链 `9655225`→`d859616`(assets)→`2d6d7f8`(registry)→`0cd3060`(prune 0.1.21)；根因实证：额度领取须官方客户端内手动完成（claim 接口强制阿里云人机验证头，无头一律 `400 3007`），`authorize` 层不做 redirect_uri 白名单（三臂 307 通过）。

- 当前会话（2026-10-15）：**copilot-provider 0.1.1：宿主 OAuth 弹窗改开插件桥接页展示设备码，全链路发布生产**：
  - 需求背景：用户在管理台点「登录」后，GitHub 授权页 8 个空白框需手输设备码，但宿主 OAuth 弹窗**不显示设备码**（附截图）。根因：宿主 `ServePluginAuthURL`（`auth_files_oauth_callback.go:218`）把插件 start 响应的 `Metadata` 只写进宿主 session store、**不回传前端**，硬编码返回 `{status,url,state}`；宿主内置 provider 是把 `user_code` 放响应顶层才显示，插件无法照做。用户采纳「选项 B：桥接页」方案。
  - **实现（方案 B）**：`handleStartLogin` 的 `URL` 改返回**插件自有 resource 前缀桥接页相对路径** `/v0/resource/plugins/copilot-provider/login-bridge?state=<state>`（resource 路由是插件唯一免鉴权 GET 面），新增 `bridge.go`（`loginBridgeURL`/`handleLoginBridge`/`loginBridgePage`/`htmlEscape`），桥接页渲染设备码（32px/letter-spacing:4px/user-select:all）+ GitHub 授权链接 + 三步指引；注册进 `managementRegistration().Resources`（**不挂 Menu**，不进菜单）。
  - **关键设计**：桥接页**只展示、不驱动轮询**——`pollDeviceLogin` 成功即 `Delete` 会话（read-once），桥接页若自行轮询会偷走凭据致宿主 `PollLogin` 拿不到 `AuthData`；轮询与落盘仍归宿主 `get-auth-status` → `PollLogin` 链路。
  - **发布闭环**：`100bc6b`(fix 11 文件) → CI dispatch `38732577295` success（并 cancel 同 head 冗余 push run `38732367637`）→ `2324317`(8 assets，sha256sum -c 7/7 OK + zip 内 `.so` 含 `0.1.1`/`login-bridge`) → `a5c2da0`(registry) → `fe8ed5a`(prune 0.1.0)；生产热重载 `active_version=0.1.1 retired_version=0.1.0`（20:26:17），**零容器重启**（Up 42h），落盘 `.so` sha256 `12c9fb2e…` 与本地 zip 逐字节一致。
  - **生产行为验收（真实端点）**：宿主弹窗端点 `GET /v0/management/copilot-provider-auth-url` 返回 `url=/v0/resource/plugins/copilot-provider/login-bridge?state=…`（`is_bridge=True`）；用该 state 打开桥接页真实渲染出设备码（与 start 返回一致）、含 GitHub 授权链接、HTML 完整；`get-auth-status` 返回 `{"status":"wait"}`；accounts/panel/bridge 内网与外网全 200。远端 7/7 artifacts sha256+size ALL PASS、0.1.0 已 404。
  - 门禁：cgo-shim build/vet/test 全绿（30 例）+ 面板回归 PASS + 双重变异反证（哨兵进编译 / 还原 URL 精确 FAIL）+ 真实浏览器 DOM 取证（正常页与错误页两分支）+ `gofmt -l` 空 + `POLLUTION: PASS` + `STYLE: PASS` + OCR 无 Critical；提交全程路径限定（踩坑 57）。
  - 文档：`doc/3-实施/2026-10-16_010000_copilot宿主OAuth弹窗设备码桥接页实施总览.md`、`doc/6-review/2026-10-16_013000_*`。

- 上一会话（2026-10-15）：**新增 copilot-provider 插件（0.1.0 首发）并完成 Kiro 可行性评估（裁定不立项）**：
  - **copilot-provider 立项与实现**：按 `doc/3-实施/2026-10-15_Copilot与Kiro插件立项实施总览.md` 的 CYCLE-1~4 推进。GitHub 免费档实测可用（`sku=free_limited_copilot` 可换内部令牌、发现 57 个模型、流式与非流式推理均通），关闭 `GAP-001`；`GAP-002` 关闭（持久化上游 `endpoints.api` 权威端点）。单测 27 例。
  - **CYCLE-4 发布闭环**：`c546b57` → `56ac86f`(8 assets) → `8966b0c`(registry)；CI 双 run success；远端 7/7 ALL PASS；生产 `plugin-store install` 返回 `restart_required=false`，**零容器重启**，落盘 `.so` sha256 `65d428b8…` 逐字节一致，四路由全 200。`AC-006`/`AC-007` 双 PASS。
  - **过程中的真实故障（已修复）**：侦察阶段提前写入的 registry 占位条目（`artifacts: []`）被并行会话的宽泛 `git add` 一并推送 → CPA 源级校验失败 → **所有插件** install 返回 `plugin_not_found`。由并行会话 `10a1f2f` 移除恢复；本轮随后严格按发布 skill Step 11 语义重做。
  - **Kiro 可行性评估（CYCLE-5，评估非实现）**：结论**不立项**。① Kiro FAQ 明文禁第三方 harness；② 免费档仅 **50 credits/月**；另有社区终局封禁实证。报告 `doc/3-实施/2026-10-15_235000_Kiro可行性评估报告.md`。

- 会话（2026-10-13，更早历史已压缩，详见 git log 与 PROJECT_HISTORY.md）：
  - **六插件 max_tokens 统一 32768 最低输出底线**：`model_specs.go` 新增 `minMaxTokensFloor=32768` + `applyMaxTokensFloor`，兜底 ReasoningMax 8192→32768，四个出口全部经 floor 收敛，广播层与 traework payload 层同步保底；zcode 规格矩阵由 14 扩至 64 条（最长前缀优先 + `vendor/` 剥离）；workbuddy/workbuddy-ai/traework 定时探活改「全量按序 any-success」，zcode 新增探活（30 分钟节流）；发布链 `7ec23c0`→`0aaaa65`/`b2b82e7`→`02e7375`→`82e437f`，六插件生产热重载零重启。
  - **Qoder 110 日调用上限 24 小时持久化冷却引擎（qoder-ai 0.1.14 / qoderwork 0.9.34）**：`isDailyCountExceeded` 精确识别 110；`daily_cooldown.go` 落盘 `daily_cooling_until` + 内存镜像 + watchdog 重建；路由/换号/会话粘性绝对排除，`resetAccountFailover` 刻意保留冷却；新增 `POST /clear-cooldown` 与面板冷却徽标。发布中发现并前向修复 0.1.13 漏登记路由致宿主空 body 404。
  - **按模型官方规格自适应放宽/截断 max_tokens**：根因是 Qoder 客户端写死 `max_output_tokens=8192`，长思维链吃满配额致正文被 `finish_reason: length` 截断（`output_tokens=reasoning_tokens=8192`，new-api 报无法扣费）。方案：内置官方规格表，推理模式自动放宽至官方上限、超限 clamp、元数据保底对齐。
  - **ZCode 0.1.15 claim 端点**（后被 0.1.22 推翻为「领取指引」，0.1.28 再以无感验证实现）：claim 链路需 `X-Aliyun-Captcha-Verify-Param` + `sgp` 区域。
  - **ZCode 0.1.14 修复用量管道/别名/额度刷新**：`usage_feed.go` 写孤立路径且缺 `tokens` 嵌套；`ExecutorModelScopeStatic` 误声明致别名未绑定；改 `ExecutorModelScopeOAuth` + `normalizeOfficialGLMModelID`。
  - **qoder 两插件修复排队帧未识别致 60s 挂死（0.1.9 / 0.9.30）**：`parseQoderSSEChunk` 增第 4 类顶层 `code` 形态 + `unwrapNestedMessage` 逐层展开，三路径循环内即刻收尾，行为统一「立即报错」；哨兵反证移除识别分支后精确复现 10.00s 挂死。
  - **删除 Gemini 插件（gemini-provider）**：体积 14.4MB、强依赖 GCP 结算、无面板与容灾 → `git rm` 后 registry 与 workflow 同步（9→8 插件）。
  - **ZCode OAuth 重构为官方 CLI 轮询链路（0.1.5→0.1.8）**：`POST /api/v1/oauth/cli/init` + `GET .../cli/poll/<flow_id>`；redirect_uri 白名单结论经三臂实测推翻，真因是 deep-link 唤起本地客户端。
  - **Qoder 403 首包假成功与流式诊断**：HTTP 200 下 `isQueued:true` 报错帧 + 连接僵死 60s、探活盲读 512 字节误判成功；生产 nginx `proxy_buffering off` 平滑重载后外网 1.35s 端到端。
  - **deepseek-v4.1-flash 推理断开根因**：同名裸模型多服务商认领 + 会话亲和钉死故障服务商 + 两个 30s 超时串行；traework 补 `filterExcludedModels`。
  - **Token 用量面板移除 4 图表（0.2.6/0.2.7/0.2.8）**：下线 `/stats/trends` 与序列聚合；0.2.7 修换句柄窗口丢记录（`ErrStoreClosed` 哨兵），0.2.8 让退役实例协程自退。

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
  "registry_updated_at": "2026-10-17T13:26:54.757596Z",
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
    },
    {
      "projection_id": "SESSION/4c4dab02fd732c0d17940754bc3095a8a25815699011645d49b517efc9cac1bb",
      "session_id": "75082e35-bf34-49e3-8ec9-d8c884098190",
      "projection_origin": "persisted",
      "synthesis_mode": "none",
      "state": "inactive",
      "plan_key": "FIX-QODER-STREAM-READ-FAILOVER-20261017",
      "source_document": "doc/3-实施/2026-10-17_203500_qoder三插件流式断流换号与冷却落盘修复实施总览.md",
      "plan_fingerprint": "f9fba8f7e10ccf53f10814cc188a002c7c92165a7c6c4561909339e3c52a3b70",
      "updated_at": "2026-10-17T13:26:54Z",
      "steps": [
        {
          "id": "TASK-001",
          "step": "[TASK-001] qoderwork 零泄漏断流换号：断流分支接入换号决策与重建请求",
          "status": "completed"
        },
        {
          "id": "TASK-002",
          "step": "[TASK-002] qoderwork 断流错误信封补 http_status=502",
          "status": "completed"
        },
        {
          "id": "TASK-003",
          "step": "[TASK-003] qoderwork 冷却落盘失败补日志并保留内存冷却兜底",
          "status": "completed"
        },
        {
          "id": "TASK-004",
          "step": "[TASK-004] workbuddy 断流换号并新增信封状态码通道",
          "status": "completed"
        },
        {
          "id": "TASK-005",
          "step": "[TASK-005] qoder-ai 断流换号与信封状态码扩展",
          "status": "completed"
        }
      ]
    }
  ],
  "quarantined_projections": [
    {
      "reason_code": "invalid_projection",
      "affected_session_id": "eccae029-046a-47a1-a6f9-f417e5d06b29",
      "source_version": 5,
      "source_index": 6,
      "raw_entry": {
        "projection_id": "SESSION/eccae029046a47a1a6f9f417e5d06b29queueframefailover000000000000000000",
        "session_id": "eccae029-046a-47a1-a6f9-f417e5d06b29",
        "projection_origin": "synthesized",
        "synthesis_mode": "exact",
        "state": "active",
        "plan_key": "FIX-QODER-QUEUE-FRAME-CLASSIFY-20261015",
        "source_document": "doc/4-bugs/2026-10-15_160000_Qoder排队帧被误判账号级故障且跨provider状态码通道缺失根因诊断.md",
        "plan_fingerprint": "queue-frame-classify-and-status-channel-20261015",
        "updated_at": "2026-10-15T09:40:00Z",
        "steps": [
          {
            "id": "TASK-001",
            "step": "[TASK-001] Bug 单落盘：排队帧误判账号级 + 跨 provider 通道缺失",
            "status": "completed"
          },
          {
            "id": "TASK-002",
            "step": "[TASK-002] 第1步：排队帧不再误判为账号级故障",
            "status": "completed"
          },
          {
            "id": "TASK-003",
            "step": "[TASK-003] 第2步：envelopeError 补 http_status 打通宿主状态码通道",
            "status": "completed"
          },
          {
            "id": "TASK-004",
            "step": "[TASK-004] 宿主侧改动计划：异步流错误通道透传状态码",
            "status": "completed"
          },
          {
            "id": "TASK-005",
            "step": "[TASK-005] 真实测试：单测 + 必失败哨兵 + cgo-shim 全绿",
            "status": "completed"
          },
          {
            "id": "TASK-006",
            "step": "[TASK-006] 6-review + 发布 + 生产热重载验收",
            "status": "completed"
          }
        ]
      }
    }
  ]
}
```
<!-- END TASK PLAN PROJECTION -->
