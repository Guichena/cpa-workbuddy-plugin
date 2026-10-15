# Copilot 与 Kiro 插件立项实施总览

- 计划编号：`IMPL-COPILOT-PROVIDER-20261015`
- 关联需求：`doc/2-需求/2026-10-15_180000_Copilot插件立项需求.md`（`REQ-COPILOT-PROVIDER-20261015`）
- 状态：待执行（CYCLE-1 待启动）
- 创建时间：2026-10-15

## 一、当前计划最终方案的简要说明

本轮推荐方案：**照 `zcode` 插件结构新建 `copilot/` 插件**，以「GitHub Device Flow OAuth + Copilot token 定时刷新 + 直连 `api.githubcopilot.com` + 单文件面板」为主落点，复用 `zcode/main.go` 的 ABI 骨架与 `writeAuthFileDirect` 凭据直写通道；Copilot 上游本身是 OpenAI 兼容协议，**不做 Anthropic 转译、不使用官方 Agent SDK**，直接以原生 SSE 转发，从而避开 SDK 不返回 `tool_calls` 的结构性缺陷。之所以这么做：zcode 是既有插件中唯一同为「OAuth 设备码 + 多账号 + 面板 + 模型出口」的形态，与其对齐可将新增代码收敛在业务逻辑层，ABI/桥/发布链路全部复用。

## 二、Agent 对当前问题的理解

- **问题 / 目标**：把 GitHub Copilot 订阅额度封装为 CPA 的 OpenAI 兼容 Provider，支持多账号池、模型发现、流式转发、故障转移与面板。
- **本轮范围**：仅 `copilot-provider` 插件；Kiro 只在 CYCLE-5 做可行性评估，不实现。
- **非范围**：Anthropic 协议转译、官方 copilot-sdk、embeddings/图像/视频能力、CPA 宿主本体改动。
- **当前优先闭环**：CYCLE-1 的 `AC-001`（登录落盘）——先证明「GitHub 账号 → Copilot token → 凭据落盘」这条链路真实可用，再谈转发与面板。
- **关键假设 / 待确认点**：
  - 假设 1：`client_id=Iv1.b507a08c87ecfe98` 与 `scope=read:user` 仍有效（来源为 `ericc-ch/copilot-api` 源码，非官方文档）。
  - 假设 2：Copilot token 端点 `GET https://api.github.com/copilot_internal/v2/token` 与所需 `x-github-api-version: 2025-04-01` 头仍有效。
  - 待确认 1（`GAP-001`）：Copilot Free 能否走此链路——CYCLE-1 实测关闭。
  - 待确认 2（`GAP-003`）：GitHub 风控边界——灰度观察。

## 三、现状与代码落点

### 新增目录树

```text
copilot/                        # 插件 9：GitHub Copilot Provider
├── main.go                     # ABI 入口（C preamble 照抄 zcode）+ handleMethod 分发
├── go.mod / go.sum             # module github.com/luode0320/cpa-workbuddy-plugin/copilot
├── VERSION                     # 版本号（起始 0.1.0）
├── oauth.go                    # Device Flow：start / poll / refresh
├── token.go                    # GitHub token → Copilot token 交换与定时刷新
├── authfile.go                 # StoredAuth + writeAuthFileDirect + listAllAuthFiles
├── models.go                   # defaultModels + filterExcludedModels + cacheModelAliases
├── executor.go                 # handleExecExecute / handleExecStream（直连 + 原生 SSE）
├── upstream.go                 # Copilot 指纹头构造 + base URL 推导
├── quota.go                    # /copilot_internal/user 额度查询与快照
├── scheduler.go                # handleSchedulerPick + accountRoutable
├── stream.go                   # host.stream.emit / close 封装
├── management.go               # managementRegistration() + handleManagement
├── panel.go                    # go:embed panel.html
├── panel.html                  # 单文件管理面板
├── README.md / CHANGELOG.md
└── *_test.go                   # 同目录单测
```

### 需同步的既有文件

| 文件 | 改动 |
|---|---|
| `.github/workflows/build.yml` | 6 处新增 copilot（tags / dispatch options / test matrix / build matrix ×5 / build-cross ×2 / release if + tag 解析） |
| `registry.json` | 新增 `copilot-provider` 条目（id/name/version/install.artifacts 7 平台） |
| `assets/icons/` | 新增 `copilot.png` |
| `README.md` | 插件全景表新增一行 |
| `PROJECT_CURRENT.md` / `PROJECT_MEMORY.md` | 插件数 8 → 9，同步事实 |

## 四、EXT-* 外部项目代码引用清单

| ID | 项目 | 版本/分支 | 路径 / 符号 | 用途 | 许可证 / 复制边界 | 验证回指 |
|---|---|---|---|---|---|---|
| `EXT-001` | [ericc-ch/copilot-api](https://github.com/ericc-ch/copilot-api) | `master` | `src/lib/api-config.ts`（client_id、端点常量）、`src/services/github/*`（device flow、token、usage） | 协议契约事实源 | MIT；**只引用协议事实，不复制代码** | CYCLE-1 实测对照端点与 client_id |
| `EXT-002` | [StarryKira/copilot2api-go](https://github.com/StarryKira/copilot2api-go) | `master` | `auth/device_flow.go`、`store/account.go`、`instance/load_balancer.go` | 工程结构参考（多账号池、刷新退避） | MIT；借鉴思路，Go 代码按本项目风格重写 | CYCLE-3 面板/池化行为对照 |
| `EXT-003` | [github/copilot-sdk](https://github.com/github/copilot-sdk) | v1.0.0 | — | **反面教材**：确认其不返回原生 `tool_calls`，故排除 | MIT；不使用 | 不适用（排除项） |

## 五、实施周期

### CYCLE-1：协议打通与凭据落盘（对应 `AC-001`、`AC-002`，关闭 `GAP-001`）

- 期次定位：第一期，验证性闭环。目标：证明「GitHub 账号 → Copilot token → 落盘 → 刷新」真实可用。
- 阶段与产出：
  - 阶段 1：`copilot/main.go` ABI 骨架 + `go.mod` + registration（能力声明）。
  - 阶段 2：`oauth.go` Device Flow + `authfile.go` 凭据直写。
  - 阶段 3：`token.go` Copilot token 交换与刷新。
  - 阶段 4：真实账号实测（含 Copilot Free 账号），关闭 `GAP-001`。
- 进入条件：无（起点）。
- 收口条件：`AC-001`、`AC-002` 通过；`GAP-001` 有实测结论（可用/不可用）。
- **执行结果（2026-10-15）**：
  - `AC-001` **PASS**：设备码登录全链路单测通过（httptest 桩），真实免费账号完成授权并换取 GitHub token。
  - `AC-002` **PASS**：令牌刷新节奏单测通过；真实账号换取 Copilot 令牌成功（含 `expires_at` / `refresh_in`）。
  - `GAP-001` **PASS**：免费账号（`sku=free_limited_copilot`）可换取内部令牌、发现 57 个模型、完成非流式与流式推理。
  - `GAP-002` **已关闭**：实测 `endpoints.api` 存在且与硬推导值不同，已改为消费该字段（`StoredAuth.APIEndpoint` + `resolveCopilotBaseURL` 优先级调整 + 回归用例 + 变异验证）。
  - 验证证据：`doc/5-tests/2026-10-15_190000_copilot插件CYCLE-1测试.md` §2.5 / §2.6。
  - 诚实边界：未在 CPA 宿主内加载 `.so` 跑通宿主 RPC 桥，宿主内端到端属 CYCLE-2 / CYCLE-4；`GAP-003` 风控边界未关闭。

### CYCLE-2：模型发现与推理转发（对应 `AC-003`、`AC-004`）

- 期次定位：第二期。目标：模型清单 + 非流式 + 流式全部打通。
- 阶段：`models.go`（含 `filterExcludedModels`）→ `upstream.go` 指纹头 → `executor.go` 转发 → `stream.go` 流式。
- 收口条件：`AC-003`、`AC-004` 通过。
- **执行结果（2026-10-15）**：
  - `AC-003` **PASS**：`model.static` 与 `model.for_auth` 双路径均返回模型清单，均经 `filterExcludedModels` 出口过滤。
  - `AC-004` **PASS**：真实免费账号非流式返回 `content="PONG"` / `finish_reason=stop`；流式 11 个 emit 帧 + 1 个 close，含 `data: [DONE]` 终止帧并立即关闭连接。
  - 宿主桥 ABI 8/8 方法全通（`plugin.register` / `model.static` / `model.for_auth` / `executor.identifier` / `auth.identifier` / `scheduler.pick` / `executor.execute` / `executor.execute_stream`）。
  - 本轮补齐 6 个单测（CYCLE-2 覆盖缺口），变异验证通过。
  - 验证证据：`doc/5-tests/2026-10-15_210000_copilot插件CYCLE-2测试.md`。
  - 诚实边界：宿主桥是自建函数指针表的**等价复刻**，不等同真实 CPA 进程加载 `.so`；宿主侧路由注册、模型目录合并与流式转发到客户端的完整链路仍属 CYCLE-4 生产验收；`GAP-003` 未关闭。

### CYCLE-3：多账号池与管理面板（对应 `AC-005`）

- 期次定位：第三期。目标：多账号治理与可视化。
- 阶段：`scheduler.go` 选号 → `quota.go` 额度 → `management.go` 路由 → `panel.html` 面板。
- 收口条件：`AC-005` 通过。
- **执行结果（2026-10-15）**：
  - `AC-005` **PASS**：多账号启停 / 删除、429/5xx 换号判定、额度查询面板展示四条判据均通过。
  - 宿主桥管理路由 **12/12 PASS**（面板页 / 账号列表 / 状态别名 / 额度查询 / 批量启用 / 批量禁用 / 路由注册），14 条路由 + `/panel` 资源齐全。
  - **修复两个真实缺陷**（均对照 zcode 基线发现的遗漏）：① `cachedQuota` 缺 TTL 过期判定 → 补 `copilotQuotaTTL = 5 分钟`；② `handleDeleteAccount` 删除后未清理额度缓存 → 补 `invalidateQuota`。
  - 本轮新增 10 个单测（CYCLE-3 覆盖缺口），两处变异验证均被精确捕获。
  - 验证证据：`doc/5-tests/2026-10-15_220000_copilot插件CYCLE-3测试.md`。
  - 诚实边界：多账号真实换号（需两个真实账号在线）与真实上游额度数据未在本轮重复实测，属 CYCLE-4 生产验收范围。

### CYCLE-4：发布与生产验收（对应 `AC-006`、`AC-007`）

- 期次定位：第四期。目标：全链路发布。
- 阶段：CI workflow 同步 → registry → 图标 → 发布 → 生产热重载。
- 收口条件：`AC-006`、`AC-007` 通过。

### CYCLE-5：Kiro (AWS) 可行性评估

- 期次定位：第五期，**评估非实现**。目标：给出 Kiro 是否值得立项的结论与依据。
- 产出：Kiro 协议可行性报告（免费额度、OAuth 形态、逆向难度、风控风险）+ 立项建议。
- 进入条件：CYCLE-4 收口完成（Copilot 已跑通）。
- 收口条件：报告落盘，给出「立项 / 不立项」明确建议。

## 六、最小任务清单

### CYCLE-1

| ID | 任务 | 产出 | 验证点 | 完成条件 | 停止条件 | 依赖 |
|---|---|---|---|---|---|---|
| `T1-1` | 建 `copilot/` 骨架 | main.go / go.mod / VERSION | `cgo-shim-build.py copilot` 能过 build | 骨架编译通过且 registration 可返回 | 若 ABI 与 zcode 不兼容则停下核查 | 无 |
| `T1-2` | Device Flow 登录 | oauth.go | 面板/接口能拿到 user_code 与 URL | 轮询成功并落盘 | 若 client_id 失效则记录并停 | `T1-1` |
| `T1-3` | Copilot token 交换 | token.go | 用真实 GitHub token 换到 Copilot token | 拿到 token 且能解析过期时间 | 若端点 404/401 则记录并停 | `T1-2` |
| `T1-4` | Free 账号实测 | 实测记录 | 用 Free 账号跑 T1-2→T1-3 | `GAP-001` 有明确结论 | 若无法取得 Free 账号则标 `blocked` | `T1-3` |

### CYCLE-2 / 3 / 4 / 5

各周期的任务在进入该周期时按同一模板细化；**当前未拆细**，避免过度规划。周期内每个最小任务默认控制在 5 个文件以内，且必须独立闭环（实现 → 真实测试 → 6-review）后才推进下一个。

## 七、真实测试安排

- **测试入口**：`python scripts/cgo-shim-build.py copilot`（build + vet + test）。
- **依赖环境**：Windows 无 CGO 的本地环境；真实 GitHub 账号（Free + Pro 各一）；网络可达 `github.com` 与 `api.githubcopilot.com`。
- **样本 / 数据来源**：真实 GitHub 账号经 Device Flow 授权；不伪造 token。
- **通过标准**：shim 全绿；哨兵测试（临时插入必失败断言）确认测试真实进编译；真实账号端到端跑通一次 chat completions。
- **不适用说明**：apifox 接口用例 `N/A`——本插件不对外暴露独立 HTTP API（见需求文档 §7）。

## 八、任务完成条件 / 停止条件 / 最大推进边界

- **任务完成条件**：`AC-001` ~ `AC-007` 全部通过，且 CYCLE-5 评估报告落盘。
- **任务停止条件**：
  - `GAP-001` 实测结论为「Copilot Free 完全不可用」→ 暂停 CYCLE-2，先与用户确认插件是否仍对 Pro 开放。
  - GitHub 风控导致账号被封 → 立即停止实测，回滚测试产物，评估风险后再决定。
  - `client_id` / 端点失效且无替代 → 停止实施，标记为受阻。
- **最大推进边界**：本计划只覆盖 `copilot-provider` 插件与 Kiro 评估；不得顺手改动既有 8 插件、CPA 宿主或无关基础设施。

## 九、风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| Copilot Free 不可用（`GAP-001`） | 插件价值下降 | CYCLE-1 前置实测，结论前置 |
| GitHub 风控封号（`GAP-003`） | 用户账号受损 | 内置限速；README 风险提示；灰度观察 |
| client_id 为逆向所得、可能失效 | 登录链路中断 | 实测验证；README 注明可替换 |
| 上游端点/指纹头随 VS Code 版本变化 | 转发失败 | 指纹头集中 `upstream.go`，便于单点更新 |
| 与并行会话写集冲突 | 提交混入 | 按路径限定 `git commit -- <路径>` |

## 十、图形化总览

图形目的：展示从立项到生产验收的周期推进顺序与关键闸门。
关联 ID：`IMPL-COPILOT-PROVIDER-20261015`

```mermaid
%%{init: {'themeVariables': {'fontSize': '20px', 'fontSizeLabel': '16px'}, 'flowchart': {'nodeSpacing': 50, 'rankSpacing': 70}}}%%
flowchart LR
    subgraph P1[协议打通]
        A1[ABI 骨架] --> A2[Device Flow]
        A2 --> A3[Token 交换]
        A3 --> A4[Free 实测]
    end
    subgraph P2[转发]
        B1[模型发现] --> B2[指纹头]
        B2 --> B3[流式转发]
    end
    subgraph P3[多账号]
        C1[选号池] --> C2[额度] --> C3[面板]
    end
    subgraph P4[发布]
        D1[CI] --> D2[registry] --> D3[热重载]
    end
    A4 --> B1
    B3 --> C1
    C3 --> D1
    D3 --> E1[Kiro 评估]
```
