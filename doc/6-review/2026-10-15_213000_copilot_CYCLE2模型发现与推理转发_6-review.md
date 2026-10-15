# copilot-provider 插件 6-review 风格回归（CYCLE-2 模型发现与推理转发）

- 审查对象：`copilot/` 插件 CYCLE-2 改动（单测补齐 + 宿主桥验证资产）
- 审查时间：2026-10-15
- 变更文件：`copilot/main_test.go`（新增 6 个用例）、`test/copilot/host_bridge_probe.py`（新增验证资产）
- 结论：**STYLE: PASS**

## 一、检查范围与依据

按 `code-style-consistency-rules` 的静态 Owner 路由推导检查步骤，逐项执行：

| 步骤 | Owner | 结论 | 证据 |
|---|---|---|---|
| STYLE-01 静态格式与编码一致性 | code-style-consistency-rules | PASS | `gofmt -l`（LF 归一后）输出为空；`main_test.go` UTF-8 无 BOM；Python 资产 `ast.parse` 通过 |
| STYLE-02 命名与符号引用一致 | naming-rules | PASS | 测试命名沿用 `TestXxx` + 中文注释头，与既有 12 个用例同构；探针文件用 snake_case（Python 生态约定） |
| STYLE-03 注释分层与定义位置 | comment-rules | PASS | 新增用例均有中文用途注释；`TestHandleExecStreamNoAccount` 补充「为何断言事件而非 error」的原因注释（非显然契约） |
| STYLE-04 函数签名与参数结构 | code-quality-rules | PASS | 测试辅助 `mustJSON` 复用既有实现，未新增辅助函数；无参数结构体引入 |
| STYLE-08 语言与框架特异写法 | golang-patterns | PASS | 无 `iota`；测试内无函数内 `var (...)` 分组；无未使用导入 |
| STYLE-09 测试资产与编码前契约 | test-program-rules | PASS | 单测落在既有 `copilot/main_test.go`（Go 包内测试约定）；宿主桥探针落 `test/copilot/`，与 `test/zcode/`、`test/copilot/panel_render_repro.mjs` 命名模式一致 |

## 二、跟随依据

本轮为**在既有插件上补测试**，跟随对象：

- Go 单测：与 `copilot/main_test.go` 既有 12 个用例同构——`TestXxx(t *testing.T)` + 顶部中文用途注释 + `t.Fatalf` 带实测值/期望值。
- 断言风格：`TestHandleExecStreamNoAccount` 的「流式错误走事件而非 Go error」判断，先在 `zcode/executor.go` 核对基线（同样 `streamEmitError` + 返回 ok 信封），确认同构后才按事件断言，未凭猜测写测试。
- 探针资产：`test/copilot/host_bridge_probe.py` 复用项目既有的「临时验证程序不入库源码、只落 `test/`」模式（对齐 `trae-local-verify-rules` 的 verify_main 定位）。

## 三、发现与处置

**过程中一度误判、经基线核对后撤回的事项（记录备查）**：无效凭据下 `model.for_auth` 返回的 `ModelInfo` 仅含 4 个字段（`ID`/`Object`/`OwnedBy`/`DisplayName`），缺少 `Name`、`ContextLength`、`MaxCompletionTokens`，与 `model.static` 的元数据口径不一致。核对 `zcode/main.go:297-315` 后确认**基线实现逐字同构**，属仓库既有约定而非本插件缺陷，按最小改动与跟随局部风格原则不予修改。

**验证资产的三次探针缺陷（已修复，非插件问题）**：真实凭据端到端首轮出现「流式无任何事件」与「`[DONE]` 检测为 0」两个现象，逐一排查后确认全部源于探针自身：

1. `executorStreamRequest.StreamID` 的 JSON tag 是 `stream_id`（Go 的 JSON 匹配不跨下划线），探针传 `StreamID` 导致插件拿不到 stream id、`streamEmit` 早退；
2. `pumpUpstreamStream` 在插件侧异步执行，探针进程提前退出，`stream_events` 恒空；
3. `host.stream.emit` 的 `payload` 是 `[]byte`（JSON 中 base64），直接字符串匹配 `[DONE]` 必然落空。

三处均已修复并在代码中留下原因注释；修复后流式契约判定 `PASS`（11 emit + 1 close + 1 个 `[DONE]` 帧）。

无风格跳变、无临时文件残留、无路径漂移。

## 四、负向边界声明

本审查只覆盖风格、格式、命名、注释、落点与测试资产归位；**不判断**业务逻辑正确性、需求覆盖度与发布放行。真实凭据端到端（`AC-003`/`AC-004`）结论以 `doc/5-tests/2026-10-15_210000_copilot插件CYCLE-2测试.md` 为准，本审查不构成其通过结论。
