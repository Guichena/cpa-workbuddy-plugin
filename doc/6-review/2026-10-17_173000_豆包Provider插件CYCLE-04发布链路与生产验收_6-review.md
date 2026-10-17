---
schema_version: 1
doc_type: style_regression
source_object_id: REQ-DOUBAO-PROVIDER-20261017
task_scope: CYCLE-04 发布链路与生产验收（TASK-009 ~ TASK-010）
status: PASS
updated_at: 2026-10-17 17:30:00
---

# 豆包 Provider 插件 CYCLE-04 发布链路与生产验收 6-review

结论：本轮为发布链路执行与生产验证，代码零改动；新增的 `registry.json` 条目、`release-assets/doubao-provider-0.1.0/` 产物目录与测试证据文档，写法与既有 9 个插件的发布记录一致，未引入局部风格跳变；**STYLE: PASS**。

- 来源对象：`REQ-DOUBAO-PROVIDER-20261017`（需求 v1.8）
- 对应实施文档：`doc/3-实施/2026-10-17_033614_豆包Provider插件立项需求_实施总览.md`（TASK-009 / TASK-010）
- 真实测试证据：`doc/5-tests/2026-10-17_172500_豆包Provider插件CYCLE-04发布链路与生产验收实测.md`
- 本轮范围：`registry.json`（+71/−0）、`release-assets/doubao-provider-0.1.0/`（8 文件）、`doc/5-tests/2026-10-17_172500_...实测.md`（新增）
- 检查步骤来源：`python scripts/static_owner_router.py --root "C:/Users/luode/.qoder/skills" --changed registry.json --changed release-assets/doubao-provider-0.1.0 --changed doc/5-tests/...实测.md`，命中 **STYLE-01 / 02 / 03 / 04 / 09** 五步

## 逐步检查结论

| 步骤 | 检查项 | 结论 | 证据 |
|---|---|---|---|
| STYLE-01 | UTF-8 无 BOM、LF、无尾随空白 | 通过 | `registry.json` 与测试文档均 `bom=False`、无尾随空白、以换行结尾；工作区 CRLF 为 Windows 检出形态，Git blob 内为 LF（718 行，与 `HEAD~1` 一致），未改变仓库行尾口径 |
| STYLE-02 | 命名与符号引用一致 | 通过 | 新增 registry 条目 `id=doubao-provider` 与插件目录名、`providerName` 常量、管理路由前缀 `/v0/management/plugins/doubao-provider/*` 完全一致；`name` 采用与 `Copilot Provider` 同构的 `Doubao Provider`；artifact 文件名遵循既有 `{id}_{version}_{goos}_{goarch}.zip` 模板 |
| STYLE-03 | 注释分层与定义位置 | 通过 | 本轮无 Go 代码改动，注释规则不适用；registry 为纯数据、测试文档为 Markdown，均无注释元信息要求 |
| STYLE-04 | 函数签名与参数结构 | 通过 | 本轮无函数签名改动；`publish-assets.py` / `download-release-assets.py` 以既有脚本原样调用，未修改 |
| STYLE-09 | 测试资产与编码前契约 | 通过 | 临时探针文件（`zz_probe_ratelimit_test.go`、`zz_probe_device_test.go`）取证后已删除，`git status doubao/` 为空；测试证据落 `doc/5-tests/` 且命名遵循 `YYYY-MM-DD_HHMMSS_主题实测.md` 既有模板；未向 `test/doubao/` 引入新资产 |

## 字段与格式对齐核对

| 核对项 | 既有 9 条口径 | 本轮 doubao 条目 | 结论 |
|---|---|---|---|
| 顶层结构 | `schema_version: 2` + `plugins: []` | 未改动 | 一致 |
| 条目字段顺序 | `id / name / description / author / version / repository / logo / homepage / license / tags / install` | 同序 | 一致 |
| `install` 结构 | `{type: "direct", artifacts: [...]}` | 同构 | 一致 |
| artifact 字段顺序 | `goos / goarch / url / sha256 / size` | 同序 | 一致 |
| 平台集合 | darwin(amd64,arm64) / freebsd(amd64) / linux(amd64,arm64) / windows(amd64,arm64) | 7 个完全相同 | 一致 |
| URL 模板 | `raw.githubusercontent.com/.../main/release-assets/<id>-<ver>/<file>` | 同模板 | 一致 |
| 既有条目 | — | 逐字段比对 9 条全部 `unchanged` | 未受影响 |

## 产物目录核对

| 核对项 | 结论 | 证据 |
|---|---|---|
| 目录命名 | 通过 | `release-assets/doubao-provider-0.1.0/`，与 registry 的 `id` + `version` 严格对应 |
| 文件集合 | 通过 | 7 个平台 zip + `checksums.txt`，与既有插件目录同构 |
| 暂存范围 | 通过 | `git diff --cached --name-only` 全部以 `release-assets/doubao-provider-0.1.0/` 开头，无并行改动混入 |

## 未纳入本轮的风格事项

- `release-assets/copilot-provider-0.1.8/`（并行会话 17:19 创建）与 `copilot-provider-0.1.8` registry 条目属其他会话在途工作，本轮未触碰、未清理、未评审。
- `prune-release-assets.py --dry-run` 列出的 4 个待删目录中，`copilot-provider-0.1.8` 属上述在途工作，本轮**跳过 prune**，避免删除并行会话尚未收口的产物。
