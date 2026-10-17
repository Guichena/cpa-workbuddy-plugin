# doubao-provider

豆包（Doubao）Provider 插件：把豆包网页端账号能力封装为 CPA 的 OpenAI 兼容 Provider。

## 能力

- 文本对话、深度思考（`reasoning_content`）
- 图像生成、视频生成
- 多账号池与阶梯冷却故障转移
- 管理面板：登录态导入、账号管理、模型展示、状态诊断

## 认证方式

**复用用户浏览器登录态**：用户在自有浏览器登录豆包后，从浏览器导出会话 Cookie 并导入本插件。插件**不实现登录流程**（不扫码、不手机号、不内嵌浏览器）。

导出步骤：

1. 在浏览器打开并登录 `https://www.doubao.com`
2. 打开开发者工具 → Application → Cookies → `https://www.doubao.com`
3. 复制完整 Cookie header（至少需含 `flow_cur_user_sec_id` 与 `sessionid`）
4. 粘贴到本插件面板完成导入

## 风险提示

- 本插件走豆包网页端协议（非火山方舟开放平台 API），协议可能随上游版本变化而失效
- 上游对请求频率与账号行为有风控策略，请合理控制调用频率
- 账号订阅状态可能影响生图/生视频等高级能力

## 配置项

| 键 | 类型 | 说明 |
|---|---|---|
| `timeout_seconds` | 整数 | 非流式上游请求超时（秒），默认 120 |
| `stream_timeout_seconds` | 整数 | 流式上游请求超时（秒），默认 600 |
| `model_cache_seconds` | 整数 | 模型目录缓存时长（秒），默认 300 |

## 构建与验证

本插件为 c-shared 动态库（`import "C"`）。本地无 CGO 环境时用 shim 验证：

```bash
python scripts/cgo-shim-build.py doubao
```
