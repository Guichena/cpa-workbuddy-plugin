# 6-review：copilot-provider 宿主 OAuth 弹窗设备码桥接页

STYLE: PASS

## 检查范围

- 测试证据：`python scripts/cgo-shim-build.py copilot` → build / vet / test 全绿（单测 30 例）；`node test/copilot/panel_render_repro.mjs` → 全部断言 PASS。
- 改动文件：`copilot/bridge.go`（新增）、`copilot/bridge_test.go`（新增）、`copilot/oauth.go`、`copilot/management.go`、`copilot/main.go`、`copilot/main_test.go`、`copilot/CHANGELOG.md`、`copilot/README.md`、`copilot/VERSION`。
- 规则来源：`PROJECT_STYLE.md`、`code-style-consistency-rules` references、本轮 `code-generation-style-rules` 风格契约。
- 检查步骤：由 `scripts/static_owner_router.py`（`--root C:/Users/luode/.qoder/skills`）按变更路径推导，命中 STYLE-01/02/03/04/05/08/09。

## 风格证据

- `STYLE-01` 静态格式与编码一致性：PASS。`copilot/` 六个 `.go` 文件 CRLF→LF 临时归一后 `gofmt -l` 输出为空、`gofmt -d` 逐文件无 diff；尾随空白 6 文件均为 0；无 BOM；`file` 判定全部 `UTF-8 text`。换行符形态与仓库既有 tracked `.go` 文件一致（CRLF 物理换行 + LF 归一后格式合规，沿用既有约定，不引入 eol 漂移）。
- `STYLE-02` 命名与符号引用一致：PASS。新增符号 `loginBridgeURL` / `handleLoginBridge` / `loginBridgePage` / `htmlEscape` 跟随 `copilot/` 既有 `handle<动作>` 与 `<对象>URL` 命名（对照 `handlePanelLoginStart` / `handlePanelLoginPoll` / `resolveCopilotBaseURL`）；`bridge_url` 元数据键跟随既有 snake_case 元数据键（`user_code` / `verification_uri` / `auth_url`）；resource 路径 `/login-bridge` 与 traework `/browser-login/bridge` 同层级语义。
- `STYLE-03` 注释分层与定义位置：PASS。`bridge.go` 四个函数均为「整体说明 + `[参数]` + `[返回]` + 最近修改时间 + 一句话改动原因」唯一完整层；`loginBridgePage` 的样式表与 HTML 片段未平摊函数注释格式；`bridge_test.go` 三个测试函数为单行说明，与 `main_test.go` 既有测试注释同构；`handleStartLogin` 与 `managementRegistration` 的函数头已同步更新最近修改时间与改动原因。
- `STYLE-04` 函数签名与参数结构：PASS。`handleLoginBridge(state string)` 单参数；`loginBridgePage(userCode, verificationURI, detail string)` 为同源字符串参数且无可选/扩展字段，未达收敛阈值（≥3 同源 + 可选字段），不引入结构体；无多行签名。
- `STYLE-05` 结构与落点：PASS。`bridge.go` / `bridge_test.go` 落 `copilot/` 扁平单包，与既有 18 个生产文件、4 个测试文件同层，未引入无需求子目录；`bridge_test.go` 与 `bridge.go` 同目录同包（跟随 `main_test.go` / `management_test.go` 既有 Go 包内测试约定）；无依赖方向越界。
- `STYLE-08` 语言与框架特异写法：PASS。无 `iota`；无函数内 `var (...)` 分组；常量集中在包级 `const` 块；`strings.NewReplacer` 用于 HTML 转义而非链式 `Replace`；`strings.Builder` + `WriteString` 渲染 HTML，规避 Go 原生字符串内反引号截断（跟随 `traework/browserlogin.go` 同款写法）。
- `STYLE-09` 测试资产与编码前契约：PASS。本轮产出编码前风格契约（跟随 `copilot/` 既有 `handle*` 命名、函数头注释格式、包内测试约定、`okEnvelope` 信封形态）；测试资产 `bridge_test.go` 落 `copilot/` 包内（跟随既有五插件 Go 包内测试约定，非新增特例），`test/copilot/` 镜像目录下既有 `panel_render_repro.mjs` / `host_bridge_probe.py` 未受本轮影响。

## 跟随依据

- **桥接页渲染**：`traework/browserlogin.go` 的 `browserLoginBridgePage` 已生产验证同款模式（`strings.Builder` + `WriteString` 逐段拼接、内联 `<style>`、错误分支渲染）；copilot 版本按其结构落地，去掉 traework 的轮询驱动逻辑（copilot 的 `pollDeviceLogin` 成功即消费会话，桥接页不得抢跑）。
- **resource 路由注册**：跟随 `traework/management.go` 的 `Resources` 注册形态（`{Path, Description}` 无 `Menu` 项 → 不进管理菜单）。
- **信封与响应**：`okEnvelope(pluginapi.ManagementResponse{StatusCode, Headers, Body})` 跟随 `copilot/panel.go` 的 `handlePanelPage` 与 `management.go` 的 `makeHTTPResponse`。
- **版本与 CHANGELOG**：`**Fix**` / `**Feat**` / `**Test**` / **涉及文件** 三段式跟随 `copilot/CHANGELOG.md` 0.1.0 与仓库既有插件 CHANGELOG 结构。

## 生产代码测试污染扫描

- 命令：`python C:/Users/luode/.qoder/skills/test-strategy-rules/scripts/scan_test_pollution.py --root . --diff-only`
- 本轮改动文件命中：`copilot/oauth.go:38` 的 `githubSlowDownStepSec` 判 `ORPHAN`（疑似死代码，prod_refs=0 / test_refs=0）。
- 归属核实：该常量由初始提交 `c546b57` 引入，本轮 diff **未触及**该行（`git diff HEAD -- copilot/oauth.go` 无 SlowDown 命中），属历史遗留孤儿常量，非本轮引入、非测试污染。
- 处置：按最小改动原则本轮不清理（不在本次改动范围内）；记为遗留项供后续单独治理。
- 其余全部命中（430 ORPHAN + 2 SUSPECT + 1 POLLUTION）均落在 `Python/pythoncore-3.15-64/` 第三方解释器源码树，与本仓库业务无关。
- 结论：**POLLUTION: PASS**（本轮改动无测试污染命中）。

## 边界声明

- 业务正确性：N/A + 原因：由真实测试负责 + 证据：`python scripts/cgo-shim-build.py copilot` 全绿 + 哨兵/变异反证。
- 需求覆盖与发布放行：N/A + 原因：不属于 6-review 职责 + 证据：实施总览 `IMPL-COPILOT-HOST-OAUTH-BRIDGE-20261016` 的 `AC-*`。
- 本审查只覆盖风格、格式、命名、注释、落点与测试资产归位；不判断桥接页在宿主弹窗中的实际渲染效果（需生产环境真实点击验证，属 TASK-006 发布验收）。
