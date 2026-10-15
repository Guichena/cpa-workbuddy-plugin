# GitHub Copilot Provider (CLIProxyAPI Plugin)

`copilot-provider` 是 CLIProxyAPI (CPA) 的 Go 原生动态库插件，把 GitHub Copilot 的订阅额度封装为 OpenAI 兼容 Provider，并全面对齐 `zcode-provider` 的多账号运维管理能力。

## 核心特性

1. **GitHub OAuth 设备码登录**：复刻 GitHub 官方 Device Flow（`client_id` 与 `scope` 为 VS Code Copilot 扩展使用的公开常量），面板内展示设备码与授权地址，插件自动轮询绑定账号，无需手工复制回调。
2. **Copilot 令牌自动保活**：用 GitHub access token 换取 Copilot 内部令牌，按上游 `refresh_in` / `expires_at` 的 80% 提前重取，失败指数退避，避免推理请求因令牌过期中断。
3. **客户端指纹注入**：复刻 VS Code Copilot Chat 的请求头（`Copilot-Integration-Id`、`Editor-Version`、`Editor-Plugin-Version`、`Openai-Intent`、`X-Initiator` 等），指纹取值集中在 `upstream.go`，便于随上游版本单点更新。
4. **多账号池与故障转移**：账号轮询调度、429/5xx 换号重试（最多 3 次）、15 秒阶梯冷却、测试失败硬隔离。
5. **动态模型发现**：实时拉取上游 `/models` 并带 5 分钟缓存，支持宿主 `oauth-model-alias` 与 `oauth-excluded-models` 治理。
6. **流式与非流式转发**：直连 `api.githubcopilot.com`，原生 SSE 逐行实时推送；遇 `[DONE]` 立即退出并关闭连接，杜绝 Keep-Alive 空闲挂起。
7. **专属管理控制台**：挂载于 `/v0/resource/plugins/copilot-provider/panel`，支持深浅主题、账号卡片、配额进度条、模型查看与账号启停。

## 配置示例

```yaml
plugins:
  enabled: true
  configs:
    copilot-provider:
      enabled: true
      # base_url 留空时按账号类型自动推导：
      # individual -> https://api.githubcopilot.com
      # business / enterprise -> https://api.<type>.githubcopilot.com
      dynamic_models: true
      model_cache_seconds: 300
      timeout_seconds: 300
      account_type: "individual"
      editor_version: "vscode/1.104.3"
      plugin_version: "copilot-chat/0.26.7"
      user_agent: "GitHubCopilotChat/0.26.7"
      models:
        - "gpt-4.1"
        - "gpt-4o"
        - "claude-sonnet-4"
```

## 本地验证

```bash
python scripts/cgo-shim-build.py copilot
node test/copilot/panel_render_repro.mjs
```

## 风险提示

- 本插件为社区非官方实现，与 GitHub、Microsoft 无隶属或授权关系；使用前请自行确认符合 GitHub 服务条款与 Copilot 订阅限制。
- GitHub 对自动化请求设有 abuse-detection；请勿以高频批量方式调用，否则可能导致 Copilot 访问被临时封停。
- `client_id` 与上游端点为逆向所得的公开常量，可能随上游变更而失效；如失效，更新 `oauth.go` / `token.go` 中的常量即可。
