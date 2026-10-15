# copilot-provider 插件 6-review 风格回归（CYCLE-4 发布链路配置）

- 审查对象：`copilot-provider` 首次入库的发布链路配置（CI workflow / registry / 图标 / README / CHANGELOG）
- 审查时间：2026-10-15
- 变更文件：`.github/workflows/build.yml`、`registry.json`、`README.md`、`assets/icons/Copilot.png`、`copilot/CHANGELOG.md`、`copilot/README.md`、`test/copilot/host_bridge_probe.py`、`copilot/` 全量源码（首次入库）
- 结论：**STYLE: PASS**

## 一、检查范围与依据

按 `code-style-consistency-rules` 的静态 Owner 路由（`scripts/static_owner_router.py`，`repository_root=D:/谷歌云盘/luode-skills`）推导检查步骤，逐项执行：

| 步骤 | Owner | 结论 | 证据 |
|---|---|---|---|
| STYLE-01 静态格式与编码一致性 | code-style-consistency-rules | PASS | `copilot/*.go` LF 归一后 `gofmt -l` 输出为空；`copilot/` 全量文件 UTF-8 无 BOM；`test/copilot/host_bridge_probe.py` `ast.parse` 通过；`panel_render_repro.mjs` `node --check` rc=0 |
| STYLE-02 命名与符号引用一致 | naming-rules | PASS | 插件 id `copilot-provider` 与源码目录 `copilot/` 的映射沿用既有 `<id>`/`<src>` 双列约定；测试辅助 `mustWriteAccount` 跟随 `main_test.go` 既有 `mustJSON` 的 `must` 前缀；registry `id`/`name`/`logo` 字段命名与其余 8 条完全同构 |
| STYLE-03 注释分层与定义位置 | comment-rules | PASS | `copilot/` 函数注释头含 `[参数]`/`[返回]` 覆盖率 56%（109 函数 / 61 完整），为五插件最高（zcode 36% / qoder-ai 17% / traework 15% / cursor 0%）；`randomUUID` 等新增函数带「最近修改时间 + 改动原因」 |
| STYLE-04 函数签名与参数结构 | code-quality-rules | PASS | 本轮无新增生产函数；配置类改动不引入参数结构体 |
| STYLE-05 目录与包结构 | package-structure-rules | PASS | 新增 `copilot/` 与既有 `qoder-ai/`（72 个 .go 扁平）、`workbuddy/` 同为扁平单包结构，未引入无需求子目录；图标落 `assets/icons/` 跟随既有兄弟资产 |
| STYLE-06 公共工具边界 | common-util-rules | PASS | `copilot/util.go` 只承载本插件私有工具（`randomHex`/`randomUUID`/`GitHubUserInfo`），未被其它插件复用；项目无跨插件公共 Go 工具包，不存在重复封装 |
| STYLE-07 时间处理一致性 | time-util-rules | PASS | 时间统一用标准库 `time`（`RFC3339` 序列化 + `time.Parse`/`time.Since`/`time.Before` 判定），与 `zcode`、`qoder-ai` 既有口径一致；项目无公共 time-util 包，不引入新抽象 |
| STYLE-08 语言与框架特异写法 | golang-patterns / frontend-ui-visual-rules | PASS | 无 `iota`；无函数内 `var (...)` 分组；`panel.html` 内联 JS 单块 `node --check` rc=0；面板主题变量跟随既有 CPA 面板规范 |
| STYLE-09 测试资产与编码前契约 | test-program-rules / code-generation-style-rules | PASS | `*_test.go` 与被测源码同目录，跟随既有五插件 Go 包内测试约定（非新增特例）；宿主 ABI 探针与面板回归脚本落 `test/copilot/` 镜像目录 |

## 二、跟随依据

本轮为**新增插件的发布链路接入**，跟随对象为仓库既有 8 插件：

- **CI workflow**：`.github/workflows/build.yml` 的 8 处（头部注释、tag 触发、dispatch options、test matrix、build matrix ×5、build-cross ×2、release `if` 条件、tag 解析分支、DISPLAY 映射）全部按 `zcode-provider` / `cursor-provider` 既有条目 1:1 复制，仅替换 id/src/runner/ext。矩阵计数：test 9 / build 45（含 5 copilot）/ build-cross 18（含 2 copilot）。
- **registry 条目**：字段顺序与 `cursor-provider` 完全同构（`id`/`name`/`description`/`author`/`version`/`repository`/`logo`/`homepage`/`license`/`tags`/`install`），`install.type` 为 `direct`（schema_version 2），`validate-registry.py` 输出 `OK registry.json: 9 plugin(s), schema_version=2`。
- **图标**：`assets/icons/Copilot.png` 采用 640×640 RGBA（与 `Cursor.png` 512×512 同规格量级），由 `imagegen` CLI 通道真实生成后 LANCZOS 缩放，未用脚本合成冒充。
- **README**：插件计数 8→9、全景表新增行、章节正文、仓库结构树、商店安装列表、`config.yaml` 示例、shim 验证命令列表，全部按既有行文格式插入。
- **CHANGELOG**：改用既有插件的 `**Feat**` / `**Test**` / **涉及文件** 三段式（原稿为纯 bullet 列表，与 zcode/cursor 的 CHANGELOG 结构不符）。

## 三、发现与处置

**修复的一处真实风格偏离**：

`copilot/CHANGELOG.md` 原为单一 bullet 列表，缺少 `**Feat**` / `**Test**` 分组标题与 **涉及文件** 清单 —— 与仓库既有 `zcode/CHANGELOG.md`、`cursor/CHANGELOG.md` 的段落结构不一致。已按既有三段式重写。

**修正的一处文档失真**：

`test/copilot/host_bridge_probe.py` 的模块 docstring 声明「不入库的临时验证资产」，但该文件本轮随 `copilot/` 一并入库；同时 `.so` 路径硬编码为 `/tmp/copilot_build/copilot.so`。已将 docstring 改为正式用法说明，并把路径改为 `COPILOT_SO` 环境变量可覆盖（保留原默认值，不改变既有调用方式）。

**未处置的对照项（判定为跟随既有约定，不改）**：

`model.for_auth` 返回的 `ModelInfo` 仅含 4 个字段。经与 `zcode/main.go` 同位置逐字节比对为一致实现，属仓库既有约定，非本插件缺陷；按最小改动原则不改动。

## 四、生产代码测试污染扫描

- 扫描范围：`copilot/*.go` 中全部非 `_test.go` 文件。
- 判据：`"testing"` 导入、`Test*`/`Benchmark*`/`Fuzz*` 导出函数、`testHook`/`testOnly`/`forTest` 类测试专用钩子、`t.TempDir`/`t.Fatalf` 等测试 API 调用。
- 结论：**POLLUTION: PASS**（命中 0）。

## 六、OCR 代码审查

按 `code-change-finalization-gate-rules` 的 `references/ocr-review/review-workflow.md` 执行，路径映射取自 `rule-path-map.md`：

| 语言 | 规则文件 | 审查范围 |
|---|---|---|
| Go | `rule_docs/go.md` | `copilot/*.go`（18 个生产文件） |
| JS | `rule_docs/ts_js_tsx_jsx.md` | `copilot/panel.html` 内联脚本、`test/copilot/panel_render_repro.mjs` |
| Python | `rule_docs/python.md` | `test/copilot/host_bridge_probe.py` |
| YAML | `rule_docs/github_workflows.md` | `.github/workflows/build.yml` |

### 6.1 Go 规则核验

| 条款 | 结论 | 证据 |
|---|---|---|
| 安全敏感随机源须用 `crypto/rand` | PASS | `util.go:4` 导入 `crypto/rand`，`randomHex`/`randomUUID`（`X-Request-Id`）均用其生成；全仓无 `math/rand` |
| 路径遍历防护 | PASS | `isSafeCopilotAuthPath` 三重校验：拒绝 `../` 与 `/..`、`filepath.Base` 须匹配 `copilot-*.json` 命名、`base == Base(Clean(path))`；调用点覆盖读/写/删全路径（`authfile.go:173/177/214/335/341`） |
| 凭据不得进入日志/错误 | PASS | 错误信息只含 `auth_id` 与状态码，无 token 原值；面板展示走 `maskAPIKey`（`key[:4]+"...."+key[-4:]`）；`management.go:331` 批量操作错误用 `maskAPIKey(token)` 脱敏 |
| `resp.Body` 全路径关闭 | PASS | 6 个出站请求点全部覆盖：5 处 `defer resp.Body.Close()`，`executor.go:129` 在错误分支手动 Close 后 `cancel()`（与成功分支的 defer 互补，无重复无遗漏） |
| goroutine 生命周期与取消 | PASS | 唯一后台 goroutine `pumpUpstreamStream` 的 `defer` 内执行 `cancel()` + `resp.Body.Close()` + `streamClose(streamID)`；`for` 循环首行 `select ctx.Done()` 保证可退出；`ctx` 传 `context.Background()` 与 `zcode/executor.go:198` 基线一致 |
| 锁与共享状态 | PASS | 全部 `RLock`/`RUnlock` 与 `Lock`/`Unlock` 成对（手动解锁路径为提前返回，已逐处核对）；`models.go:79-86` 在 `RLock` 内仅取数据、锁外做 `time.Now()` 比较；`cachedQuota` 锁外做 `time.Parse` |
| 不可复制同步原语 | PASS | `sync.RWMutex`/`sync.Mutex` 均为包级变量或嵌入结构体指针持有，无按值传递/复制 |
| 无限重试与忙等待 | PASS | `stream.go:81` 的 `for` 有 `ctx.Done()` 退出路径与 `[DONE]`/`event: error` 哨兵；重试上限硬编码 3 次（`executor.go:100`） |
| 边界索引 | PASS | `maskAPIKey` 有 `len<=8` 前置分支；`management.go:157` 有 `len(vals)>0` 守卫；`scheduler.go:78` 在 `len(candidates)==0` 早退之后 |

### 6.2 JS 规则核验

| 条款 | 结论 | 证据 |
|---|---|---|
| XSS 防护 / innerHTML 安全 | PASS | 所有用户可控数据经 `esc()`（转义 `& < > " '`）后插入；`card()` 内 `label`/`username`/`auth_id`/`account_type`/`expires_at` 全部 `esc()` 包裹；纯文本场景用 `textContent`（`panel.html:200-201/392`） |
| 禁止 `eval` / `Function` / 字符串 `setTimeout` | PASS | 全仓无命中 |
| 禁止 `document.write` | PASS | 无命中 |
| 原型链安全 | PASS | 无 `.prototype` 赋值 |
| 敏感信息暴露 | PASS | 面板只展示 `api_key_masked`，不返回原值 |

### 6.3 Python 规则核验

| 条款 | 结论 | 证据 |
|---|---|---|
| 可变默认参数 | PASS | 唯一默认值为 `SO = os.environ.get("COPILOT_SO", "/tmp/...")`（字符串不可变） |
| 资源管理 | PASS | 无文件/socket 未关闭路径；ctypes 内存由 `libc.malloc`/`libc.free` 配对管理，`host_call` 分配、`host_free` 释放 |
| 边界与空值 | PASS | `req_len` 为 0 时不构造 buffer；`out.ptr and out.len` 双条件守卫后再 `string_at` |
| 凭据硬编码 | PASS | 凭据经 `COPILOT_GH_TOKEN` 环境变量读入，脚本不回显原值 |

### 6.4 工作流规则核验

| 条款 | 结论 | 证据 |
|---|---|---|
| 语法与触发器完整 | PASS | `yaml.safe_load` 解析通过，jobs 为 `test`/`build`/`build-cross`/`release`；`copilot-provider` 已加入 tag 触发与 dispatch options |
| 第三方 action 版本 | PASS | 全部使用语义化版本标签（`checkout@v4`、`setup-go@v5`、`upload-artifact@v4`、`go-cross/cgo-actions@v1`），无未固定版本 |
| 权限最小化 | PASS | 仅 `contents: write`（创建 Release 与 push tag 所需） |
| 无硬编码敏感信息 | PASS | 凭据仅经 `${{ github.token }}` / `secrets.*` 引用；命中的 "token" 均为 `workbuddy-token-usage` 插件名 |

### 6.5 审查结论

**OCR 审查:PASS** —— 未发现 Critical 或 High 级别问题。Go 侧的错误处理、并发安全、资源清理、路径校验四项关键面均与 `zcode` 基线同构；JS 侧 XSS 防护完整；工作流未引入新的权限或版本固定缺陷。

## 七、负向边界声明

本审查只覆盖风格、格式、命名、注释、落点、测试资产归位与 OCR 行级审查；**不判断**业务逻辑正确性、需求覆盖度与发布放行。`AC-006` 结论以 `scripts/cgo-shim-build.py copilot` 全绿为准，`AC-007` 以发布链路远端验证为准，本审查不构成其通过结论。
