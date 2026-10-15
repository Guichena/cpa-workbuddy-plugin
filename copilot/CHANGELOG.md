# Changelog

## 0.1.3

修复模型自动获取不正确（面板与宿主长期只显示 5 个兜底模型，上游实际可用 25 个），并为面板补齐「测试模型」功能。

**Fix**

- `models.go`：`authModelsForIndex` 优先级反转为「上游动态发现 > 账号落盘缓存 > 配置 models > 静态兜底」。原先账号文件的 `models` 字段非空即返回，导致 `fetchUpstreamModels` 永不执行、动态发现被完全旁路。
- `models.go`：`fetchUpstreamModels` 按上游契约过滤——只保留 `capabilities.type=chat` 且 `policy.state != disabled` 的条目，并剔除 `copilot-search-*` / `exec-agent-*` / `trajectory-compaction` 等内部子代理工具项（vendor=Fireworks 且 family 命中前缀）。实测生产 57 条 → 25 条可用对话模型。注意 `model_picker_enabled` 全为 false，不可用作判据。
- `models.go`：`allConfiguredModels` 改为动态优先，不再无条件并入兜底清单，避免上游已下线模型污染宿主全局模型目录与跨 provider 路由选模。
- `models.go`：落地此前形同虚设的 `dynamic_models` 配置开关（原字段被解析但全仓无读取点），显式 `false` 时跳过上游请求。
- `authfile.go`：`saveAccount` / `saveTokenAccount` 落盘不再写入兜底清单，避免旧清单遮蔽动态发现。
- `oauth.go`：`handleRefreshAuth` 在刷新令牌时顺带刷新模型清单，使存量账号的失真 `models` 字段能被自动纠正。
- `management.go`：面板 `/accounts` 的模型列表改用 `authModelsForIndex`（动态优先），不再直读账号落盘字段。

**Feat**

- 新增面板「测试模型」功能：账号卡片「测试」按钮 → 模型选择弹窗 → 点选模型发起一次真实推理并就地展示结果。后端新增 `POST /test-active`（`active_ping.go`）。
- 探活契约对齐既有插件成熟策略：流式请求 `stream=true, max_tokens=1`，收到首个有效输出帧即返回（TTFT 级），后台异步关流；拦截 HTTP 200 下的 `event: error` 与 JSON `error` 字段，不盲判首包成功。

**Test**

- `models_discovery_test.go` 新增 5 例：真实对话模型保留（含 policy 缺省）、不可用条目剔除（embeddings/completion/disabled/三类内部工具项）、内部工具项判定必须同时满足 vendor 条件、流式错误帧拦截（含正常帧与 `[DONE]` 不误报）、错误文本截断边界。
- 哨兵反证 3 组：关闭 policy 过滤 → `disabled policy` 用例精确 FAIL；关闭内部工具项过滤 → 3 个工具项用例精确 FAIL；关闭 `event: error` 分支 → 错误帧用例精确 FAIL；恢复后全绿。
- 真实 `.so` ABI 探针 5/5 PASS（`test/copilot/models_and_test_probe.py`）：注册表含 `/test-active`、`model.for_auth` 与 `model.static` 出口非空、`POST /test-active` 入参校验、`GET /models` 可达。
- 生产真实数据验证：上游 `/models` 57 条经过滤判据得 25 条可用对话模型；真实凭据流式探活 `status=200`、首包 1313ms、非错误帧。

**涉及文件**：`copilot/`（`models.go`、`authfile.go`、`management.go`、`oauth.go`、`active_ping.go`、`models_discovery_test.go`、`panel.html`、`VERSION`、`main.go`、`CHANGELOG.md`、`test/copilot/models_and_test_probe.py`）。

## 0.1.2

修复插件调度器越界返回自有账号 ID 导致宿主判 `unknown auth id` 的问题：`handleSchedulerPick` 原先忽略宿主下发的候选集，直接返回自有账号池的 `AuthID`（`copilot-<hash8>`，不带 `.json`），而宿主候选 ID 带 `.json` 后缀，逐字校验失败后回落内建选择器，插件调度意图被完全忽略（3 小时窗口累计 1165 条告警）。现改为只在宿主候选集内取号并原样返回候选 ID。

**Fix**

- `scheduler.go`：`handleSchedulerPick` 改为按 `req.Candidates` 过滤（本 provider、未禁用、不在冷却期），在候选集内轮转取号并原样返回 `candidate.ID`；非本 provider 候选或无可用候选时返回 `Handled:false` 交由宿主内建调度器处理。新增 `candidateDisabled` 对齐宿主候选禁用语义（`Status=disabled` 与 `Metadata.disabled`）。

**Test**

- `scheduler_test.go` 新增 6 例：候选 ID 逐字返回（回归判据）、外部 provider 让位、禁用跳过与全禁用让位、冷却跳过与全冷却让位、空候选让位、候选集内轮转。
- 真实 `.so` ABI 探针 5/5 PASS（WSL gcc 构建 + ctypes 宿主桥驱动完整生产路径，覆盖单测无法触达的 ABI 边界与 JSON 字段名契约）。
- 哨兵反证：注入 `strings.TrimSuffix(candidates[idx].ID, ".json")` 后 4 例精确 FAIL，恢复后全绿。

**涉及文件**：`copilot/`（`scheduler.go`、`scheduler_test.go`、`VERSION`、`main.go`）。

## 0.1.1

修复宿主管理页「登录」弹窗不显示设备码的问题：宿主 `ServePluginAuthURL` 只回传 `{status, url, state}`，插件返回的 `Metadata.user_code` 被写进宿主 session store 而不回传前端，导致用户在弹窗里看不到设备码、无法完成授权。现将登录启动返回的 `URL` 指向插件自有 resource 前缀的桥接页，宿主弹窗打开的即该页，页面内直接展示设备码与授权入口。

**Fix**

- `oauth.go`：`handleStartLogin` 返回的 `URL` 由 GitHub 授权地址改为桥接页相对路径 `/v0/resource/plugins/copilot-provider/login-bridge?state=<state>`；`Metadata` 新增 `bridge_url`，`prompt` 改为指引用户在弹窗页面内完成授权。
- `management.go`：`Resources` 注册 `/login-bridge`（无 `Menu`，不进管理菜单）；`handleManagement` 在 resource 前缀下分发该路径到桥接页。

**Feat**

- `bridge.go`：新增设备码桥接页渲染（`loginBridgeURL` / `handleLoginBridge` / `loginBridgePage` / `htmlEscape`）。页面展示大号可选中设备码、三步授权指引与 GitHub 授权页入口；仅做展示，不驱动轮询、不消费登录会话，授权结果轮询与凭据落盘仍由宿主 `get-auth-status` → `PollLogin` 链路负责（桥接页若抢先调用 `pollDeviceLogin`，会在成功时消费掉会话导致宿主拿不到凭据）。

**Test**

- `bridge_test.go` 新增 3 例：桥接页渲染设备码与授权地址且 HTML 完整未截断、缺 state / 未知 state / 超时 state 的守卫与超时会话清理、resource 路由分发到桥接页。
- `main_test.go` 的 `TestDeviceCodeFlowStartAndPoll` 补充断言：启动响应的 `URL` 必须是桥接页相对路径前缀，且 `Metadata.bridge_url` 与之一致。
- 单测总数 30 例（`main_test.go` 17 + `management_test.go` 10 + `bridge_test.go` 3）。

**涉及文件**：`copilot/`（`bridge.go`、`bridge_test.go`、`oauth.go`、`management.go`、`main.go`、`VERSION`、`main_test.go`）。

## 0.1.0

首发版本：把 GitHub Copilot 订阅额度封装为 CPA 原生 Provider，覆盖设备码登录、令牌保活、模型发现、多账号池与管理面板。

**Feat**

- 新增 `copilot-provider` 插件骨架：C ABI 入口、注册元数据与能力声明（model provider / auth provider / executor / scheduler / management）。
- 实现 GitHub OAuth Device Flow 登录：设备码申请、授权轮询、账号落盘（`copilot-<hash8>.json`）。
- 实现 Copilot 内部令牌交换与保活：`GET /copilot_internal/v2/token`，按 `refresh_in` / `expires_at` 的 80% 提前重取。
- 实现客户端指纹注入（`upstream.go`）与上游主机名按账号类型推导。
- 实现模型发现（`/models` + 5 分钟缓存）、`filterExcludedModels` 排除治理与模型别名解析。
- 实现非流式 / 流式转发执行器，流式遇 `[DONE]` 立即终止。
- 实现多账号调度、故障转移（429/5xx 换号重试 + 15 秒冷却）与配额查询（`/copilot_internal/user`）。
- 实现管理面板（账号卡片、配额进度条、设备码登录、Token 导入、启停与删除）。
- 持久化上游 `endpoints.api` 权威推理端点，端点解析优先级为配置 `base_url` > 账号端点 > 账号类型推导（含 `*.githubcopilot.com` 域校验）。
- 额度快照补 5 分钟 TTL 过期判定，账号删除时同步失效额度缓存。

**Test**

- 单测 27 例（`main_test.go` 17 + `management_test.go` 10），含设备码全链路桩测试、端点优先级与负例校验、调度轮询、额度 TTL 与删除清缓存。
- 面板 DOM 回归脚本 `test/copilot/panel_render_repro.mjs`；宿主 ABI 桥探针 `test/copilot/host_bridge_probe.py`。

**涉及文件**：`copilot/`（`main.go`、`authfile.go`、`token.go`、`oauth.go`、`upstream.go`、`quota.go`、`management.go`、`scheduler.go`、`executor.go`、`stream.go`、`models.go`、`panel.go`、`panel.html`、`util.go`、`failover.go`、`VERSION`、`go.mod`、`main_test.go`、`management_test.go`）、`assets/icons/Copilot.png`、`registry.json`、`.github/workflows/build.yml`、`README.md`。
