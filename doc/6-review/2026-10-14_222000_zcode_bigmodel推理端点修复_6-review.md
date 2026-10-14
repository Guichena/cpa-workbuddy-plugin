# zcode bigmodel 推理端点修复（0.1.27）6-review 风格回归

- 对应 Bug：`doc/4-bugs/2026-10-14_220000_zcode_bigmodel推理端点误用open_bigmodel致401鉴权失败根因诊断.md`
- 对应测试：`doc/5-tests/2026-10-14_221500_zcode_bigmodel推理端点修复测试.md`
- 回归时间：2026-10-14
- 范围：`zcode/upstream_target.go`、`zcode/upstream_target_test.go`、`zcode/VERSION`、`zcode/main.go`、`zcode/CHANGELOG.md`

## 检查项与结论

| 检查项 | 结论 | 证据 |
| --- | --- | --- |
| 命名一致性 | PASS | 删除 `bigmodelCodingPlanBaseURL` 后剩余常量 `zaiCodingPlanBaseURL` / `zaiAPIAuthropicBaseURL` 命名未变；无遗留孤儿命名 |
| 目录归位 | PASS | 改动全部落在 `zcode/`；文档落在 `doc/4-bugs/` 与 `doc/5-tests/` |
| 注释链（`comment-rules`） | PASS | `resolveUpstreamTargets` 函数头保留 `[参数]`/`[返回]`/`最近修改时间`，`改动原因` 更新为端点修正；常量注释同步删除 bigmodel 条目 |
| 日志 | PASS | 本轮未新增日志 |
| 错误处理 | PASS | 未引入吞异常或兜底特判；端点解析逻辑简化为单一 start-plan 路径 |
| 格式（gofmt） | PASS | `gofmt -l` 空（LF 归一后） |
| 生产代码测试污染 | PASS | `POLLUTION: PASS`（哨兵变异已恢复） |
| 最小改动 | PASS | 仅删 1 个常量 + 删 1 处分支 + 更新注释 + 调整 1 个测试断言；无顺手重构 |
| 向后兼容 | PASS | zai 账号端点不变；历史账号（无归属字段）端点不变；`provider_family` 字段与面板展示保留 |
| 排版与行尾 | PASS | Go 文件保持 CRLF；无整文件伪变更 |

## 具体风格判定

1. **删常量而非留空壳**：`bigmodelCodingPlanBaseURL` 唯一引用点被移除后直接删除常量，不留未使用符号或注释残留，符合仓库「清理本轮产生的残留」要求。
2. **注释承载实测结论**：函数头注释补充 CYCLE-3 对照实验结论（无效 JWT 401 / 有效 JWT 405-429）与 asar 逆向依据，符合「注释解释 WHY 而非 WHAT」。
3. **测试断言强化**：在原有「包含 zcode.z.ai」之外新增**反向断言**（不得含 `open.bigmodel.cn`），与 CYCLE-1 测试文档记录的「哨兵 1 首轮未 FAIL 暴露断言盲区」教训一致——字面量主机断言避免常量同值时退化为恒真。
4. **分支消除方式**：直接删除 family 分支而非保留 `if family == bigmodel { sameURL }` 的恒等分支，符合「简单优先、不留双轨分流」。
5. **改动原因行**：`最近修改时间：2026-10-14；改动原因：移除 bigmodel 专用端点，start-plan 两站共用 zcode.z.ai`，符合仓库「改动位点补齐闸门」。

## 结论

`STYLE: PASS`

无风格跳变、无越界改动、无污染。0.1.27 修复与既有 zcode 包风格保持一致。
