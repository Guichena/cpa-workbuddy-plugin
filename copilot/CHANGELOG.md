# Changelog

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
