# 项目历史事件

> 本文件追加关键历史事件并只保留最近 20 条（按日期倒序、新事件置顶、追加后自动裁剪）；普通启动默认不读取，只有历史追问、当前状态不足或真实卡点时才窄检索。

## 事件


- 2026-10-14：**四插件（workbuddy/workbuddy-ai/traework/zcode）定时探活降频至 4 小时并修正失败账号节流缺陷，全链路发布生产**——用户口径先问「真的需要定时探测吗」，选定「移除探活 + test_failed 机制」后中途改口为「探活还是留下，但 4 小时一次就够了，不要那么频繁」。最终方向：**保留探活与 test_failed 标签全链路，仅把节流间隔 30 分钟 → 4 小时**。关键修复（顺带发现的真实缺陷）：`recordActivePing` 原先只在探活**成功**时刷新节流时间戳，导致失败账号不计时、会在每个 watchdog tick（10 分钟）被反复全量重测，反而比健康账号更频繁地消耗账号配额——改为「无论成败都先刷新时间戳」后再轮测。四个插件 `doActivePing` 同构修改（workbuddy/workbuddy-ai/traework 用 `hostAuthIndexForPhys`+`persistTestFailedToggle`，zcode 用 `sa.TestFailed`+`saveAuthTestStatus`），各补 `TestDoActivePingFailureStillThrottles` 回归用例（断言失败后立即再次调用被节流拦截）。过程中曾误将工作树回退到 HEAD，需回贴并行会话的 `HIGH-CREDIT-FIRST` / cooldown-defer 注释修正（qoder-ai/qoderwork/workbuddy/workbuddy-ai/traework 的 scheduler/session_auth/watchdog/routing_test），按路径限定 `git commit -- <20 路径>` 只提交本轮文件。版本：workbuddy 0.15.9 / workbuddy-ai 0.1.15 / traework 0.2.12 / zcode 0.1.24。**CI 处置（可复用教训）**：① 四个插件并发 dispatch，zcode 首个 run 因既有 flaky 测试 `Test (token-usage-tracker)`（本轮未改该目录、本地全绿）失败致其 Release job skipped → 单插件重发成功；② traework 首个 run 卡在 `darwin/amd64` 的 `actions/checkout` 超 35 分钟不动 → 取消并重发；③ 取消自己 push 触发的冗余 push run（assets/registry 提交各触发一个）释放免费 Runner 后 traework 才获得执行位（踩坑 55 复现）。发布链：`22bf7fe`(feat 20 文件) → 四插件各自 assets/registry 提交 → `368a63e`(prune 5 个旧版本)；远端四插件各 7/7 artifacts sha256+size ALL PASS；生产四插件 `plugin-store install` 均 `restart_required=false`、日志确认 `plugin hot reloaded`（workbuddy 0.15.8→0.15.9、workbuddy-ai 0.1.14→0.1.15、traework 0.2.11→0.2.12、zcode 0.1.22→0.1.24 直上），落盘 `.so` sha256 与本地 zip 逐字节一致，accounts/panel 全 200，**零容器重启**。门禁：四插件 `cgo-shim-build.py` build/vet/test 全绿 + 哨兵反证测试真实进编译。

- 2026-10-14：**zcode-provider 0.1.22 纠正「激活开通」错误承诺，改为如实的 Start Plan 领取指引并全链路发布生产**——用户拍板只做「📖 领取指引」形态（授权登录本身即完成账号绑定，每日额度领取是官方在 ZCode 客户端内的独立一步，插件不该承诺「点击即下发额度」）。根因实证：官方每日 800 万 Start Plan **并非授权登录时自动下发**，而是由 ZCode 桌面客户端内的服务端活动弹窗引导用户**手动领取**（`POST /api/v1/zcode-plan/billing/claim`），该接口强制校验阿里云人机验证头（`X-Aliyun-Captcha-Verify-Param`，`captcha.enabled=true`，sceneId `11xygtvd`）；无头 / 伪造验证头 / 换 token 一律 `400 3007 captcha verify failed`，网页端与插件端均无领取入口。另实测推翻旧结论：`authorize` 层**不做** redirect_uri 白名单（三臂候选均 307 通过），真因是桌面端 deep-link 唤起本地客户端致 CPA 拿不到授权码，故仍应复用官方 CLI 轮询链路。实现（仅 5 个 zcode 文件）：① 卡片未领取态徽标「未激活」→「未领取」、按钮「⚡ 激活开通」→「📖 领取指引」（`data-action="guide"`）；② 新增 `guideModal` 领取指引弹窗（官方领取四步 + 「刷新额度」复查），移除失效的 `activateAccount` 一键下发交互；③ `handleActivatePoll` 与 OAuth 弹窗成功文案统一为「授权登录成功」语义。门禁：面板脚本语法与事件链静态校验通过；`cgo-shim-build.py zcode` build/vet/test 全绿。发布链：`9655225`(fix) → CI run `38439920222` 64/64 success → `d859616`(8 assets) → `2d6d7f8`(registry) → `0cd3060`(prune 0.1.21)；远端 0.1.22 七平台 sha256+size 7/7 ALL PASS、0.1.21 raw URL 已 404；生产 `plugin-store/zcode-provider/install?version=0.1.22` → `restart_required=false`，日志 `plugin hot reloaded active_version=0.1.22 retired_version=0.1.21`，**零容器重启**（容器 Up 4 hours），落盘 `.so` sha256 `d5d10e85…` 与本地 linux_amd64 zip 逐字节一致；`/v0/resource/plugins/zcode-provider/panel` 200 且新文案生效（`领取指引`×7、旧文案 0），accounts 200（3 账号）。版本排序：上一轮 0.1.21 已由并行会话发布完毕（registry/release/assets 三处一致），故本轮直接发 0.1.22，无冲突。

- 2026-10-13：**六插件统一 32768 最低输出底线 + zcode 规格矩阵全扩 + 四插件定时探活改全量按序，并全链路发布生产（六插件各 +1 patch）**——用户四轮诉求递进：① 收口「插件兜底 ReasoningMax 8192 与 traework 20000 口径不一」；② 明确「所有模型最小 max_tokens 都应是 32768，这是底线」；③ 定时探活由「随机取 ≤5 个模型」改为「全量模型按序测试，任一成功即健康、不打 test_failed，仅全败才打标」（qoder 两插件不探活、zcode 需新增）。实现：① 六插件 `model_specs.go` 新增 `minMaxTokensFloor=32768` + `applyMaxTokensFloor` 纯函数，兜底 `ReasoningMax` 8192→32768，`computeAdaptiveMaxTokens` 四出口全部经 floor 收敛，广播层 `models.go` 的 `MaxCompletionTokens` 与 traework `upstream.go` payload 层同步保底；② zcode 规格表 14→64 条（GLM 4.5/4.6/4.7 全系 + Claude/GPT/Gemini/DeepSeek/Qwen），匹配改最长前缀优先 + `vendor/` 前缀剥离；③ workbuddy/workbuddy-ai/traework 新增 `orderedAllWorkbuddyModels`/`orderedAllTraeModels` 返回全量候选按序测试，废除「随机 ≤5 + 30s 整轮预算熔断」，zcode 新增定时探活（30 分钟节流 + 20s 单请求超时 + 全量按序 any-success），抽出 `pingUpstreamOnce` 纯传输内核供面板测试与定时探活复用（避免调度探活污染用量/冷却）。版本：workbuddy 0.15.8 / workbuddy-ai 0.1.14 / qoder-ai 0.1.15 / qoderwork 0.9.35 / traework 0.2.11 / zcode 0.1.21。发布链：`7ec23c0`(feat 52 文件) → `0aaaa65`/`b2b82e7`(assets) → `02e7375`(registry) → `82e437f`(prune 6 个旧版本)；远端六插件各 7/7 artifacts sha256+size ALL PASS；**CI 处置**：六插件并发 dispatch，workbuddy-provider 首个 run 因 `token-usage-tracker` 既有 flaky 测试（`TestFeedImporterLoopSurvivesHandleSwapAndResumes`，本轮未改该目录、本地全绿）失败 → 单插件重跑成功，其余 5 个一次通过；**生产**：六插件 `plugin-store install` 均 `restart_required=false` 且日志确认 `plugin hot reloaded`（active_version 全为目标版本），零容器重启，落盘 `.so` sha256 与本地 release zip 逐字节一致，accounts/panel 全 200。并行会话保护：`scheduler.go`（High-Credit-First 注释）、`zcode/panel.html`、`zcode/management.go` 及 zcode 0.1.22 bump 为其他会话在途改动，按路径限定 `git commit -- <路径>` 只提交本轮文件。门禁：六插件 `cgo-shim-build.py` build/vet/test 全绿；三份 6-review `STYLE: PASS`。

- 2026-10-12：**qoder-ai-provider 0.1.9 / qoderwork-provider 0.9.30 修复「上游 HTTP 200 顶层 code 排队帧未被识别致 qwen3.8 大上下文 60s 挂死」**——用户报「还是不能从 new-api 请求 cpa 的 qwen-3.8-flash、qwen-3.8 模型，但 cpa 本身测试可以」。生产直测复现决定性判据：同一模型名仅 prompt 长度不同行为相反（`hi` 1.47s 成功、67KB 3.49s 成功、112KB **3/3 稳定挂死 60.5s**）；挂死响应体首行即 `data: {"code":"403","message":"{...10605...isQueued:true...retryAfterSeconds:30}"}`，随后 3 条 `: keep-alive`，约 60s 后对端 `INTERNAL_ERROR` 复位（new-api 侧 `client_gone`、`frt=-1000`、`completion_tokens=0`）。根因（数据面解析器侧，上一轮未收敛）：`parseQoderSSEChunk` 只认 `[DONE]`/嵌套 `body`/扁平 `choices`·`id`/`error` 四类形态，顶层 `code` 帧落入兜底 `return "", false` 被 `continue` 跳过 → 读不到终止信号死等 Keep-Alive；面板探活因 prompt 极小不触发排队 + 首字节盲判而显示成功，形成「测试通过、真实不可用」假象（同一错误帧的两条路径）。排队性质经 feed 全量 259 条确认为**请求级 + 时段性**（同账号 ok/bad 混合 `01a003c5` 25:39、成功请求 input 达 289,424、97 条挂死集中 23:36–00:13 两账号交替）。修复：解析器增第 4 类顶层 `code` 形态；`sseErrorFrame` 增顶层 `code`+`message` 识别并新增 `unwrapNestedMessage` 逐层展开（保留首层业务码 10605）；`pumpUpstreamStream`/`collectUpstreamStreamQoder`/`aggregateQoderSSE` 三路径循环内即刻收尾（非流式聚合的物化循环也须退出）；`sendActivePingQoder` 改首帧内容校验。行为统一「立即报错」（`shouldRotateOnUpstreamErr` 对排队帧天然 false，不换号）。门禁：两插件 `cgo-shim-build.py` build/vet/test 全绿；新增 `stream_queue_frame_test.go`（`hangingReader` 模拟 Keep-Alive），**哨兵反证移除识别分支后该用例耗时 10.00s 精确复现挂死**；6-review `STYLE: PASS`；`POLLUTION: PASS`。文档：Bug 主文档 `doc/4-bugs/2026-10-12_001641_Qoder上游200排队帧未被识别致new-api调用qwen3.8挂死根因诊断.md`、测试主文档 `doc/5-tests/2026-10-11_164802_Qoder排队帧识别回归.md`、6-review `doc/6-review/2026-10-11_164802_Qoder排队帧识别修复_6-review.md`；skill `project-cpa-workbuddy-plugin-sse-and-ping-optimization-rules` 规则一新增契约；知识库 `HTTP200不等于流式协议成功-有效终止契约.md` 追加 2026-10-12 补充。

- 2026-10-11：**彻底废弃并删除 Gemini 插件（gemini-provider）全量代码与构建资产**——用户反馈当前 Gemini 插件体积极大（.so 达 14.4MB，比含全套 UI 的 WorkBuddy 还要大 80%），且在实际使用中几乎不可用（强制要求带有 Cloud Project 与结算绑定的 GCP 开发者账号，缺少 Web 控制台与调度容灾机制）；经评估确认其投入产出比过低且严重违背轻量化原则，决定彻底放弃并不再维护该插件。执行全量清理：使用 `git rm` 删除 `gemini/` 源码目录、`release-assets/gemini-provider-0.1.1` 资产目录及对应图标文件；从 `registry.json` 中移除该插件（插件源总数由 9 个回退至 8 个）；从 `.github/workflows/build.yml` 中彻底剔除 `gemini` 的构建、交叉编译与 Release 矩阵；全面同步 `README.md`、`PROJECT_CURRENT.md`、`PROJECT_MEMORY.md`、`PROJECT_HISTORY.md` 与《九大插件全景架构与演进计划总览》；`validate-registry.py` 校验 8 插件通过，全量兄弟插件 `cgo-shim-build.py` 编译与单测全绿。

- 2026-10-11：**沉淀「HTTP 200 首包业务报错帧假成功与全链路流式不缓冲规范」并完成生产 Nginx 重载验证**——用户反馈 New-API 接入 Qwen-3.8-Flash 极慢卡死无法使用，但 CPA 面板测试极快（219ms）、Token 用量插件显示响应不慢（334ms）。生产直测锁定双重根因：① 阿里 Qoder 上游对 Qwen 3.8 等受限/排队模型返回 HTTP 200 下的 `data: {"code":"403",... "10605"}` 排队不可用错误帧，CPA 面板 `active_ping` 仅盲读 512 字节未校验内容即判成功，误报 219ms 假成功，而真实推理因上游长连接死等卡死整整 60 秒直到 INTERNAL_ERROR 崩溃；② Token 用量插件记录的首包 TTFT 恰为 334ms 错误帧耗时，造成“响应不慢”错觉，但实际 `failed: true` 且耗时 60s；③ New-API 渠道测试按钮强制发送非流式（stream: false）全量等待，不能用于流式验证；④ 经由真实 `auto`（1.4s）与 `deepseek-v4.1-flash`（1.1s）对照确证 New-API 直连 CPA 8317 流式通畅；完成生产 Docker Nginx `proxy_buffering off; proxy_cache off;` 配置检查并通过 `docker exec nginx nginx -s reload` 热重载生效，外网端到端 1.35s 极速流式验证通过。落点 `project-cpa-workbuddy-plugin-sse-and-ping-optimization-rules`。

- 2026-10-11：**新增「生产配置存储红线（不手工改数据库表，强制）」到仓库级规则与发布 skill**——用户口径「写进发布 skill」「同时补进」。背景：2026-10-11 两次生产 CPA 故障均由外部会话直读直写 `config_store` 触发配置转义损坏。结论（实证）：更新 / 安装生产插件只需 `plugin-store install` 一条管理 API 命令，CPA 自动把 version + 7 平台 artifacts 写入 `config_store.content` 的 `plugins.configs.<id>` 并热重载，**零人工介入**（18:04:53 `zcode-provider` 0.1.3 install 前后两次配置快照 diff，唯一实质差异即该插件 version 与 artifacts）；反例是 `select content` 后经 JSON 二次序列化写回 → 真实换行退化为字面 `\n` → 塌缩单行 → YAML 判空 → `remote-management.secret-key` / `port` 全丢 → `/v0` `/v8` 管理路由 404 + 端口漂移 + 外网 502。落点：`AGENTS.md` / `CLAUDE.md`「项目特有规则 → 仓库与发布」在重启红线后新增「生产配置存储红线」；项目 skill `project-cpa-workbuddy-plugin-release-rules` Step 13 新增「⛔ 更新插件不需要手工改数据库表」子章节、权责边界新增「不直接读写生产 `config_store` 表」、踩坑清单追加第 54 条，`references/source-notes.md` 追加变更记录；`PROJECT_MEMORY.md`（核心记忆 + 变更记录）同步。红线要点：改插件配置只走 install；非插件配置走 `PUT /v8/management/config`；表**只读不写**；损坏铁证 = 原反斜杠被再转义一层（`\"` → `\\"`）+ `char(10)` 真实换行数 = 1。

- 2026-10-11：**吸收「生产容器重启红线」到项目规则与 skill（非必要不重启 cpa 容器）**——用户口径：非必要不要重启生产 cpa 容器，插件的新增 / 更新 / 安装走 `plugin-store install` 热重载即可、一般不需要重启。依据：install 默认返回 `restart_required:false` 热重载即生效（`docker logs cli-proxy-api | grep "hot reloaded"` 可见 `active_version`）；重启 `cli-proxy-api` 会中断在线流量并触发 60–80 秒冷启动（9 插件 + 模型刷新），属高成本、高影响操作，仅在热重载经证据确认未生效或配置损坏恢复等必要场景、且用户明确确认时才执行。落点：AGENTS.md / CLAUDE.md「仓库与发布」新增「生产容器重启红线（非必要不重启，强制）」；`project-cpa-workbuddy-plugin-release-rules` Step 13 校验项改为日志判定并新增红线条、权责边界末条改为「不主动重启生产 cpa 容器」；`project-cpa-workbuddy-plugin-host-config-safety-rules` 配置写入红线新增第 7 条（配置损坏恢复为必要例外）。

- 2026-10-11：qoder-ai-provider **0.1.6 修复 UMID 设备风控与独立机器指纹派生（生产真实增加 +100 积分）**——用户报错：Qoder AI 国际版签到仍有问题，客户端显示可以签到（每日 100 Credits），但生产账号一个都无法真正签到成功，积分没有增加。根因排查：① 官方客户端通过阿里云安全盾 UMID 获取 `machineToken` 并派生 `machineType`/`machineCode`，请求头缺少设备标识时上游 OpenAPI 静默隐藏每日活动；② 上游同机多账号风控拦截，多账号共用设备标识返回 `BLOCKED: SAME_PERSON_ALREADY_CLAIMED`；③ `checkin.go` 的 float64 类型断言导致 `rewardCredits` 提取与快照更新失效。修复：在 `qoder-ai/` 纯 Go 独立实现安全盾协议（Section 1B 封装、zlib 压缩、10 组密钥 AES-128-CBC 加密、XXH64 派生），按账号 UID 确定性派生独立虚拟硬件指纹，并支持拦截自动轮换；注入完整 `Cosy-Machine*` 请求头；修复积分快照即时回写。本地 `cgo-shim-build.py qoder-ai` 全绿 + 金标准反证；CI run `38084535313` 全绿；assets `0952920` → registry `292ceb9`（0.1.6）。生产部署验证：`plugin-store install 0.1.6` 热重载成功；**生产账号 `u4aac824d` 真实签到成功，赠送/签到积分 0 → 100（+100）**；批量签到 `summary={already:6, no_activity:2, fail:0}` 全量通过；prune 清理 0.1.5 历史资产。

- 2026-10-11：qoderwork-provider **0.9.27 修复 CN 每日签到「返回刷新成功但积分不变」（签到端点迁移 campaigns）**——用户报错：生产 QoderWork 账号积分 188，客户端显示可签到 +100，但签到不成功、只返回后台刷新、积分不变。根因（缺陷 C，与上一轮 0.9.26 缺陷 A 不同源）：CN 签到误用已废弃端点 `/sash/api/v1/me/daily-check-in/{status,claim}`；生产 token 实测 `status` 恒 `{"status":"DISABLED","campaignKey":"cn_daily_check_in_legacy"}`、`claim` 恒 HTTP 409 `AlreadyExists`，永远领不到积分。CN 官方客户端 v0.4.4（`com.qodercn.app.stable`）反解 + 运行日志证实真实签到走 campaigns API（`GET /sash/api/v1/me/campaigns` + `POST .../{campaignId}/claim`），且必须带 `Cosy-ClientType:10`+`User-Agent:Qoder` 头（缺头时列表被掩盖为 `showCampaign:false, campaigns:[]`）；与 qoder-ai 国际版 0.1.4 完全同源。修复：`main.go` 换 `endpointCampaigns`/`endpointCampaignClaim`；`billing.go` 新增 `campaignHeaders` + campaigns 解析 + `pickCheckinCampaign` + `performCheckinCall(sa,campaignID)`；`checkin.go` 传 `ci.CampaignID`、`!Active` 如实返回、summary 加 `no_activity`；`management.go` 加 `campaign_id`；`panel.html` 文案对齐。本地 `cgo-shim-build.py qoderwork` 全绿 + 双反证（`ClaimStatus` 破坏 → `TestPickCheckinCampaign_ClaimedInWindow` 必失败；删 Cosy 头 → `TestCampaignHeaders_CosyContract` 必失败）+ 提交态独立编译全绿（`git archive HEAD` + 叠加本轮文件）。发布链：`5b4acba` → CI run `38119587051` success（push-run `38119563064` 含 `Test (qoderwork) success`）→ assets `c568ce4` → registry `6a801b4`（0.9.27，7 平台 sha256/size ALL PASS、零残留）→ prune `bdda55a`（仅删自己的 0.9.26，保留并行会话在制的 qoder-ai 0.1.6）→ 文档 `dc94498`。生产部署：`plugin-store install 0.9.27` 落盘 `.so` sha256 `99733396c1bf3533e09274acfe175bd05fb87de60685f61be32a7d2e018c4085` 与本地 zip 逐字节一致，日志 `plugin hot reloaded active_version=0.9.27 retired_version=0.9.26`；**行为验收：生产账号 `u09a5b6ab`（`5643c3de6d95bacf`）真实签到 `success:true` + 「签到成功」，积分 188 → 288（+100）**；幂等复签返回 `reason=already, skipped=true,「今日已签到」` 不再报错。

- 2026-10-11：qoderwork-provider **0.9.26 修复宿主 HTTP 桥状态码解码错误（签到假成功根因，与 qoder-ai 0.1.3 同源）并发布部署**——用户报错：生产 QoderWork 账号 `u09a5b6ab` 可签到但「签到好像有 bug，无法签到」，面板显示签到成功却拿不到积分（假成功）。根因：`qoderwork/host_bridge.go` 用 `json:"status_code"` 解码宿主 `host.http.do` 响应，而宿主 v7.2.x 序列化 `pluginapi.HTTPResponse` 无 json tag，线协议键名是 PascalCase `{"StatusCode":404,...}`；下划线标签既不精确匹配也不构成大小写不敏感匹配 → `StatusCode` 恒为 0 → `if resp.StatusCode >= 400` 永不触发 → 404/4xx/5xx 被当 200 成功；`performCheckinCall` 再对缺 `success` 字段的响应盲归一化 `m["success"]=true` → 对外「签到成功」但积分不变。修复：移植 `parseHostHTTPDoResult`（PascalCase 优先 + 下划线防御性回退）；`performCheckinCall` 缺 `success` 布尔字段即返回 `success:false`。本地 `cgo-shim-build.py qoderwork` 全绿 + 反证（改回旧标签必失败，`StatusCode = 0, want 200`）。发布链：`72ce2d0` → CI run `38064786540` 65/65 success → `fb07a16`（8 资产）→ `712cfc6`（registry 0.9.26）→ `c37fea5`（prune 0.9.25）→ `20e6b01`（文档）；远端 7 平台 zip sha256 全等、旧版资产 404。生产部署：`plugin-store install` 0.9.26 落盘 `.so` sha256 `53a909b0e84bc70483db55ee94bfb928345eb3bdc0eacce4f60f396aa0fd29c1` 与本地 zip 一致，日志 `plugin loaded/registered version=0.9.26`；行为验收生产账号 `u09a5b6ab`（`6a155a864014ffd8`）调用 `/checkin` 返回 `success:false` + 真实上游 `http 409 AlreadyExists`（summary fail:1），由「假成功」变为如实失败。

- 2026-10-10：traework-provider **0.2.5 发布部署与签到失败双根因修复**（`x-device-id` 16 位数字风控 + 自动签到调度器相位对齐）——用户诉求：「修复 TraeWork 签到失败，用生产账号『用户04878311608』测试」。生产对照实验锁定两项根因：① `deviceIDFor` 拼接 `<baseDeviceID>-<userID>`（33 字符含 `-`）及旧版 `randomDeviceID()` 尾零填充/首位越界（如目标账号 `1114256688551036` 的 `deviceId=9670064000000000`）命中 `/trae/api/v2/ug/checkin_credits/claim` 设备指纹风控，100% 返回 9074「当前参与用户太多，请稍后再试」，且重试复用同一被拦截 ID；② `autoCheckinLoop` 原用 `time.NewTicker(1min)` + `now.Minute()==0` 相位错配导致自动签到漏触发。修复：`deviceIDFor` 改为按 `(baseDeviceID, userID)` SHA-256 确定性派生首位 1~3、末位 1~9 的 16 位纯数字设备号；`randomDeviceID` 改用 `crypto/rand` 直接映射 16 位数字；`checkinAccount` 遇 9074/9095 自动切换新 16 位设备号重试；`autoCheckinLoop` 改为 `nextAutoCheckinTime` + `time.NewTimer` 绝对时间对齐。本地 `cgo-shim-build.py traework` 全绿（含必失败哨兵）+ 6-review `STYLE: PASS`。发布链：`54f490a` → CI run `38059677609` 64 jobs success → `6291b2a`（8 资产 ALL CHECKSUMS OK）→ `2ffcdc9`（registry 0.2.5）→ `74dfe92`（prune 0.2.4）；远端 7 平台 raw URL 全 200。生产部署：`plugin-store install` 热重载 `active_version=0.2.5 retired_version=0.2.4`，落盘 `.so` SHA-256 `f24fe5f42cf6203cb8f69f68b6f1edfb1b1d9b56e7cc62221a24ddb5fbdbf4eb` 与本地一致；目标账号「用户04878311608」（`76bc7754f3fd72b2`）签到成功（积分包 2→3，剩余积分 130→230），单账号与全量 7 账号调用 `/checkin` 均返回 `ok: true`（`checked_in: 7, fail: 0`）。

- 2026-10-10：qoder-ai-provider **0.1.3** 修复宿主 HTTP 桥状态码解码错误（签到假成功根因）——用户报错：生产账号 u8e6a5348（393 积分）与 ua554edc3（0 积分）签到显示「成功 +100」但积分不变。根因：`host_bridge.go` 用 `json:"status_code"` 解码宿主响应，宿主 v7.2.x 未加 tag 输出 PascalCase `{"StatusCode":404,...}`，`StatusCode` 恒为 0 → 404 被当成功；`performCheckinCall` 再盲归一化 `m["success"]=true`。修复：移植 workbuddy `parseHostHTTPDoResult`，收紧归一化。本地 `cgo-shim-build.py qoder-ai` 全绿 + 反证（改回旧标签必失败）。产品事实：国际版 openapi.qoder.sh 无签到端点（404），app.asar 无签到代码，奖励体系为 campaigns（均不可领），**已发布部署并生产验收（2026-10-10）**：提交 `3fbe653`→assets `6bb57b5`→registry `746b1ef`→prune `d405ebd`；CI run 38062071659 success；远端 7/7 资产 sha256+size 全等、被删 0.1.2 资产 404；生产 plugin-store install 0.1.3 落盘 `.so` sha256 `e7f7537d…a46e0` 与本地 zip 一致、日志 `plugin loaded/registered version=0.1.3`；行为验收三账号 `/checkin` 由「假成功 success:true」变为如实 `fail:1 + http 404 NotFound`，缺陷 A 已修（不再假成功）。真实签到入口仍待产品确认（GAP-001）。

- 2026-10-10：qoder-ai-provider **0.1.2** / qoderwork-provider **0.9.25** content 多形态解析根因修复（真实推理 503）——用户报错：生产调用 `qwen-3.8-flash` 返回 `503 auth_unavailable: ... last upstream error: payload parse: json: cannot unmarshal array into Go struct field ***.***.content of type string`，而面板「测试」按钮通过。根因：两插件 `body.go` 的 `openAIMessage.Content` 声明为强类型 `string`，客户端按 OpenAI 多模态规范发送部件数组 content（`[{"type":"text","text":"..."}]`）时 `json.Unmarshal` 直接失败 → `handleExecExecute`/`handleExecStream` 进入 `payload parse` 失败分支 → 对外 503；测试按钮走 `sendActivePingQoder` **直接构造结构体**（`Content: "hi"`）绕过 JSON 解析，故测试通过而真实请求必失败（两条路径不同）。修复：为 `openAIMessage` 增加自定义 `UnmarshalJSON`（`decodeOpenAIContent`），纯字符串原样接收、部件数组提取 `text`/`input_text` 拼接、null/缺省归一空串；`Content` 字段类型与下游签名零改动。对照：workbuddy/workbuddy-ai 走 `map[string]any` 泛型解析 + `rewriteContentField` 已显式处理两种形态，不受影响；traework/cursor 亦已支持数组。本地：两插件各新增 `body_test.go`（4 项），cgo-shim build/vet/test 全绿；**修复前必失败已分别回退修复块复跑反证**，报错文本与生产 `last upstream error` 一致。**本轮关键冲突**：首次提交 `65f554c` 时 qoderwork 版本定为 0.9.24，但并行会话已于 21:57:35（`cafc4cf`）发布 `qoderwork-provider-v0.9.24`（tag→`bc365e6`，**不含**本次修复）并回填 registry，故改发 **0.9.25**（`27d6c99`）避免复用已存在 tag；已取消撞 tag 的两个 queued run（38057923742/38057921816）。文档：Bug 主文档 `doc/4-bugs/2026-10-10_215813_Qoder插件多模态content数组致推理503.md`、测试主文档 `doc/5-tests/2026-10-10_215813_Qoder插件content多形态解析回归.md`、6-review `STYLE: PASS`。

- 2026-10-10：qoderwork-provider **0.9.23** 发布部署（面板「测试」按钮改为弹出模型选择窗口按指定模型测试）——用户诉求：qoderwork 与三插件对齐，卡片「测试」不再随机挑模型，改为点击后弹出该账号支持的模型小窗口、点选指定模型再测。后端：qoderwork/models.go 新增 authModelsForIndex + handleModelsQuery（cachedDynamicModels → callModelsAPI，AccessToken 非空前置判断），management.go 新增只读 GET /models?auth_index= 路由（未进 mutatingManagementPath），active_ping.go 的 handleTestActiveWithAuth(sa, authIndex, model) 支持指定模型（model 空保持随机兼容旧面板），兜底模型 auto；前端 panel.html 新增测试弹窗（openTestModal / closeTestModal / onTestMaskClick / loadTestModels / runTestModel）并补 .model-list/.model-item 等 7 条 CSS。本地：cgo-shim build/vet/test 全绿 + 必失败哨兵确证真实进编译；Node vm 回归 17 项 PASS + 旧版反证 FAIL（TypeError openTestModal is not a function）；6-review STYLE: PASS。发布链 7df7f9a（13 文件）→ CI run 37964833825 success（head=7df7f9a）→ 7f18e2a（8 资产，7/7 sha256 OK）→ registry 0.9.23 回填（随 30dfe9b 一并推送，7 artifacts sha256+size 与 checksums 全等）；远端 raw 7/7 200 且 size 一致。生产：plugin-store install 0.9.23 installed，落盘 .so sha256 bb55d2df…3977 与本地 zip 内 .so 完全一致；热重载 active_version=0.9.23 retired_version=0.9.22；行为验收 GET /models?auth_index= 返回 {"models":["auto"]}、POST /test-active 指定 model=auto 成功（421ms，回显该 model）、无 model 走兼容随机路径同样成功（622ms）、panel/accounts 均 200、面板资源含 testModal/openTestModal/loadTestModels/runTestModel/data-action="test" 全部命中。**重要踩坑**：期间遇到生产自定义源被别的会话半成品条目（qoder-ai-provider `direct` + 空 artifacts）整体校验失败，导致该源全部插件 install 报 plugin_not_found；根因是宿主 ParseRegistry 对单源 registry 原子校验、任一条目非法即整源不入列，待对端补齐 artifacts 后源自动恢复，无需重启宿主。发布后清理：prune dry-run 确认 8 插件保留集完整、待删 0。

- 2026-10-09：workbuddy-provider **0.15.4** / workbuddy-ai-provider **0.1.9** / traework-provider **0.2.3** 发布部署（三插件测试按钮改为弹出模型选择窗口按指定模型测试）——用户诉求：面板卡片「测试」按钮原为随机挑模型直接测，改为点击后弹出该账号支持的模型小窗口、点选指定模型再测。后端：三插件 models.go 新增 authModelsForIndex + handleModelsQuery，management.go 新增只读 GET /models?auth_index= 路由，active_ping.go 的 handleTestActiveWithAuth(sa, authIndex, model) 支持指定模型（model 空则保持随机，兼容旧面板）；前端：三插件 panel.html 新增测试弹窗（openTestModal / loadTestModels / runTestModel），无模型时提示「暂无可用模型」并允许关闭。本地：cgo-shim 三插件 build/vet/test 全绿 + 必失败哨兵确证真实进编译；三面板 Node vm 回归各 17 项 PASS + 旧版反证 FAIL；6-review STYLE: PASS。发布链 ce6b4e2（32 文件）→ CI 三 run success（37940758486 / 37940778484 / 37940786472，head=ce6b4e2）→ fe5742d（24 资产，三目录 7/7 sha256 OK）→ 207e22c（registry 回填）→ ada831f（prune 旧版 0.15.3 / 0.1.8 / 0.2.2）；远端 49/49 当前资产 200、被删旧版 404。生产：三插件 plugin-store install 全部 installed，落盘 .so sha256 与本地 zip 完全一致（workbuddy 0.15.4 9c3b85b6… / workbuddy-ai 0.1.9 dd77aab4… / traework 0.2.3 2d671e16…），热重载 active_version 命中目标版本，panel / accounts 三插件全 200；行为验收 /models 三插件返回真实模型（17 / 27 / 大量）、缺 auth_index 返回 auth_index is required、/test-active 指定 deepseek-v4.1-flash 在 traework 成功（1.625s，回显该 model），workbuddy 因额度 429 但错误文案确认使用指定模型。

- 2026-10-09：workbuddy-ai-provider **0.1.8** 发布部署（国际版每日签到替换失效加油包）——面板把已失效的「领取专家加油包」入口替换为国际版每日签到（状态查询 + 领取积分），新增自动签到开关（00/04/08/12/16/20 六时段）与批量签到；发布链 583bb05→b4e3a3e（8 资产 7/7 sha256 OK）→45f6ce5（registry）；CI run 37836464993 全 57 jobs success；生产热重载 active_version=0.1.8 retired_version=0.1.7、落盘 .so sha256 baf416d3…c49a 与本地 zip 一致；生产 /checkin 按契约透传上游结果（code=10001 签到活动未开启，未伪造成功），四路请求头对照 T0-T3 零差异 + 全 11 账号 active=false + CN 域反证（CN active=true）+ banner 12302 判定为上游活动离线，登记 GAP-001 环境性阻断。

- 2026-10-09：workbuddy-token-usage **0.2.4** 发布部署——移除面板 SSE 短连接轮询（每 2s EventSource 重连）与 15s 定时轮询，改打开时单次加载 + 手动刷新，默认时间范围改「最近 1 小时」；补图标 assets/icons/TokenTracker.png。发布链 aab549c→37de5e4（8 资产 7/7 OK）→cd8b642（registry）→63a9879（prune 0.2.3）；CI run 37812435637 全 57 jobs success；远端 0.2.4 资产 200 + sha256 一致、0.2.3 已 404；生产热重载 active_version=0.2.4 retired=0.2.3、落盘 .so sha256 8ce19240… 与本地 zip 一致；生产面板 /usage 200（EventSource=0/setInterval=0/last_1_hour×7）。

- 2026-10-07：cursor-provider **0.1.0** 发布部署与端到端验收——发布闭环：commit 44d2bb4（162 文件）推送后派发 CI run 37636155616，全 57 jobs success；下载 8 资产（7 平台 zip + checksums）SHA256 校验 ALL OK（commit a17c2aa）；publish-assets 回填 registry（commit 92f569c）；远端 raw 7 资产 size+sha256 ALL PASS；生产 plugin-store install 0.1.0，落盘 .so sha256 bae19b38 与本地 zip 100% 一致，容器日志 plugin loaded + plugin registered 热重载成功，plugin-store 状态 installed/registered/enabled/effective_enabled 全 true。生产端到端验收：真实 token 导入成功（auth_index 9c0e4081ebf23fa7 → cursor-6c2463e563a13c02.json，面板可见 246 模型），重复导入正确去重；推理验收 cursor/default 非流式 3 次 + 流式（1..5 完整 + finish_reason:stop + [DONE]）+ 多轮对话全部成功；export 接口 200。高级模型（gpt-5.3-codex / composer-2.5 / claude-4-sonnet / gemini-3.x 等）统一返回上游 resource_exhausted（429，约 280ms 快速拒绝），判定为 Cursor 服务端对该账号订阅的配额限制，插件按真实 429 语义透传，非移植缺陷。发布后清理：release-assets prune dry-run 确认保留集完整、无待删；本地缓存 .workbuddy/release-assets 已删除。

- 2026-10-07：发布后清理规则固化与 release-assets 瘦身——把「每次发布后清理不需要的垃圾」吸收为项目规则与 skill：新增项目 skill project-cpa-workbuddy-plugin-release-asset-prune-rules（保留集=registry 每插件当前版本目录）+ 通用脚本 scripts/prune-release-assets.py（--dry-run/--apply，受跟踪目录 git rm、空目录 rmdir）；AGENTS.md/CLAUDE.md「仓库与发布」小节新增「发布后清理」条；发布 skill 增 Step 13.5。实操：release-assets 从 3.75 GB（191 版本目录）清理到 0.13 GB（保留 registry 当前 6 个版本目录：workbuddy-provider-0.15.3 / qoderwork-provider-0.9.22 / traework-provider-0.2.2 / workbuddy-ai-provider-0.1.7 / workbuddy-token-usage-0.2.3 / gemini-provider-0.1.1），删 185 个历史版本目录 / 1469 文件，提交 095f4a4 推送 origin/main；远端抽查当前版本 raw 200、旧版本 404。

## 计数锚点区

> 本区由 `memory-usage-tracking-rules` 收口闸门维护：HISTORY 仅窄读计入，会话启动不读不计；被裁剪事件的锚点随事件一起删除（不保留 retired）；本区计数仅作主题热度弱信号。锚点 key 用事件 `- YYYY-MM-DD：` 后的核心主题短语（约前 12 字符，可前缀匹配）。

```yaml
version: 1
anchors:
- title: 'zcode-provider **0.1.22 纠正「激活开通」'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: '**六插件统一 32768 最低输出底线'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: '**沉淀「HTTP 200 首包业务报错帧假成功'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: '**新增「生产配置存储红线」'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: '**吸收「生产容器重启红线」'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: 'qoder-ai-provider **0.1.6 修复 UMID'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: 'qoderwork-provider **0.9.27 修复 CN'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: 'qoderwork-provider **0.9.26 修复宿主'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: 'traework-provider **0.2.5 发布部署与签到失败双根因修复**'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: 'qoder-ai-provider **0.1.3** 修复宿主'
  usage_count: 1
  usage_days: 1
  last_used_at: 2026-10-11
  absorbed_to: null
- title: 'qoder-ai-provider **0.1.2** / qoderwork-provi'
  usage_count: 1
  usage_days: 1
  last_used_at: 2026-10-10
  absorbed_to: null
- title: 'qoderwork-provider **0.9.23** 发布部署'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: 'workbuddy-provider **0.15.4** / workbuddy-ai-provider'
  usage_count: 1
  usage_days: 1
  last_used_at: 2026-10-10
  absorbed_to: null
- title: 'workbuddy-ai-provider **0.1.8** 发布部署'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: 'workbuddy-token-usage **0.2.4** 发布部署'
  usage_count: 1
  usage_days: 1
  last_used_at: 2026-10-09
  absorbed_to: null
- title: 'cursor-provider **0.1.0** 发布部署与端到端验收'
  usage_count: 0
  usage_days: 0
  last_used_at: null
  absorbed_to: null
- title: '发布后清理规则固化与 release-assets'
  usage_count: 1
  usage_days: 1
  last_used_at: 2026-10-09
  absorbed_to: null
```
