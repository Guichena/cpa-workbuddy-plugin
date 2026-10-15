# copilot-provider 插件 6-review 风格回归（CYCLE-3 多账号池与管理面板）

- 审查对象：`copilot/` 插件 CYCLE-3 改动（额度缓存 TTL + 删除清理 + 10 个单测）
- 审查时间：2026-10-15
- 变更文件：`copilot/quota.go`、`copilot/management.go`、`copilot/management_test.go`（新增）、`test/copilot/host_bridge_probe.py`
- 结论：**STYLE: PASS**

## 一、检查范围与依据

按 `code-style-consistency-rules` 的静态 Owner 路由推导检查步骤，逐项执行：

| 步骤 | Owner | 结论 | 证据 |
|---|---|---|---|
| STYLE-01 静态格式与编码一致性 | code-style-consistency-rules | PASS | `gofmt -l`（LF 归一后）输出为空；`management_test.go` UTF-8 无 BOM；探针 `ast.parse` 通过 |
| STYLE-02 命名与符号引用一致 | naming-rules | PASS | 测试命名 `TestXxx` + 中文注释头；辅助函数 `mustWriteAccount` 沿用既有 `mustJSON` 的 `must` 前缀惯例 |
| STYLE-03 注释分层与定义位置 | comment-rules | PASS | 改动的 `cachedQuota` / `handleDeleteAccount` 均更新「最近修改时间 + 改动原因」；新增常量 `copilotQuotaTTL` 带用途注释（含「为何返回空而非陈旧值」的原因说明） |
| STYLE-04 函数签名与参数结构 | code-quality-rules | PASS | 未新增参数结构体；`cachedQuota` 签名不变；测试辅助函数单参数职责 |
| STYLE-08 语言与框架特异写法 | golang-patterns | PASS | 无 `iota`；测试内无函数内 `var (...)` 分组；`cachedQuota` 在 `RLock` 内仅取指针、锁外做 `time.Parse`（避免持锁解析） |
| STYLE-09 测试资产与编码前契约 | test-program-rules | PASS | 新增 `copilot/management_test.go` 与被测源码同目录（Go 包内测试约定），未新建目录；探针扩展落在既有 `test/copilot/` |

## 二、跟随依据

本轮为**在既有插件上补测试与修缺陷**，跟随对象：

- **额度 TTL 语义**：`zcode/quota.go:149` 定义 `zcodeQuotaTTL = 5 * time.Minute`、`:532-545` 的 `cachedQuota` 按 `FetchedAt` 判过期。本插件的 `copilotQuotaTTL` 采用同一取值与同一判定形态，仅命名随本插件前缀。
- **删除后失效额度**：`zcode/management.go:413` 在 `deleteAuthFileDirect` 成功后调 `invalidateQuota(authID)`。本插件照此补齐，位置一致（删除成功之后、返回之前）。
- **测试风格**：与 `copilot/main_test.go` 既有 18 个用例同构——表驱动用 `[]struct{...}`、`t.Fatalf` 带实测值与期望值、临时目录用 `t.TempDir()` + `setAccountsDir` 隔离。
- **探针扩展**：管理路由判定沿用「管理响应体由宿主原样写回，看 StatusCode 而非 ok 信封」的既有约定。

## 三、发现与处置

**修复的两个真实缺陷**（均为对照 zcode 基线发现的遗漏，非风格问题）：

1. `cachedQuota` 缺少 TTL 过期判定 → 面板会长期展示陈旧额度。已补 `copilotQuotaTTL`。
2. `handleDeleteAccount` 删除后未清理额度缓存 → 面板残留已删账号条目。已补 `invalidateQuota`。

**验证资产的两处探针缺陷（已修复，非插件问题）**：

1. `ManagementResponse.StatusCode` 无 json tag，序列化后是 Go 字段名 `StatusCode` 而非 `status_code`；探针用错键导致 6 项路由全判 FAIL。
2. `ManagementRequest.Body` 是 `[]byte`（JSON 中 base64），探针直接传字符串导致 POST 路由取不到 body。

两处均已修复并在代码中留下原因注释；修复后管理路由判定转为 12/12 PASS。

无风格跳变、无临时文件残留、无路径漂移。

## 四、负向边界声明

本审查只覆盖风格、格式、命名、注释、落点与测试资产归位；**不判断**业务逻辑正确性、需求覆盖度与发布放行。`AC-005` 结论以 `doc/5-tests/2026-10-15_220000_copilot插件CYCLE-3测试.md` 为准，本审查不构成其通过结论。
