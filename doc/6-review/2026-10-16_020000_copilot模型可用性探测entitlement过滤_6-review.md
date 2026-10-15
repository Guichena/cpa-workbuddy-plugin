# 6-review：copilot-provider 模型可用性探测（entitlement 过滤）

STYLE: PASS

## 检查范围

- 改动文件：`copilot/models.go`（修改，+125）、`copilot/models_discovery_test.go`（修改，+259）、`copilot/main.go`（版本号）、`copilot/VERSION`、`copilot/CHANGELOG.md`。
- 规则来源：`PROJECT_STYLE.md`、`code-style-consistency-rules` references、本轮 `code-generation-style-rules` 风格契约。
- 检查步骤：由 `code-style-consistency-rules/scripts/static_owner_router.py` 按变更路径推导，命中 STYLE-01/02/03/04/07/08/09 七步。

## 风格证据

- `STYLE-01` 静态格式与编码一致性：PASS。5 个文件均 UTF-8 无 BOM、纯 LF（CRLF 计数 0）、尾随空白 0 行。`gofmt -l` 对 `models.go` / `models_discovery_test.go` / `main.go` 输出为空（exit 0）。
- `STYLE-02` 命名与符号引用一致：PASS。新增符号 `probeUsableModels`（批量）/ `probeModelCallable`（单个）构成明确的「批量 / 单元」命名对，语义与既有 `cachedModelsForIndex` / `authModelsForIndex` 的「后缀区分职责」族一致；`stringSliceEqual` 为小驼峰职责直述；测试辅助 `probeServerForTest` 跟随 `copilot/` 既有 `xxxForTest` 后缀约定（`upstreamItemForTest` / `pickForTest`）。
- `STYLE-03` 注释分层与定义位置：PASS。两个探测函数均为「整体说明 + `[参数]` + `[返回]` + 最近修改时间」完整层；`probeUsableModels` 的多要点说明（探测手法 / 零消耗依据 / 并发上限 / 失败策略）采用列表式分行，列表与后续段落之间保留空注释行（gofmt 要求，已修正一处）。`stringSliceEqual` 为 3 行短函数，注释保持两行式。测试函数为单行说明，与 `models_discovery_test.go` 既有测试注释同构。
- `STYLE-04` 函数签名与参数结构：PASS。`probeUsableModels(ctx, sa, cfg, candidates)` 四参数、`probeModelCallable(ctx, baseURL, token, modelID, cfg)` 五参数，均单行声明、无多行参数列表；参数模型与同包 `fetchUpstreamModels(ctx, sa)` / `pingUpstreamOnce(sa, model)` 同族（ctx 前置、配置后置）。未达结构体收敛阈值。
- `STYLE-07` 接口契约与数据访问：PASS。`fetchUpstreamModels` 的对外契约（返回 nil 由调用方兜底）未变，仅在成功路径内追加探测，调用方 `authModelsForIndex` / `oauth.go` 无需改动；探测结果同步落盘复用既有 `writeAuthFileDirect`，不新增写路径；`stringSliceEqual` 守卫避免模型清单未变化时的无意义落盘。
- `STYLE-08` 语言与框架特异写法：PASS。无 `iota`；无函数内 `var (...)` 分组声明（`var wg sync.WaitGroup` / `var usable []string` 均逐行声明）；并发用 `sync.WaitGroup` + 带缓冲 channel 信号量（`make(chan struct{}, maxConcurrency)`），goroutine 内 `defer wg.Done()` 与 `defer func(){ <-sem }()` 成对；`results` 按索引写入、`wg.Wait()` 后统一读取，无数据竞争（`go test -race` 未启用但索引写入模式安全）。`strings.Contains(strings.ToLower(...))` 用于错误文本口径归一，属正当用途。
- `STYLE-09` 测试资产与编码前契约：PASS。测试落 `copilot/` 包内（与 `bridge_test.go` / `main_test.go` / `management_test.go` / `scheduler_test.go` 同层同包）；新增 6 个用例均走 `httptest` 桩服务端，与既有 `TestPingUpstreamOnce*` 三例同构（`activeConfig.Store(cfg)` 注入 `BaseURL` + `defer` 还原）。未新增根 `test/` 镜像文件（本轮无新脚本资产）。

## 跟随依据

- **探测手法**：`probeModelCallable` 复用既有 `applyCopilotHeaders`（`upstream.go:121`）与 `resolveCopilotBaseURL`（`upstream.go:62`），不另起一套指纹注入；`modelHTTPClient`（15s 整体超时）用于探测，与 `fetchUpstreamModels` 的 `/models` 请求同客户端，区别于推理用的 `inferenceHTTPClient`。
- **并发原语**：`sync.WaitGroup` + 带缓冲 channel 信号量是 Go 标准并发限流写法，与同包 `modelCache` 的 `sync.RWMutex` 用法族一致；未引入 `errgroup` 等新依赖（遵循「最小实现优先级阶梯」第 5 级）。
- **测试构造**：`probeServerForTest` 以 `gjson.GetBytes(body, "model")` 解析请求体，与 `active_ping.go:146` 的 `gjson.Get` 用法同族；`activeConfig.Store(cfg)` / `defer activeConfig.Store(prevCfg)` 与既有三例逐字同构。
- **降级语义**：探测整体无结果时保留 `/models` 过滤清单，与 workbuddy `resolveModels`（`workbuddy/models.go:160`）「动态失败回退静态」的既有兜底方向一致（宁多勿空）。

## 生产代码测试污染扫描

- 命令：`grep -n "httptest\|_test\b\|testing\." copilot/models.go copilot/active_ping.go`
- 本轮改动文件命中：**无**（输出为空）。
- 结论：**POLLUTION: PASS**（本轮改动无测试污染命中）。

## 边界声明

- 业务正确性：N/A + 原因：由真实测试负责 + 证据：cgo-shim build/vet/test 全绿 + 哨兵反证 4/4 精确 FAIL（禁用探测过滤 / 探测判据写反 / 移除降级保留 / 探测改串行）+ 真实 `.so` ABI 探针 5/5 PASS + 生产账号实测（25 条候选 → 10 条可调用，配额零消耗）。
- 需求覆盖与发布放行：N/A + 原因：不属于 6-review 职责 + 证据：Bug 主文档 `BUG-COPILOT-MODEL-ENTITLEMENT-20261016` 的修复与验证章节。
- 本审查只覆盖风格、格式、命名、注释、落点与测试资产归位；不判断修复后在生产环境的模型展示与测试通过率（属发版后验收事项）。
