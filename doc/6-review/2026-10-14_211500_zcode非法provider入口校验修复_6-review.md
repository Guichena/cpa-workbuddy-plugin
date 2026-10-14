# zcode 非法 provider 入口校验修复（0.1.26）6-review 风格回归

- 对应 Bug：`doc/4-bugs/2026-10-14_210000_zcode非法provider被归一化提前改写致HTTP入口校验失效静默回退根因诊断.md`
- 对应测试：`doc/5-tests/2026-10-14_211000_zcode非法provider入口校验修复测试.md`
- 回归时间：2026-10-14
- 范围：`zcode/oauth.go`、`zcode/management.go`、`zcode/provider_family_test.go`、`zcode/VERSION`、`zcode/main.go`、`zcode/CHANGELOG.md`

## 检查项与结论

| 检查项 | 结论 | 证据 |
| --- | --- | --- |
| 命名一致性 | PASS | 新函数 `resolveOAuthProvider` 与既有 `normalizeProviderFamily` / `isValidOAuthProvider` 同前缀 `provider` 语义族；动词 `resolve` 准确表达「校验 + 归一化」 |
| 目录归位 | PASS | 改动全部落在 `zcode/`；文档落在 `doc/4-bugs/` 与 `doc/5-tests/` |
| 注释链（`comment-rules`） | PASS | 新增 `resolveOAuthProvider` 含 `[参数]`/`[返回]`/`最近修改时间`；被改动的 `startAccountOAuth` / `providerFromMetadata` / `handleActivateStart` 注释同步更新并补「改动原因」 |
| 日志 | PASS | 本轮未新增日志；错误经 HTTP 响应体返回，与既有 `makeHTTPResponse` 错误路径一致 |
| 错误处理 | PASS | 非法 provider 返回 `400` + 明确错误文案；空值仍按默认 `zai`（向后兼容）；未引入吞异常或兜底特判 |
| 格式（gofmt / 前端语法） | PASS | `gofmt -l` 空（LF 归一后）；未触碰 `panel.html` |
| 生产代码测试污染 | PASS | `POLLUTION: PASS`（生产代码无测试专用符号；哨兵变异已恢复） |
| 最小改动 | PASS | 仅新增 1 个纯函数 + 3 处调用点调整 + 测试用例；无顺手重构、无格式批量调整 |
| 向后兼容 | PASS | `normalizeProviderFamily` 语义未改，存量账号读取路径不变；空 provider 行为不变 |
| 排版与行尾 | PASS | Go 文件保持 CRLF；无整文件伪变更 |

## 具体风格判定

1. **函数抽取位置**：`resolveOAuthProvider` 紧邻 `isValidOAuthProvider` 放置，两者同属「provider 取值处理」语义块，符合既有「同语义函数相邻」的排布。
2. **错误信息复用**：沿用 CYCLE-1 既有文案 `不支持的 OAuth provider: %q（仅支持 zai / bigmodel）`，未另造措辞，保证错误契约稳定。
3. **调用点改法**：`handleActivateStart` 用 `errProvider` 局部变量名（避免与后续 `err` 混淆），与函数内既有 `errNew` / `errParse` 的「场景化错误变量命名」一致。
4. **注释改动原因**：三处被改函数均在 `最近修改时间` 行补「改动原因」，符合仓库「改动位点补齐闸门」要求。
5. **测试组织**：新增用例置于 `provider_family_test.go` 既有 `TestProviderFromMetadata` 之前，与 provider 相关用例聚簇；断言风格沿用 `t.Errorf` / `t.Fatalf` 混用的既有写法。
6. **HTTP 层断言**：复用 `unwrapMgmtResp` 与 `pluginapi.ManagementRequest`，与 `management_test.go` 既有入口测试写法一致，未引入新测试脚手架。

## 结论

`STYLE: PASS`

无风格跳变、无越界改动、无污染。0.1.26 修复与既有 zcode 包风格保持一致。
