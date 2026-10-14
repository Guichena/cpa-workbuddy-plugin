# zcode-provider bigmodel 中国区授权（CYCLE-2）测试记录

- 对应需求：`doc/2-需求/2026-10-14_153951_zcode支持bigmodel中国区授权需求.md`
- 对应实施：`doc/3-实施/2026-10-14_zcode支持bigmodel中国区授权实施总览.md`
- 前序测试：`doc/5-tests/2026-10-14_170000_zcode_bigmodel中国区授权CYCLE1测试.md`
- 测试时间：2026-10-14
- 测试范围：CYCLE-2（TASK-005），面板 provider 选择交互与账号卡片归属标识

## 一、测试环境

- 构建入口：`python scripts/cgo-shim-build.py zcode`（CGO_ENABLED=0 shim，覆盖 build + vet + test）
- 前端回归入口：`node test/zcode/panel_quota_render_repro.mjs`（Node vm + DOM 桩，真实执行 panel.html 内联脚本）
- 面板语法入口：`node -e` 对两个 `<script>` 块执行 `new Function()` 编译校验
- Windows 本地 Go：go1.27.0

## 二、测试结果

| 项 | 命令 | 结果 |
| --- | --- | --- |
| 构建 | `go build ./...` | OK |
| 静态检查 | `go vet ./...` | OK |
| 单元测试 | `go test ./...` | OK（zcode 2.852s / internal/mimic 0.480s） |
| 面板回归 | `node test/zcode/panel_quota_render_repro.mjs` | PASS（全部断言通过） |
| 面板语法 | `new Function()` × 2 script 块 | OK |
| 格式 | `gofmt -l`（LF 归一后） | 空（合规） |
| 测试污染 | 生产代码 grep 测试专用符号 | 空（POLLUTION: PASS） |

## 三、本轮改动与新增用例

### 后端数据源（`zcode/management.go`）

`accountSummary` 新增 `ProviderFamily string \`json:"provider_family,omitempty"\`` 字段，`handleListAccounts` 以 `providerFamilyOf(acc)` 填充。面板据此渲染归属徽标；历史账号缺省 `zai`。

### 面板交互（`zcode/panel.html`）

| 落点 | 改动 |
| --- | --- |
| 工具栏 | 新增 `#oauthProvider` 下拉（国际站 `zai` / 中国区 `bigmodel`），默认 `zai` |
| `openOAuthBridge()` | 读取下拉值，`/activate-start` 请求体由 `{}` 改为 `{"provider": 选中值}` |
| `providerBadge(a)` | 新增归属徽标渲染函数；中国区用强调色区分，历史账号按国际站 |
| `card(a)` | 卡片标题区插入 `providerBadge(a)`；`data-search` 纳入 `provider_family` 便于搜索 |
| 授权弹窗文案 | 按所选站点动态显示 `chat.z.ai（国际站）` / `bigmodel.cn（中国区）` |

### 新增 / 扩展用例

| 用例 | 断言 |
| --- | --- |
| `TestHandleListAccounts_ExposesProviderFamily`（`provider_family_test.go`） | 面板数据源透出归属：bigmodel 账号输出 `bigmodel`，zai 账号输出 `zai` |
| 阶段 6「授权站点归属徽标」（`panel_quota_render_repro.mjs`） | 中国区卡片含 `badge provider bigmodel` 且标注「中国区」；缺省账号标注「国际站」且不含 `bigmodel` |
| 阶段 7「授权 provider 透传」（`panel_quota_render_repro.mjs`） | 下拉选中 `bigmodel` 时请求体 provider 为 `bigmodel`；空选值回落 `zai` |

### 顺带修正

回归脚本阶段 1 的「无额度卡片」断言长期失配：断言写的是 `加载中…`，而 `quotaHTML` 无额度分支自 `9655225` 起已改为「未领取 · 需在 ZCode 客户端领取 Start Plan」。本轮修正断言为 `未领取`，使该回归入口恢复可用（属既有缺陷修复，非本轮功能引入）。

## 四、判别力反证（哨兵变异）

| 变异 | 注入内容 | 结果 |
| --- | --- | --- |
| 哨兵 1 | `panel.html` 中 `JSON.stringify({provider: provider})` 改为固定字符串 | 阶段 7 两条断言精确 FAIL |
| 哨兵 2 | `providerBadge` 判定条件 `fam==="bigmodel"` 改为不匹配值 | 阶段 6 三条断言精确 FAIL |
| 哨兵 3 | `management.go` 中 `ProviderFamily: providerFamilyOf(acc)` 改为固定字符串 | `TestHandleListAccounts_ExposesProviderFamily` 精确 FAIL |

三次变异均在恢复后重跑全绿，证明新增断言真实进编译且具备判别力。

## 五、AC 覆盖情况

| AC | 状态 | 说明 |
| --- | --- | --- |
| AC-001 面板可选 bigmodel 发起授权 | **达成** | 下拉选择 + 请求体透传 + 授权地址断言（阶段 7） |
| AC-002 bigmodel 授权端到端可用 | 未覆盖 | 需真实 bigmodel 账号（GAP-004/005），属 CYCLE-3 |
| AC-003 zai 既有行为零回归 | **达成** | 现有测试全绿 + 空选值回落 `zai` 用例 |
| AC-004 卡片区分归属 | **达成** | 归属徽标 + 后端字段透出（阶段 6 + Go 用例） |
| AC-005 cgo-shim 全绿 | **达成** | 见第二节 |
| AC-006 未知 provider 明确报错 | **达成** | CYCLE-1 已覆盖，本轮无回归 |

## 六、未覆盖与说明

- 面板 UI 为静态渲染断言（Node vm 执行真实内联脚本），**未做真实浏览器点击验证**；本轮改动为纯前端渲染逻辑与请求载荷，风险集中在字符串拼接，静态断言已覆盖关键分支。
- `AC-002` 端到端仍需真实中国区账号，属 CYCLE-3。
- 未触碰生产配置、未执行 `plugin-store install`、未重启容器（遵守实施计划第 8 章最大推进边界）。
