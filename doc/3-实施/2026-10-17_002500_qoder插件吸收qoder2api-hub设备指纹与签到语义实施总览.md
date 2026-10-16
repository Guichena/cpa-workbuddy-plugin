# 需求实施总览：qoder 插件吸收 qoder2api-hub 的设备指纹稳定化与签到失败语义

结论：**经 2026-10-17 生产只读取证，原计划的签到语义部分（`CYCLE-02`）已作废**，只保留「推理设备指纹按账号 UID 稳定派生」一项，并新增「面板文案区分 + 签到日志补齐」为候选周期；影响：qoderwork 多账号用户在推理链路上的设备标识漂移；范围：qoderwork-provider 的 `sign.go`/`cosy_session.go`（指纹）与 `checkin.go`/`panel.html`（文案与日志，候选）；非范围：qoder-ai 的 UMID 链路、外部项目的网关架构、`runtime-info.exe` 原生桥、签到失败语义改造（已证伪）；变化：同一账号的设备标识跨进程重启与 token 刷新保持恒定；完成标准：qoderwork 单测全绿且哨兵变异可精确失败、生产热重载后同账号 machineId 在 token 刷新前后一致；术语说明：设备指纹指上游用于识别「同一台物理设备」的 `cosy-machineid`/`cosy-machinetoken`/`cosy-machinetype` 三头；验证状态：GAP-01 已闭合（结论否定），见 `doc/4-bugs/2026-10-17_010000_*`；本计划尚未编码。

## 1. 当前计划最终方案简要说明

- 推荐方案一句话结论：只吸收「UID 稳定派生设备指纹」与「签到失败语义诚实化」两点，其余三项不吸收。
- 主落点 / 主路径：`qoderwork/sign.go`（指纹派生）+ `qoderwork/cosy_session.go`（缓存键）+ `qoderwork/billing.go`（失败码解析）+ `qoderwork/checkin.go`（错误透传）。
- 为什么先走这条路线：这两点是本仓库相对外部项目**已被证实的能力缺口**（随机指纹漂移、失败语义丢失），改动局限在 3~4 个文件，不触碰上游协议与宿主契约。

## 2. 基本信息

- 对应需求文档：本文档（本轮由用户「看看能不能吸收到 qoder 插件」直接触发，未单独建需求文档；若进入实施需补 `doc/2-需求/` 主文档）
- 来源对象标识（需求或 Bug）：`REQ-QODER-ABSORB-QODER2API-20261017`
- 当前实施文档命名主干：`2026-10-17_002500_qoder插件吸收qoder2api-hub设备指纹与签到语义`
- 对应需求与实施计划全量顺序实施方案：N/A（本项目已有九大插件演进总览，本计划为其下增量；证据：`doc/3-实施/2026-10-11_043000_九大插件全景架构与演进计划总览.md`）
- 实施计划完成条件（`AC-*`）所在章节：本文件「§5 最小任务清单」各任务的「任务完成条件」
- 对应 `6-review` 风格回归记录：`doc/6-review/2026-10-17_014000_qoderwork签到失败日志补齐_6-review.md`（`TASK-04`，`STYLE: PASS`）、`doc/6-review/2026-10-17_022000_qoderwork面板区分活动未下发与账号不适用_6-review.md`（`TASK-03`，`STYLE: PASS`）
- 对应测试主文档：`doc/5-tests/2026-10-17_013000_qoderwork签到失败日志补齐测试.md`（`TASK-04`）、`doc/5-tests/2026-10-17_021000_qoderwork面板区分活动未下发与账号不适用测试.md`（`TASK-03`）
- 对应 Bug 取证记录：`doc/4-bugs/2026-10-17_010000_qoderwork签到同人去重失败码不存在GAP01取证结论.md`
- agent 理解的问题 / 目标：用户希望判断外部开源项目 `qoder2api-hub` 的四项能力（设备指纹隔离 / 每日签到 / 额度查询 / Pro 福利领取）能否吸收进本仓库 qoder 插件；经逐条源码对照，四项中三项本仓库已具备，真实缺口集中在「指纹稳定性」与「签到失败语义」
- 当前计划范围：qoderwork-provider 的设备指纹稳定化 + 签到失败语义诚实化
- 明确不在范围：qoder-ai 的 UMID 链路改造（其已用真实远端桥，优于派生方案）、外部项目的网关形态、`runtime-info.exe` 原生身份桥、额度查询与 Pro 领取（本仓库已具备同端点实现）
- 当前优先闭环：`CYCLE-01` 的 `TASK-01`（指纹稳定派生），因其为本轮唯一「无前提争议」的改动
- 关键假设 / 待确认点：
  - **关键假设 A1**：外部项目的「派生六头会被服务端过滤活动」结论**仅适用于活动 API**，不适用于推理 COSY 头。证据：外部项目 `qoder_accounts.py:1722-1772` 的 `desktop_headers()` 只服务 `/sash/...` 活动接口；推理头在 `qoder_sign.py:596-618` 独立构造且使用派生指纹。**本计划只改推理头，故不受该结论约束。**
  - **待确认点 GAP-01（阻断 `CYCLE-02`）**：CN 版 qoderwork **不发机器头**（仅 `Cosy-ClientType:10`+`UA`），服务端按什么维度做「同人去重」未知。外部项目实测的 `SAME_PERSON_ALREADY_CLAIMED` 是在**携带机器头**的前提下观测到的；CN 无机器头时是否复现该失败码，需运行时证据。
  - **待确认点 GAP-02**：外部项目对 `SAME_PERSON` 的处理是**记 6 小时冷却并诚实上报**，而本仓库 qoder-ai 是**轮换 UMID 重试 3 次**。两者策略相反，需用户裁定 qoderwork 采用哪一种。
- 跨会话执行入口：本文件 + `doc/3-实施/2026-10-11_043000_九大插件全景架构与演进计划总览.md`
- 主项目地址、仓库类型与代码基线：`F:\cpa-plugin`（Go c-shared 插件仓库），基线 `c3d44ce`（chore(release): prune superseded workbuddy-provider assets (0.16.1)）
- local 环境与依赖入口：`python scripts/cgo-shim-build.py qoderwork`（Windows 无 CGO 时的 build+vet+test 全绿入口）
- 外部项目代码引用：回指下方 `EXT-001`
- 当前状态：调研完成，计划待用户裁定
- 是否已获得开始实施授权：**否**（本计划不构成实施授权）

## 2.3 跨会话独立执行与外部项目代码引用清单

- 新会话接手第一步：读本文件 §2 的 GAP-01/GAP-02，确认是否已闭合
- 主项目地址与项目根：`F:\cpa-plugin`
- 主项目代码基线：`c3d44ce`
- 计划源文件与版本：本文件 v1.0
- 依赖安装、local 配置和服务启动入口：`python scripts/cgo-shim-build.py qoderwork`
- 中断点核验顺序：`TASK-01`（指纹）→ GAP-01 取证 → `CYCLE-02`（语义）
- 外部项目代码引用：

| 引用 ID | 外部项目名称 | 外部项目地址 | 版本 / 提交 | 项目根相对路径 | 文件 / 符号 | 引用用途 | 许可证 / 复制边界 | 可达性检查与失败停止条件 | 使用 TASK / TEST / EVIDENCE 回指 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| EXT-001 | qoder2api-hub | https://github.com/shuishuipingan/qoder2api-hub | `fd7a3740b7523af6e9abf1c1eb7c8338da6c1056`（2026-10-16） | `qoder_fingerprint.py` | `derive_id(uid, salt)` | 参考「UID+盐 → 稳定设备标识」的派生思路 | MIT；**只借鉴算法思路，不复制代码**（语言不同：Python vs Go） | `curl -sL https://raw.githubusercontent.com/shuishuipingan/qoder2api-hub/fd7a374/qoder_fingerprint.py`；拉取失败即停止实施并标记阻断 | `TASK-01` |
| EXT-002 | qoder2api-hub | 同上 | 同上 | `qoder_accounts.py` | `desktop_headers()` / `claim_campaign()` | 参考「同人去重」的失败码语义与「派生头仅用于推理」的边界结论 | MIT；仅作行为参考，不复制 | 同上路径拉取；失败即阻断 | `TASK-02`、`GAP-01` |

## 3. 实施周期总览

- 总周期说明：两个周期，`CYCLE-01` 无前提争议可直接实施，`CYCLE-02` 被 `GAP-01` 阻断需先取证
- 本次计划拆分的子任务周期数：2
- 周期拆分原则：按「能力域」垂直切片——设备指纹稳定化与签到失败语义是两条独立链路，前者影响所有推理请求，后者只影响签到
- 周期排序说明：`CYCLE-01` 先做，因其改动确定、风险可控、收益覆盖全量推理请求
- 周期 1：
  - 周期序号 / 期次定位：`CYCLE-01` 第一期
  - 周期目标：qoderwork 推理设备指纹由随机 UUID 改为按账号 UID 稳定派生
  - 本周期包含的最小任务：`TASK-01`
  - 周期内最小任务执行顺序：`TASK-01`
  - 进入条件：无（无外部前提）
  - 收口条件：`TASK-01` 实现 + 真实测试 + `6-review` 三项闭环
  - 状态：**已完成**（本地门禁全过：单测全绿、双哨兵正交精确 FAIL、`gofmt` 空、`POLLUTION: PASS`、`STYLE: PASS`）；生产验证已完成（0.9.41 已发布，热重载成功，9 账号验收通过）
  - 完成标志：同账号在 token 刷新前后 machineId/machineToken/machineType 恒定，且不同账号互不相同
  - 证据：测试主文档 `doc/5-tests/2026-10-17_024000_qoderwork推理设备指纹稳定派生测试.md`、6-review `doc/6-review/2026-10-17_025000_qoderwork推理设备指纹稳定派生_6-review.md`
  - 遗留：~~qoder-ai 同款缺陷（段落 md5 一致）未修，P2 待评估~~ → **已由 `CYCLE-04` 处置（2026-10-17）**；生产上游接受新指纹**已验证**（qoderwork 0.9.41 生产 `test-active` 返回业务码 112 通过鉴权）
  - 与前后周期衔接：为 `CYCLE-02` 提供稳定身份基础（`CYCLE-02` 已作废，故无下游依赖）
- 周期 2：
  - 周期序号 / 期次定位：`CYCLE-02` 第二期 —— **⚠️ 已作废（2026-10-17 生产取证证伪）**
  - 作废原因：GAP-01 闭合结论为否定——CN 场景不会返回 `SAME_PERSON_ALREADY_CLAIMED`，服务端改在**活动列表阶段**直接隐藏 `CLAIM_BENEFIT` 活动（6/9 账号列表无该条目），插件侧 `pickCheckinCampaign` 返回 `active=false` 根本不会发起 claim。原计划的失败语义分支在 CN **永不触发**，属死代码。
  - 证据：`doc/4-bugs/2026-10-17_010000_qoderwork签到同人去重失败码不存在GAP01取证结论.md`
  - 原目标：qoderwork 签到领取的失败语义诚实化（同人去重 / 幂等重复可读化）—— 不再实施
  - 状态：作废；`GAP-02`（同人去重策略二选一）随本周期作废而消解，无需用户裁定

- 周期 3（候选，待用户裁定是否纳入）：
  - 周期序号 / 期次定位：`CYCLE-03` 第三期
  - 周期目标：签到可观测性与面板文案诚实化（对应新登记的 `GAP-03` + `GAP-04`）
  - 本周期包含的最小任务：`TASK-03`（面板区分「活动被服务端隐藏」与「确实无活动」）、`TASK-04`（qoderwork 签到链路补失败日志，对齐 workbuddy 的 `billing ... failed` 形态）
  - 状态：**两个任务均已实施完成**（`TASK-04` 提交 `f8e3f26`；`TASK-03` 本轮完成），本地真实测试与面板回归全绿、双侧哨兵精确 FAIL、`STYLE: PASS`
  - 进入条件：用户已裁定纳入（2026-10-17）
  - 收口条件：两个最小任务各自实现 + 真实测试 + `6-review` 闭环 —— **已满足**
  - 完成标志：面板可区分两种「无活动」（按钮四态：已签到/签到/未下发/不可签）；签到失败在 `docker logs` 可见
  - 与前后周期衔接：与 `CYCLE-01` 无依赖，可独立推进
  - 遗留：批量签到 toast 未细分两形态（P3，见 6-review §6）；生产验证待发布
  - 说明：本周期为取证过程中**新发现**的缺口，非原方案内容；`CONSTRAINT-01`（签到去重维度疑似非插件可控）仅登记备查，不立项

- 周期 4（用户裁定纳入）：
  - 周期序号 / 期次定位：`CYCLE-04` 第四期
  - 周期目标：把 qoderwork 已验证的推理设备指纹稳定派生方案移植到 qoder-ai，消除同一缺陷
  - 本周期包含的最小任务：`TASK-05`（qoder-ai 推理设备指纹按账号 UID 稳定派生）
  - 周期内最小任务执行顺序：`TASK-05`
  - 进入条件：用户 2026-10-17 明确要求「qoder-ai 同款指纹缺陷」
  - 收口条件：`TASK-05` 实现 + 真实测试 + `6-review` 三项闭环
  - 状态：**已完成**（本地门禁全过：单测全绿、双哨兵正交精确 FAIL、`gofmt` 空、`POLLUTION: PASS`、`STYLE: PASS`）；**生产已发布并验收**（0.1.22 热重载成功、落盘 .so 哈希一致、3 账号 `test-active` 全 `ok:true`、零容器重启）
  - 完成标志：qoder-ai 同账号在 token 刷新前后 machineId/machineToken/machineType 恒定，且与 qoderwork 命名空间隔离
  - 证据：测试主文档 `doc/5-tests/2026-10-17_040000_qoder-ai推理设备指纹稳定派生测试.md`（§8 生产验收）、6-review `doc/6-review/2026-10-17_041000_qoder-ai推理设备指纹稳定派生_6-review.md`
  - 发布提交链：`4ab4d83`(源码+测试+文档) → `117608f`(assets) → `8b33a61`(registry) → `79a29d9`(prune)
  - 关键前提（已厘清）：qoder-ai 有两条独立设备标识链路——推理 COSY 链路（`sign.go` 的 `newCosySession`，**缺陷所在**）与活动 UMID 链路（`umid.go` 的 `resolveAccountUMID`，**已用真实远端桥稳定派生，无缺陷**）；本轮只改前者
  - 与前后周期衔接：与 `CYCLE-01` 同构（唯一差异为命名空间前缀 `qoderai:` vs `qoderwork:`），可独立推进
  - 遗留：生产上游接受新指纹未验证（P1，发布后立即验证）；工作树并行会话已把 qoder-ai bump 到 0.1.21（账号冷却阶梯化），版本号需在发布前协调

- 总体真实测试安排：
  - 真实测试安排是否默认必需：是
  - 覆盖哪些最小任务：`TASK-01`、`TASK-02`
  - 公共测试环境 / 依赖：Windows 无 CGO 环境 → `python scripts/cgo-shim-build.py qoderwork`
  - 公共样本 / 数据来源：真实账号 auth 文件（local 配置）；`GAP-01` 需生产只读观测
  - 总体通过标准：`cgo-shim-build.py qoderwork` build+vet+test 全绿 + 哨兵变异精确失败 + 面板 JS `node --check` rc=0

## 4. 阶段计划

- 阶段 1：
  - 阶段名称：指纹派生改造
  - 阶段目标：把设备标识的来源从 `uuid.NewString()` 换成 uid 派生
  - 只做这一件事：改 `newCosySession` 的三处标识生成 + 缓存键透传 uid
  - 输入条件：`storedAuth.Account.UID` 可获取（已确认：`cosy_session.go:27`）
  - 输出产物：`qoderwork/sign.go` + `qoderwork/cosy_session.go` 改动
  - 验证门槛：`TASK-01` 真实测试通过
- 阶段 2（按需，被 GAP-01 阻断）：
  - 阶段名称：签到失败语义分类
  - 阶段目标：解析 `failureCode` 并映射为可读中文
  - 只做这一件事：改 `performCheckinCall` 的失败分支
  - 输入条件：`GAP-01` 闭合 + `GAP-02` 已裁定
  - 输出产物：`qoderwork/billing.go` + `qoderwork/checkin.go` 改动
  - 验证门槛：`TASK-02` 真实测试通过

## 5. 最小任务清单

- 最小任务 1：
  - 任务名：设备指纹按账号 UID 稳定派生
  - 所属周期：`CYCLE-01`
  - 周期内顺序：1
  - 所属阶段：`PHASE-01`
  - 本任务只做这一件事：把 `newCosySession` 中 `machineID`/`machineToken`/`machineType` 三处 `uuid.NewString()` 改为基于 uid 的确定性派生
  - 垂直切片目标：同一账号跨 token 刷新 / 进程重启得到恒定设备标识
  - 输入条件：`cosyIdentity.UID` 已由 `cosy_session.go:40-50` 填充
  - 实现产出：新增 `deriveMachineID(uid)` / `deriveMachineToken(uid)` / `deriveMachineType(uid)` 三个纯函数（落 `sign.go`，与既有 `jsonSortedCompact` 等同层），`newCosySession` 签名补 uid 入参
  - 真实测试是否必需：是
  - 真实测试入口：`python scripts/cgo-shim-build.py qoderwork`
  - 真实测试依赖环境：Windows + Python 3（无 CGO）
  - 真实测试样本 / 数据来源：固定 uid 字符串（如 `u-test-0001`）+ 第二个 uid 作隔离对照
  - 真实测试通过标准：新增用例断言①同 uid 两次派生结果相同；②不同 uid 派生结果不同；③派生结果长度/字符集与既有 UUID 形态兼容（不破坏上游对 `cosy-machineid` 的格式预期）
  - 测试点：`TestDeriveMachineIdentityStable`（幂等）、`TestDeriveMachineIdentityIsolated`（跨账号隔离）
  - `6-review` 风格回归点：`sign.go` 新增函数需带完整函数头注释（`[参数]`/`[返回]`/`最近修改时间`），与同文件既有 `newCosySession` 风格一致
  - 任务完成条件：`AC-01-1` 单测全绿；`AC-01-2` 哨兵变异（把派生改回随机）精确 FAIL 上述两个用例；`AC-01-3` `6-review` 输出 `STYLE: PASS`
  - 任务停止 / 结束条件：`AC-01-1~3` 全满足即结束；任一不满足则停在当前任务排查，不进入 `TASK-02`
  - 阻断条件：若发现上游对 `cosy-machineid` 有 UUID v4 格式硬校验（`sign.go:120` 现用 `uuid.NewString()` 即 v4），派生值形态不匹配会导致鉴权失败 → 需改用 `uuid.NewSHA1`/`NewMD5` 保持 UUID 形态
  - 前置依赖：无
  - 下一任务依赖：`CYCLE-02` 的 `TASK-02`
  - 预计触达文件数：2（`sign.go`、`cosy_session.go`）+ 1 测试文件
- 最小任务 2（**已作废**）：
  - 任务名：签到失败语义诚实化
  - 所属周期：`CYCLE-02`（已作废）
  - 作废原因：GAP-01 证伪——CN 不返回 `SAME_PERSON_ALREADY_CLAIMED`，失败分支不可达
  - 证据：`doc/4-bugs/2026-10-17_010000_*`
  - 处置：不实施；如需签到可观测性，改走 `CYCLE-03` 的 `TASK-04`

- 最小任务 3（候选）：
  - 任务名：面板区分「活动被服务端隐藏」与「确实无活动」
  - 所属周期：`CYCLE-03`
  - 周期内顺序：1
  - 所属阶段：`PHASE-03`
  - 本任务只做这一件事：`checkinSummary` 增加来源标识，面板据此显示不同文案
  - 垂直切片目标：用户能分辨「账号本可签到但活动未下发」与「确实无活动」
  - 输入条件：`fetchCheckinStatus` 已能拿到 `ShowCampaign`（`billing.go:86`）
  - 实现产出：`billing.go` 补 `show_campaign` 透传 + `panel.html` 文案分支
  - 真实测试是否必需：是
  - 真实测试入口：`python scripts/cgo-shim-build.py qoderwork` + 面板 JS `node --check`
  - 真实测试样本 / 数据来源：本轮取证的 9 账号真实响应形状（脱敏后作为 fixture）
  - 真实测试通过标准：`showCampaign=true` 且无 CLAIM_BENEFIT 时文案区分于 `showCampaign=false`
  - 测试点：`TestCheckinSummaryDistinguishesHiddenCampaign`
  - `6-review` 风格回归点：面板文案与既有中文风格一致
  - 任务完成条件：单测全绿 + 哨兵变异精确失败 + `STYLE: PASS`
  - 任务停止 / 结束条件：三项全满足即结束
  - 阻断条件：无
  - 前置依赖：无（与 `CYCLE-01` 独立）
  - 下一任务依赖：无
  - 预计触达文件数：3（`billing.go`、`checkin.go`、`panel.html`）

- 最小任务 4（候选）：
  - 任务名：qoderwork 签到链路补失败日志
  - 所属周期：`CYCLE-03`
  - 周期内顺序：2
  - 所属阶段：`PHASE-03`
  - 本任务只做这一件事：在 `performCheckinCall` / `fetchCheckinStatus` 失败分支补 `log.Printf`，对齐 workbuddy 形态
  - 垂直切片目标：签到失败在 `docker logs cli-proxy-api | grep qoderwork` 可见
  - 输入条件：`billingDo` 已有失败日志先例（`billing.go:52,56`）
  - 实现产出：失败分支补日志（含账号脱敏标识 + 上游 code/msg）
  - 真实测试是否必需：是
  - 真实测试入口：同上
  - 真实测试样本 / 数据来源：mock 失败响应
  - 真实测试通过标准：失败路径产出含 `[qoderwork]` 前缀的日志行
  - 测试点：`TestPerformCheckinCallLogsFailure`
  - `6-review` 风格回归点：日志中文表达、无凭据回显
  - 任务完成条件：单测全绿 + 哨兵变异精确失败 + `STYLE: PASS`
  - 任务停止 / 结束条件：三项全满足即结束
  - 阻断条件：无
  - 前置依赖：无
  - 下一任务依赖：无
  - 预计触达文件数：2（`billing.go`、`checkin.go`）

- 最小任务 5：
  - 任务名：qoder-ai 推理设备指纹按账号 UID 稳定派生
  - 所属周期：`CYCLE-04`
  - 周期内顺序：1
  - 所属阶段：`PHASE-04`
  - 本任务只做这一件事：把 `qoder-ai/sign.go` 的 `newCosySession` 中 `machineID`/`machineToken`/`machineType` 三处 `uuid.NewString()` 改为基于 uid 的确定性派生，命名空间前缀 `qoderai:`
  - 垂直切片目标：同一账号跨 token 刷新 / 进程重启得到恒定设备标识，且与 qoderwork 命名空间隔离
  - 输入条件：`cosyIdentity.UID` 已由 `cosy_session.go:40-50` 填充
  - 实现产出：新增 `cosyFingerprintSeed` / `deriveMachineID` / `deriveMachineToken` / `deriveMachineType` 四个纯函数（落 `sign.go`），改造 `newCosySession`
  - 真实测试是否必需：是
  - 真实测试入口：`python scripts/cgo-shim-build.py qoder-ai`
  - 真实测试依赖环境：Windows + Python 3（无 CGO）
  - 真实测试样本 / 数据来源：固定 uid 字符串 + 第二个 uid 作隔离对照 + 跨插件命名空间对照
  - 真实测试通过标准：6 个用例断言同 uid 幂等、跨 uid 隔离、形态兼容、种子回退、令牌变更不变、跨插件命名空间隔离
  - 测试点：`TestDeriveMachineIdentity_*`（5 个）、`TestCosyFingerprintSeed_FallbackChain`
  - `6-review` 风格回归点：`sign.go` 新增函数需带完整函数头注释，与同文件既有风格及 `qoderwork` 同款实现一致
  - 任务完成条件：`AC-05-1` 单测全绿；`AC-05-2` 双哨兵正交变异精确 FAIL；`AC-05-3` `6-review` 输出 `STYLE: PASS`
  - 任务停止 / 结束条件：`AC-05-1~3` 全满足即结束
  - 阻断条件：无（`RISK-01` 沿用 `CYCLE-01` 的形态兼容设计规避）
  - 前置依赖：无
  - 下一任务依赖：无
  - 预计触达文件数：2（`sign.go` + 1 测试文件）

## 6. 现状与落点

- 涉及目录：`qoderwork/`
- 涉及文件 / 模块：`sign.go`（指纹生成）、`cosy_session.go`（会话缓存）、`billing.go`（签到调用）、`checkin.go`（编排）
- 复用点：`cosy_session.go:27` 已按 uid 建缓存键；`cosyIdentity.UID` 已填充，无需新增数据流
- 需要新增的内容：三个派生纯函数（`sign.go`）、失败码映射表（`billing.go`）
- 代码落点目录树：

```text
qoderwork/
├── sign.go              # 改：newCosySession 三处标识改派生；新增 4 个派生纯函数
├── cosy_session.go      # 改：newCosySession 调用点补 uid 入参
├── billing.go           # 改（CYCLE-02 作废后转 CYCLE-03）：签到失败日志 + show_campaign 透传
├── checkin.go           # 改（CYCLE-03）：区分活动未下发与账号不适用
├── panel.html           # 改（CYCLE-03）：按钮四态
└── sign_fingerprint_test.go  # 新增：指纹稳定性与隔离性用例

qoder-ai/                # CYCLE-04 新增周期
├── sign.go              # 改：newCosySession 三处标识改派生；新增 4 个派生纯函数（前缀 qoderai:）
└── sign_fingerprint_test.go  # 新增：指纹稳定性、隔离性与跨插件命名空间用例
```

## 7. 方案选择

- 方案 A（推荐）：**UID 派生 + 保持 UUID 形态**。用 `uuid.NewSHA1(uuid.NameSpaceOID, []byte("machine:"+uid))` 之类派生，既稳定又保持上游可能期望的 UUID 形态。
- 方案 B：**UID 派生 + 16 进制串**。直接 MD5 派生（更贴近外部项目），但形态与现状 UUID 不同，存在上游格式校验风险。
- 方案 C：**不改指纹，仅持久化到磁盘**。保持随机生成但落盘复用，解决「重启漂移」但不解决「token 刷新漂移」（`invalidateCosySession` 在 `keepalive.go:160` 与 `oauth.go:542,563` 三处被调用，约每 4 小时触发一次）。
- 推荐方案与原因：方案 A。它同时解决进程重启漂移与 token 刷新漂移，且不改变上游可见的标识形态，回归风险最低。

## 9. 真实测试安排

| 测试 ID | 任务 | 命令/入口 | local 环境 | 样本 | 断言 | 失败预期 | 清理 | 证据 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `TEST-01` | `TASK-01` | `python scripts/cgo-shim-build.py qoderwork` | Windows + Python3 | 固定 uid 字符串 | 同 uid 幂等、跨 uid 隔离、形态兼容 | 派生改回随机 → 精确 FAIL | 无（纯函数） | `doc/5-tests/` |
| `TEST-02` | `TASK-02` | 同上 | 同上 | mock 响应体 + GAP-01 真实样本 | 已知码映射中文、未知码不误包装 | 移除映射 → 精确 FAIL | 无 | `doc/5-tests/` |

- 免测任务及理由：无（两个任务均为行为变更）

## 11. 风险与阻断项

| ID | 风险/阻断 | 触发证据 | 当前措施 | 恢复路径 | 禁止动作 |
| --- | --- | --- | --- | --- | --- |
| `GAP-01` | CN 无机器头场景的签到去重维度未知 | **已闭合（否定）**：9 账号全部 HTTP 200、无 `SAME_PERSON` 失败码；6 账号列表直接无 `CLAIM_BENEFIT` | `CYCLE-02` 作废 | 若未来探针出现 `BLOCKED` 则重评 | 禁止按已证伪的假设实施死代码 |
| `GAP-02` | 同人去重策略未裁定（冷却 vs 轮换） | 随 `CYCLE-02` 作废而消解 | 无需裁定 | — | — |
| `GAP-03` | 面板无法区分「活动被隐藏」与「真无活动」 | 6/9 账号有此形态 | `CYCLE-03` `TASK-03` 候选 | 纳入则实施 | 禁止归因为插件缺陷 |
| `GAP-04` | qoderwork 签到失败零日志 | `docker logs \| grep qoderwork` 无失败记录 | `CYCLE-03` `TASK-04` 候选 | 纳入则实施 | — |
| `CONSTRAINT-01` | 签到去重维度疑似非插件可控（9 号同机仅 2-3 号可见活动） | 本轮 9 账号探针 | 登记备查 | 不立项 | 禁止把外部约束包装成插件功能 |
| `RISK-01` | 上游对 `cosy-machineid` 有 UUID v4 硬校验 | 若鉴权失败即证 | 方案 A 保持 UUID 形态 | 回退方案 C | 禁止直接上 16 进制串 |
| `RISK-02` | 指纹变更属上游可见行为变更 | 同账号 machineId 与生产当前值不同 | 灰度单账号验证 | 回退版本 | 禁止全量直上不经单账号验证 |
| `RISK-03` | **CYCLE-01 收益已被本轮取证收窄**：改推理指纹**不会**提升签到成功率（签到链路不发机器头） | `billing.go:78-82` 仅 `Cosy-ClientType`+`UA` | 价值重定位为「仅解决推理链路设备漂移」 | — | 禁止宣称「改指纹能修签到」 |

- 任务完成条件：见各 `TASK` 的 `AC-*`
- 任务停止 / 结束条件：任一 `AC-*` 不满足即停在当前任务
- 当前 agent 最大推进边界：**只到计划落盘，不进入编码**
- 是否已获得用户开始实施授权：否

## 13. 自审结论

- 覆盖度检查：四项能力已逐条对照，缺口与已具备项均已标注证据
- 实施周期检查：两周期，`CYCLE-02` 显式登记 `GAP-01` 阻断
- 最小任务闭环检查：两任务均含完成条件 / 停止条件 / 真实测试 / `6-review`
- 阶段单一目标检查：每阶段单一目标
- 占位词检查：无
- 可执行性检查：命令与文件落点均为本仓库真实验证过的入口
- 图文一致性检查：本计划为中等复杂度，图形化决策 N/A（原因：两周期线性依赖，无分支状态机；证据：§3 周期排序说明）
- 用户确认状态：待确认

## 追踪附录

- `SRC -> DEC -> REQ/RULE -> AC -> CYCLE -> TASK -> TEST -> EVIDENCE`：
  - `SRC-001`（用户请求：评估吸收 qoder2api-hub）→ `DEC-001`（只吸收 2 点）→ `REQ-QODER-ABSORB-QODER2API-20261017` → `AC-01-*`/`AC-02-*` → `CYCLE-01`/`CYCLE-02` → `TASK-01`/`TASK-02` → `TEST-01`/`TEST-02` → `doc/5-tests/2026-10-17_024000_*`（已产出）
  - `SRC-002`（用户请求：qoder-ai 同款指纹缺陷）→ `DEC-002`（移植 qoderwork 已验证方案）→ `CYCLE-04` → `TASK-05` → `TEST-05-1~6` → `doc/5-tests/2026-10-17_040000_*`（已产出）
  - `EXT-001`（指纹派生思路）→ `TASK-01`、`TASK-05`
  - `EXT-002`（失败码语义）→ `TASK-02`、`GAP-01`（已证伪，`CYCLE-02` 作废）
