# 插件 max_tokens 兜底口径对齐与 zcode 规格表全扩 6-review 风格回归

- 编号：REVIEW-PLUGIN-MAXTOKENS-SPEC-ALIGN-20261013
- 关联来源：本轮 `workbuddy` / `workbuddy-ai` / `zcode` 三插件的兜底规格与规格表对齐改动
- 审查时间：2026-10-13 22:10（北京时间）
- 结论：**STYLE: PASS**

## 1. 审查范围

`workbuddy`、`workbuddy-ai`、`zcode` 三插件本次改动，共 6 个文件：

- `workbuddy/model_specs.go`（未收录模型兜底 `ReasoningMax` 8192 → 32768，补函数头元信息与改动位点注释）
- `workbuddy-ai/model_specs.go`（同源镜像，改动一致）
- `workbuddy/max_tokens_test.go`（新增未收录模型兜底断言）
- `workbuddy-ai/max_tokens_test.go`（同源镜像）
- `zcode/model_specs.go`（规格表全扩为通用家族、匹配逻辑改为最长键优先、补 `vendor/` 前缀剥离、补 GLM 新世代家族规则、兜底 `ReasoningMax` 对齐 32768）
- `zcode/max_tokens_test.go`（新增 `TestResolveOfficialModelSpec_Extended` 覆盖 8 个用例）

## 2. 检查项与结论

| 维度 | 检查 | 结论 |
| --- | --- | --- |
| 格式化 | 6 文件均无 BOM、无尾随空白；`zcode` 两文件 `gofmt -l` 通过；`workbuddy` 两文件的 map 对齐差异经 `git show HEAD` 复核确认为历史遗留（HEAD 版本同样不过 gofmt），按手术式改动未重排 | PASS |
| 编码 | 全部 UTF-8 可解码，无乱码；行尾与仓库 `* text=auto` 归一口径一致 | PASS |
| 命名 | `bestKey` / `bestSpec` 与同仓先例 `traework/browserlogin.go:238` 的 `bestKey` / `best` 选优命名一致；`resolveOfficialModelSpec` / `computeAdaptiveMaxTokens` 未更名，保持跨插件同构 | PASS |
| 注释语言 | 新增注释全部简体中文，符合仓库核心规范 | PASS |
| 注释元信息 | 改动函数头补齐 `[参数]` / `[返回]` / 最近修改时间与改动原因；`lookupOfficialModelSpec`（workbuddy 侧）与 `resolveOfficialModelSpec`、`computeAdaptiveMaxTokens`、`relaxMaxTokensInPlace`（zcode 侧）均已覆盖 | PASS |
| 结构 | `zcode/model_specs.go` 仍为单一职责文件，规格表 + 匹配 + 自适应计算三段式布局与其余五插件同源保持 | PASS |
| 局部一致性 | `workbuddy` 与 `workbuddy-ai` 两文件逐字节同源；`zcode` 沿用了同族插件的 `ModelOfficialSpec` 结构与 `computeAdaptiveMaxTokens` 算法形态，未引入新抽象 | PASS |
| 死代码 | 无新增未使用 import；`zcode` 保留 `fmt` / `strings` 均为实际使用；build 与 vet 零 warning | PASS |
| 排版 | 无多余空行与调试残留，缩进为 tab，与同目录既有文件一致 | PASS |
| 位置归位 | 全部改动落在各自插件根目录的既有文件内，无新增文件、无跨目录漂移 | PASS |

## 3. 本仓库既有风格遵守

- 规格表字典字面量对齐沿用同族插件既有的 `{ContextWindow: ..., MaxOutputTokens: ..., ReasoningMax: ...}` 单行写法，未改结构。
- 兜底分支采用与 `qoder-ai` / `qoderwork` / `traework` 相同的「常规保守、推理放宽」口径，消除三方不一致。
- `zcode` 匹配逻辑由 `strings.Contains` 收紧为双向前缀取最长键，修正短键遮蔽长变体问题；该写法与本仓「精确命中优先、再前缀模糊」的既有匹配思路一致。
- 新增测试用例命名与断言风格沿用各插件 `max_tokens_test.go` 既有 table-driven 结构，未引入新的测试框架或写法。

## 4. 发现与处理

- 未发现需要修复的风格问题。
- 说明：`workbuddy/model_specs.go`、`workbuddy-ai/model_specs.go` 的 map 键对齐在 HEAD 版本即不满足 `gofmt`，属历史遗留；本轮遵守手术式改动原则，仅改动兜底值与注释，未顺带重排整段 map，故保留该历史差异。

## 5. 结论

STYLE: PASS
