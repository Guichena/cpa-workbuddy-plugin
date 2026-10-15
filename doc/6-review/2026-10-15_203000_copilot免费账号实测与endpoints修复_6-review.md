# copilot-provider 插件 6-review 风格回归（T1-4 实测 + GAP-002 修复）

- 审查对象：`copilot/` 插件 T1-4 实测后的代码改动（`endpoints.api` 消费链路）
- 审查时间：2026-10-15
- 变更文件：`copilot/authfile.go`、`copilot/token.go`、`copilot/upstream.go`、`copilot/oauth.go`、`copilot/main_test.go`
- 结论：**STYLE: PASS**

## 一、检查范围与依据

按 `code-style-consistency-rules` 的静态 Owner 路由（`route_review_pipeline`，repository_root 指向 skill 真源目录）推导检查步骤，逐项执行：

| 步骤 | Owner | 结论 | 证据 |
|---|---|---|---|
| STYLE-01 静态格式与编码一致性 | code-style-consistency-rules | PASS | 5 个改动文件全部 UTF-8 无 BOM、CRLF=0、行尾空白行=0；`gofmt -l`（LF 归一后）输出为空 |
| STYLE-02 命名与符号引用一致 | naming-rules | PASS | 新增字段 `APIEndpoint` / JSON 键 `api_endpoint` 与既有 `CopilotToken`/`copilot_token` 命名形态同构；新函数无引入，仅签名扩展 |
| STYLE-03 注释分层与定义位置 | comment-rules | PASS | 修复 3 处注释漂移：`saveGitHubAccount` 的 `[参数]` 补 `apiEndpoint`；`handleRefreshAuth`、`settleDeviceLogin`、`ensureCopilotToken` 的「改动原因」由「新增」更新为本轮真实改动；新增字段带行尾中文注释 |
| STYLE-04 函数签名与参数结构 | code-quality-rules | PASS | `saveGitHubAccount` 7 参（含 3 个 string 合并声明），与 `zcode/authfile.go` 的 `saveZaiAccount` 7 参形态同构；未新增参数结构体，符合「跟随局部既有粒度」 |
| STYLE-08 语言与框架特异写法 | golang-patterns | PASS | 无 `iota`；本轮未新增函数内 `var (...)` 分组；`strings.TrimRight`/`TrimSpace` 仅用于外部端点归一化（上游返回值属系统边界） |
| STYLE-09 测试资产与编码前契约 | test-program-rules | PASS | 回归用例落在既有 `copilot/main_test.go`（Go 包内测试约定），未新增文件、未新建目录；无测试资产漂移 |

## 二、跟随依据

本轮是**在既有新插件上做增量修复**，跟随对象仍为 `zcode/` 局部基线：

- 新增字段采用与 `CopilotToken`/`ExpiresAt` 完全一致的行尾注释风格与 `omitempty` 约定。
- `resolveCopilotBaseURL` 的优先级分支保持原有「配置优先」写法，仅插入 `endpoints.api` 一层，未重构函数结构。
- `exchangeCopilotToken` 由返回 `(string, time.Time, error)` 改为返回 `(copilotTokenResult, error)`，与 `fetchCopilotToken` 的返回类型统一，减少调用点解构噪音。
- 端点注入变量 `copilotTokenURLForTest` 沿用 zcode 的 `xxxURLForTest` 命名与「非空时覆盖」写法（zcode 同类注入点 13 处）。

## 三、发现与处置

发现并修复的真实问题：

1. **`endpoints.api` 解析后被丢弃**（`AccountAPI` 字段无任何消费点，属死代码）。按需求文档 `GAP-002` 的「存在则以响应字段为准」要求修复：新增 `StoredAuth.APIEndpoint` 持久化，`resolveCopilotBaseURL` 优先级调整为「配置 `base_url` > `endpoints.api` > 按账号类型推导」。
2. **3 处注释元信息漂移**：改动过的函数仍标注「改动原因：新增」。已全部更新为本轮真实改动原因（属 STYLE-03 范畴，已在本轮修复）。

**OCR 代码审查（Go 规则）发现并修复的安全边界问题**：`endpoints.api` 是外部 API 返回值，而该 URL 会承载 `Authorization: Bearer <copilot token>` 发往上游。原实现直接采用该值作为请求目标，一旦上游响应被篡改或异常，凭据可能被带到任意主机。已按 Go 规则的「安全敏感边界 → SSRF / 凭据泄漏」条目修复：新增 `isCopilotEndpoint` 守卫，只接受 `https` 且主机为 `githubcopilot.com` 或其子域，不匹配时回退按账号类型推导；补 4 个负例回归（`evil.example.com`、`http://` 降级、`api.githubcopilot.com.evil.com` 后缀混淆、非法 URL），并经变异验证确认守卫生效。

无风格跳变、无临时文件残留、无路径漂移。

## 四、负向边界声明

本审查只覆盖风格、格式、命名、注释、落点与测试资产归位；**不判断**业务逻辑正确性、需求覆盖度与发布放行。`GAP-003`（GitHub 风控边界）仍未关闭，宿主内端到端加载验证属 CYCLE-2 / CYCLE-4 范围，本审查不构成其通过结论。
