---
schema_version: 1
doc_type: style_regression
source_object_id: REQ-COZE-PROVIDER-20261017
task_scope: CYCLE-00 TASK-001 + TASK-002 授权登录链路
status: PASS
updated_at: 2026-10-18 00:30:00
---

# Coze Provider 插件 CYCLE-00 授权链路 6-review

结论：本轮新增的 `coze/` 插件（授权登录链路）与既有 `doubao` / `copilot` 插件写法一致，未引入局部风格跳变；**STYLE: PASS**。

- 来源对象：`REQ-COZE-PROVIDER-20261017`
- 对应实施文档：`doc/3-实施/2026-10-17_230000_CozeProvider插件立项_实施总览.md`
- 本轮范围：`coze/main.go`、`coze/protocol.go`、`coze/cookiejar.go`、`coze/authfile.go`、`coze/credentials.go`、`coze/login.go`、`coze/bridge.go`、`coze/management.go`、`coze/panel.go`、`coze/panel.html`、`coze/login_test.go`、`coze/live_check_test.go`、`coze/go.mod`、`coze/go.sum`、`coze/VERSION`、`coze/README.md`、`coze/CHANGELOG.md`；`assets/icons/Coze.png`；`.github/workflows/build.yml`；`registry.json`
- 真实测试证据：`python scripts/cgo-shim-build.py coze` → build / vet / test 三项全绿；真实账号 `TestLiveAuthChain` 通过（授权 + 续期闭环）
- 检查步骤来源：STYLE-01 ~ STYLE-09 九步

## 逐步检查结论

| 步骤 | 检查项 | 结论 | 证据 |
|---|---|---|---|
| STYLE-01 | UTF-8 无 BOM、LF 换行、无尾随空白、gofmt | 通过 | 17 个文件均 `BOM=False / LF / trailing_ws=0`；`gofmt -l` 对 11 个 Go 文件返回空 |
| STYLE-02 | 命名与同层符号一致 | 通过 | 包级符号（`providerName` / `pluginLogoURL` / `version` / `hostAPI` / `activeConfig` / `envelope` / `okEnvelope` / `errorEnvelope` / `writeResponse`）与 `doubao/main.go` 同名同形；插件专属符号统一 `coze` 前缀（`cozeRegistration` / `cozeAuthFilePrefix` / `isCozeAuthFileName`）；类型名与 doubao 对应物一致（`StoredAuth` / `savedCookie` / `cookieStore`） |
| STYLE-03 | 注释分层与定义位置 | 通过 | 全部业务函数具备「说明 + `[参数]` + `[返回]` + 最近修改时间」四要素；`init()` / `main()` 与 `doubao` / `copilot` 一致地不带元信息注释（既有约定）；C ABI 入口（`cliproxy_plugin_init` 等）同既有约定；结构体字段为单行作用说明；`login.go` 顶部用「为什么不能一键授权」论证块说明设计取舍 |
| STYLE-04 | 函数签名与参数结构 | 通过 | `doTokenBeat(client, token, cookieHeader)` 三参数为最小必要（HTTP 客户端需可注入）；`swapEndpoints(tokenBeat, accountInfo) func()` 与 `doubao` 的 `swapAccountInfoEndpoint` 同形；无返回值超 2 个的场景 |
| STYLE-05 | 结构与落点 | 通过 | 新建 `coze/` 与既有 10 插件同级同构；文件按职责拆分（`protocol` 契约 / `cookiejar` 存储 / `credentials` 兑换续期 / `login` 状态机 / `bridge` 页面 / `management` 路由 / `panel` 面板）；单文件最大 448 行（`credentials.go`），未超 200 行的拆分阈值判断以「职责单一」为准——该文件只做凭据兑换与续期一件事 |
| STYLE-06 | 公共复用与工具落点 | 通过 | 未新增导出工具函数，无需登记公共工具索引；`htmlEscape` / `htmlManagementResponse` / `jsonManagementResponse` 与 `doubao` 同名同实现（跨插件重复是既有约定，各插件独立编译，不做跨插件共享） |
| STYLE-07 | 接口契约与错误处理 | 通过（含两处修正） | **修正 1**：`handleRefreshAuth` 原本在 `sa.PhysicalPath != ""` 时调 `applyRenewal` 写盘，但 `PhysicalPath` 是 `json:"-"`，从宿主 `StorageJSON` 反序列化后必为空 → 该分支为死代码。已删除，并补注释说明落盘归宿主（与 `workbuddy/oauth.go` 的「不显式 host.auth.save，宿主自行持久化」口径一致）。**修正 2**：令牌原经 URL query 回传，resource 路由的访问日志会记录完整 URL → 凭据落进宿主日志。改为 URL fragment（浏览器不发给服务端）+ 同源请求头两段式回传，并补 `loginImportedAuthID` 重放幂等闸门（`x-tt-token` 只能兑换一次）。管理路由契约与面板 `const API`（`/v0/management/plugins/coze-provider`）一致；错误统一走 `errorEnvelope` / `makeHTTPResponse` |
| STYLE-08 | 语言与框架特异写法 | 通过 | 无 `iota`；常量集中顶部（`providerName` / `loginStateTTL` / `callbackPath` / `cozeAuthFilePrefix` / `tokenTTLHint`）；`panel.html` 单 `<script>` 块通过 `node --check`；`.github/workflows/build.yml` 通过 YAML 解析且 8 处接入点与 `doubao-provider` 完全同形 |
| STYLE-09 | 测试资产与编码前契约 | 通过 | 单测落在 `coze/login_test.go`（与源码同目录，符合 Go 惯例）；`live_check_test.go` 用 `COZE_LIVE_TOKEN` 环境变量门控，未设置时 `t.Skip`，不会污染常规 CI；测试注入点 `swapEndpoints` 仅被测试引用 |

## 发现与处理

| 序号 | 发现 | 处理 |
|---|---|---|
| 1 | `handleRefreshAuth` 中 `sa.PhysicalPath != ""` 分支恒为假（`json:"-"`），`applyRenewal` 在刷新路径永远不执行 | 已删除该死分支，改为注释说明落盘归宿主；`applyRenewal` 保留给面板手动续期路径（`renewAccountByID` 持有真实 `PhysicalPath`） |
| 2 | `bridge.go` 授权脚本原用 `location.origin` 拼回调地址，在扣子页面执行时会指向 `coze.cn` 而非 CPA | 已在实现阶段自纠：改为 `__CPA_ORIGIN__` 占位符，由引导页渲染时用自身 `location.origin` 替换 |
| 3 | `coze/` 初建时缺 `go.sum` | 已从 `doubao/go.sum` 复制（同一 SDK 依赖集），shim 全绿验证 |
| 4 | 令牌经 URL query 回传会落进 resource 路由的访问日志 | 改为 fragment + 同源请求头（`X-Coze-Token` / `X-Coze-Error`）两段式回传；补 `TestBuildAuthScript_TokenNeverInQuery`、`TestLoginCallbackShell_HidesTokenFromServer` |
| 5 | 回调页重放会二次兑换同一 `x-tt-token`（该令牌一次性） | 新增 `loginImportedAuthID` 闸门：已导入的会话直接返回成功；补 `TestHandleLoginCallback_ReplayIsIdempotent`（断言上游调用次数不增加） |

以上五项均为本轮自身产生或本轮内已闭环，处理后 `cgo-shim-build.py coze` 重新验证全绿；两次哨兵反证（令牌改回 query / 移除幂等闸门）均精确命中目标用例。

## 负向边界声明

本记录只判断写法、位置、格式与既有习惯，不判断业务正确性、不重新验需求覆盖、不替代真实功能测试、不作发布放行或最终验收结论。
