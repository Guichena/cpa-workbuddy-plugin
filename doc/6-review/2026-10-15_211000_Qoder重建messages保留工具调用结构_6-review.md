# 6-review 风格回归：Qoder 重建上游 messages 保留工具调用结构

- 回归时间：2026-10-15 21:10（北京时间）
- 来源对象：`doc/4-bugs/2026-10-15_193641_qwen-3.8-flash客户端声明tools致上游持续发起工具调用而推理提前结束根因诊断.md`
- 检查范围：本轮改动文件 —— `qoderwork/body.go` / `qoderwork/body_test.go` / `qoderwork/VERSION` / `qoderwork/main.go` / `qoderwork/CHANGELOG.md` 与 `qoder-ai/` 同名 5 文件，共 10 个（含发布后的注释补齐小改）
- 检查方式：`code-style-consistency-rules` 九步流水线（来源映射推导）
- 真实测试前置：隔离 worktree（`git worktree add --detach HEAD` + 仅复制本轮 8 文件，模拟 CI 构建对象）`cgo-shim-build.py qoderwork` / `qoder-ai` 均 build+vet+test 全绿；两组哨兵变异各精确 FAIL 一项后恢复全绿

## STYLE: PASS

## 改动文件清单

| 插件 | 文件 | 改动 |
| --- | --- | --- |
| qoderwork | `body.go` | `openAIMessage` 补 `ToolCalls`/`ToolCallID`/`Name`；`UnmarshalJSON` 同步解析；`buildQoderBody` 重建 messages 时按非空保留 |
| qoderwork | `body_test.go` | 新增 `TestOpenAIRequestPreservesToolStructure` / `TestBuildQoderBodyPreservesToolPairing` |
| qoderwork | `VERSION` / `main.go` | 0.9.39 → 0.9.40 |
| qoderwork | `CHANGELOG.md` | 新增 `## 0.9.40` 章节 |
| qoder-ai | 同上 5 个文件 | 逐行同构同步（0.1.19 → 0.1.20） |

## 检查步骤与证据

### STYLE-01 静态格式与编码一致性

- 证据：`STYLE-01：10 个文件 → UTF8=True / BOM=False / bareCR=0 / trailingWS=0 → PASS`
- 说明：`git show f74a5e8` 复核入库形态 —— 4 个 Go 文件与 CHANGELOG 入库均为纯 LF（`crlf=0`），无整文件重写；工作区呈 CRLF 是 `core.autocrlf=true` 的检出行为，与本轮无关。VERSION 保持既有尾换行（首版漏写尾换行已修正，diff 收敛为单行版本变化）。

### STYLE-02 命名与符号引用一致

- 证据：`STYLE-02：openAIMessage.ToolCalls / ToolCallID / Name → 与 openAIRequest.Tools 同域术语、Go 标准 MixedCase → PASS`
- 证据：`STYLE-02：json tag tool_calls / tool_call_id / name → 与 OpenAI 协议字段逐字一致 → PASS`

### STYLE-03 注释分层与定义位置

- 证据：`STYLE-03：body.go:UnmarshalJSON → [参数]/[返回]/最近修改时间 齐备，末行日期已更新为 2026-10-15 并含一句话改动说明 → PASS`
- 证据：`STYLE-03：body.go:openAIMessage → 新增字段的说明压缩为单行目的注释（"承载工具调用与工具结果，供重建上游 messages 时保持配对"） → PASS`
- **FIX 已执行**：首版把字段说明写成两行且含反事实推演（「丢弃会让模型无法判定探测已完成而重复发起同类调用」），命中 `comment-rules`「说明性注释只留目的，过程一律删除」驳回条件，已压为单行目的注释；`buildQoderBody` 本轮发生流程变化（新增三个非空条件分支）但函数头缺 `[参数]`/`[返回]`/`最近修改时间`（该函数头自 65f554c 起长期为此形态），按改动位点补齐闸门补全。
- 证据：`STYLE-03：重建循环处未新增注释 → 既有 "// Append the actual conversation" 已覆盖，三个 if 为语义自明的字段拷贝，加注释构成冗余 → PASS`

### STYLE-04 函数签名与参数结构

- 证据：`STYLE-04：buildQoderBody / UnmarshalJSON 签名均未变化，仅函数体与结构体字段扩展 → PASS`
- 证据：`STYLE-04：openAIMessage 字段类型 []any（工具调用为协议透传，不在插件侧重建模） → PASS`

### STYLE-05 结构与落点

- 证据：`STYLE-05：改动全部落在 body.go 请求构建层（结构体定义 → 解析 → 渲染），未越界到 stream / scheduler / management → PASS`
- 证据：`STYLE-05：测试落在同目录 body_test.go（仓库既有 Go 单测约定，test/ 只放 .mjs 脚本） → PASS`

### STYLE-06 公共复用与工具落点

- 证据：`STYLE-06：无新增 helper —— 复用既有 buildToolsProbe（上一轮 tools 对齐引入），未重复封装 body 渲染 → PASS`
- 说明：两插件逐行同构（含注释与测试）是仓库既有约定（qoderwork / qoder-ai 同源），非重复实现。

### STYLE-07 接口契约与数据访问

- 证据：`STYLE-07：三个新字段 json tag 均带 omitempty → 未声明工具的单轮问答请求形态零变化（回归既有 tools_alignment_test.go 与 body_test.go 全部用例通过可证） → PASS`
- 证据：`STYLE-07：[]any 原样透传工具调用，不重塑结构、不丢字段 → 上游契约变化点限于 messages 数组内容 → PASS`
- 说明：无 SQL / 无日志链路 / 无时区转换改动。

### STYLE-08 语言与框架特异写法

- 证据：`STYLE-08：len(m.ToolCalls) > 0 / m.ToolCallID != "" / m.Name != "" 三条件按非空追加，无嵌套三元、无 reflect 深拷贝 → PASS`
- 证据：`STYLE-08：UnmarshalJSON 内 wire 匿名结构体承接全部字段后统一赋值，与既有 Role/Content 写法同构 → PASS`

### STYLE-09 测试资产与编码前契约

- 证据：`STYLE-09：2 个新用例分别锚定解析层与渲染层——回退渲染逻辑时仅 TestBuildQoderBodyPreservesToolPairing FAIL、删除 UnmarshalJSON 赋值时仅 TestOpenAIRequestPreservesToolStructure FAIL（各精确一项，无交叉） → PASS`
- 证据：`STYLE-09：TestXxx 函数头按规则免写 [参数]/[返回]，写验证对象与判据防什么、无副作用 → PASS`
- 说明：编码前风格契约由 `code-generation-style-rules` 形成（跟随 body.go 既有 marker 体系与 omitempty 字段惯例）。

## 函数注释核对清单

```text
函数注释核对清单：
- [通过] qoderwork/body.go | UnmarshalJSON | [参数]: 是 | [返回]: 是 | 最近修改时间: 是(2026-10-15) | 一句话说明: 是
- [通过] qoderwork/body.go | buildQoderBody | [参数]: 是 | [返回]: 是 | 最近修改时间: 是(2026-10-15) | 一句话说明: 是
- [通过] qoder-ai/body.go | UnmarshalJSON | [参数]: 是 | [返回]: 是 | 最近修改时间: 是(2026-10-15) | 一句话说明: 是
- [通过] qoder-ai/body.go | buildQoderBody | [参数]: 是 | [返回]: 是 | 最近修改时间: 是(2026-10-15) | 一句话说明: 是
- [通过] qoderwork/body_test.go | TestOpenAIRequestPreservesToolStructure | [参数]: 免写 | [返回]: 免写 | 验证对象+判据: 是 | 副作用: 无
- [通过] qoderwork/body_test.go | TestBuildQoderBodyPreservesToolPairing | [参数]: 免写 | [返回]: 免写 | 验证对象+判据: 是 | 副作用: 无
- [通过] qoder-ai/body_test.go | 同上 2 个用例 | [参数]: 免写 | [返回]: 免写 | 验证对象+判据: 是 | 副作用: 无
- [通过] buildToolsProbe（既有函数，本轮未修改）| 基线一行说明，不做无关改动
```

## 补丁注释核对清单

```text
补丁注释核对清单：
- 本轮补丁位点: 0 个（三个非空条件为字段拷贝而非兼容分支，语义自明；既有 "// Append the actual conversation" 已覆盖该段作用）
```

## 结论

- **STYLE: PASS**
- 发现并修正 2 处：字段说明的反事实推演（压为单行目的注释）、`buildQoderBody` 函数头缺元信息（按改动位点补齐闸门补全）。修正后双插件 cgo-shim 复测全绿、gofmt 干净。
- 未发现命名跳变、落点越界、依赖方向反转、重复封装或测试资产错位。
- 注释修正为纯注释改动（不影响运行行为），以 `docs/comments` 类提交入库，随下一次 qoder 插件发布自然带上；不因此单独触发 0.9.41 / 0.1.21 发布。
- 本结论只覆盖写法、位置、格式与既有习惯，不判断业务正确性、需求覆盖或发布放行。
