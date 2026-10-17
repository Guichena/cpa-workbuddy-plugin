# QwenWork Provider CYCLE-02 多账号调度 6-review

- 日期：2026-10-18 02:10
- 范围：`qwenwork/` 的 6 个生产文件改动 + 1 个新增测试文件 + 1 个新增探针脚本
- 结论：**STYLE: PASS**

## 1. 检查项

| 维度 | 结论 | 说明 |
|---|---|---|
| 命名一致性 | PASS | 新增 `accountIdentity` / `foldCredentialIntoDoc` 与既有 `accountRoutable` / `authFileNameFor` 同族；`curID` 与既有 `usedAuthID` / `currentID` 语义对齐 |
| 局部风格延续 | PASS | 沿用同目录既有写法：`[参数]`/`[返回]`/`最近修改时间` 中文元信息、步骤编号注释、`strings.TrimSpace` 防御、错误串 `fmt.Errorf("...: %w", err)` |
| 注释语言 | PASS | 本轮新增与改写注释均为中文；协议字段名、错误原文按规则保留原文 |
| 注释颗粒度 | PASS | 函数头写完整元信息；方法体内用 `1.`/`2.` 编号说明流程；单行注释只写目的 |
| 位置归位 | PASS | 身份推导放 `main.go`（与 `stripProviderPrefix` 同区）；折叠逻辑放 `keepalive.go`（唯一调用方）；测试放同包 `multiaccount_test.go` |
| 无多余抽象 | PASS | 未引入接口、工厂或配置项；`accountIdentity` 是唯一被复用的纯函数，四处调用 |
| 无无关 diff | PASS | 未改任何既有插件的源码；未做批量格式化；`gofmt -l` 对改动文件为空 |
| 测试污染 | PASS | 生产文件中无 `_test` / `testOnly` / `isTest` 符号 |
| 错误处理 | PASS | 折叠基线损坏时返回错误而非静默重建；`persistAuthDirect` 拒绝相对/不安全路径 |
| 并发安全 | PASS | 改动未新增共享可变状态；冷却/会话表仍由既有互斥锁保护 |
| 文件规模 | 关注 | `main.go` 1003 行、`stream.go` 1025 行，均超 200 行建议值；属既有结构，本轮未扩张职责，未做无关拆分 |

## 2. 本轮暴露的既有问题（不在本轮修复范围）

- `workbuddy` 的 `usage_config.go` 存在与 C2-2 完全相同的 `scheduler_mode: off` 解析缺陷（`workbuddy/usage_config.go:123-131`）。本轮只改 `qwenwork`，未跨插件改动。
- `accountFailover.go` 头部注释仍写「Qoder 系专属：排队帧…110 日限额」，与 QwenWork 现状不符，属注释漂移，未在本轮改写以免扩大 diff。

## 3. 证据

- `python scripts/cgo-shim-build.py qwenwork` → build + vet + test 全绿
- WSL `CGO_ENABLED=1 go build -buildmode=c-shared` → 7,853,120 字节 ELF
- WSL `CGO_ENABLED=1 go test ./...` → ok 1.523s
- WSL `CGO_ENABLED=1 go vet ./...` → 无输出
- `gofmt -l`（CRLF 归一后）→ 空
- `test/qwenwork/multiaccount_probe.py` → 9/9 PASS
