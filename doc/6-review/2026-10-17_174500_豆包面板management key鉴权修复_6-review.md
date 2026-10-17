---
schema_version: 1
doc_type: style_regression
source_object_id: BUG-DOUBAO-PANEL-NO-MGMT-KEY-20261017
task_scope: 豆包面板补齐 management key 鉴权（0.1.1）
status: PASS
updated_at: 2026-10-17 17:45:00
---

# 豆包面板 management key 鉴权修复 6-review

结论：本轮新增的三段式 key 获取代码与 `copilot` / `workbuddy` / `qoderwork` / `zcode` / `traework` 五个既有插件**逐行同构**（仅 `SS_KEY` 值与新增的 `showAuth` / `saveKey` 两函数不同，后者与 `workbuddy` 一致），未引入新的局部风格跳变；**STYLE: PASS**。

- 来源对象：`BUG-DOUBAO-PANEL-NO-MGMT-KEY-20261017`
- 对应 Bug 文档：`doc/4-bugs/2026-10-17_174000_豆包面板缺少management key致导入报missing management key根因诊断.md`
- 本轮范围：`doubao/panel.html`、`doubao/CHANGELOG.md`、`doubao/VERSION`、`doubao/main.go`、`test/doubao/panel_auth_repro.mjs`（新增）、`test/doubao/panel_accounts_repro.mjs`、`test/doubao/panel_import_repro.mjs`
- 检查步骤来源：`python scripts/static_owner_router.py --root "C:/Users/luode/.qoder/skills" --changed doubao/panel.html --changed doubao/CHANGELOG.md --changed doubao/main.go --changed test/doubao/panel_auth_repro.mjs`，命中 **STYLE-01 / 02 / 03 / 04 / 05 / 08 / 09** 七步

## 逐步检查结论

| 步骤 | 检查项 | 结论 | 证据 |
|---|---|---|---|
| STYLE-01 | UTF-8 无 BOM、LF、无尾随空白 | 通过 | `panel.html` / `CHANGELOG.md` / `VERSION` / `panel_auth_repro.mjs` 均 `bom=False / crlf=0 / trailing_ws=0`；`panel.html` 单 `<script>` 块经 `node --check` 通过 |
| STYLE-02 | 命名与符号引用一致 | 通过 | `SS_KEY = "doubao-mgmt-key"` 与五个兄弟插件的 `<id>-mgmt-key` 模板一致；`getKey` / `readPanelKey` / `readUrlKey` / `persistKey` / `authHeaders` / `showAuth` / `saveKey` 全部沿用既有命名，无自创新名 |
| STYLE-03 | 注释分层与定义位置 | 通过 | 保留既有「management key acquisition (3 fallbacks, zero config)」块注释结构；新增中文说明只解释非显然约束（宿主 401 形态、为何必须落 sessionStorage），未添加解释「做了什么」的冗余注释；`api()` 内 401 分支只保留一句「隐私模式下不可写时忽略」 |
| STYLE-04 | 函数签名与参数结构 | 通过 | 新增函数均为零参或无参多返回值形态，与既有插件同名函数签名完全一致；`api(path, opts)` 签名未变 |
| STYLE-05 | 结构与落点 | 通过 | key 获取块紧邻 `const API` 之后、业务函数之前，与五个兄弟插件的落点完全一致；新增测试落 `test/doubao/`（与 `panel_accounts_repro.mjs` / `panel_import_repro.mjs` 同级）；未新增目录、未跨模块引用 |
| STYLE-08 | 语言与框架特异写法 | 通过 | 无 `iota`（`grep -c iota doubao/*.go` 无命中）；无 `fmt.Print` / `log.Print` 控制台输出；面板 JS 保持 ES5 风格（`var` 未引入、`const`/`function` 与既有块一致），未混用 `let`/箭头函数造成同文件风格割裂 |
| STYLE-09 | 测试资产与编码前契约 | 通过 | 新增 `panel_auth_repro.mjs` 与既有两个 repro 脚本同构（Node `vm` + DOM 桩 + `PASS/FAIL` 输出 + 退出码语义）；两处既有桩的 `sessionStorage` 同步升级为 Map 实现，属本轮修复的必要连带改动，未扩大范围 |

## 同构性核对（对照 copilot/workbuddy）

| 代码块 | 与既有插件差异 | 结论 |
|---|---|---|
| 常量与工具函数（`ENC_PREFIX` / `SECRET_SALT` / `PANEL_STORE` / `_enc` / `_dec` / `_keyBytes` / `_xor` / `_b64d` / `deobfuscate` / `isEmbedded`） | 逐字相同 | 完全同构 |
| `readPanelKey` / `readUrlKey` / `persistKey` / `getKey` / `authHeaders` | 仅注释措辞微调，逻辑逐行相同 | 完全同构 |
| `SS_KEY` | `doubao-mgmt-key`（其余插件为各自 id 前缀） | 符合模板 |
| `showAuth` / `saveKey` | 与 `workbuddy` 一致（copilot 无手动输入形态） | 同构 |
| `api()` 的 401/403 处理 | 比既有插件更简（无失败计数与 IP 封禁退避），因豆包面板无高频轮询 | 有意简化，未引入新范式 |

## 未纳入本轮的风格事项

- 未引入 `workbuddy` 的「连续失败计数 + 60s 退避」机制：该机制服务于其 2s 一次的后台轮询，豆包面板无此场景，按最小实现原则不预置。
- `doubao/panel.html` 现 327 行，未触发 200 行拆分约定（该约定约束 Go 生产文件，面板 HTML 不适用；同仓库 `workbuddy/panel.html` 逾千行亦未拆分）。
