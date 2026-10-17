# TASK-000：Coze 授权登录 `redirect_uri` 可行性验证

- 验证时间：2026-10-17 23:30（GMT+8）
- 验证对象：`https://www.coze.cn/sign/oauth` 与 `https://www.coze.cn/auth/callback`
- 桌面端版本：`D:\Coze\versions\1.1.45`（appId 1002230）
- 验证方式：curl 响应体比对 + 隔离无痕 Chrome（CDP，独立 profile，无登录态）
- 关联需求：`doc/3-实施/2026-10-17_230000_CozeProvider插件立项_实施总览.md` TASK-000

## 结论

**`/sign/oauth` 不接受第三方 `redirect_uri`，插件不能直接复用桌面端的「自定义协议回调」链路。**

真正的 OAuth 回调被**硬编码**为 `https://www.coze.cn/auth/callback`；用户传入的 `redirect` 参数只被塞进 `state.navigatePath`，作为**登录成功后的站内跳转目标**，且仅当目标是 `coze.cn` 站内地址时才被采用。

## 证据

### 证据 1：`/sign/oauth` 会把 `redirect` 降级为 `state.navigatePath`

请求：

```text
GET https://www.coze.cn/sign/oauth?platform=aweme_v2&page_from=coze_pro_sign_in
    &redirect=https%3A%2F%2Fexample.com%2Fcb
    &state={"type":"login","platform":"aweme_v2"}
```

浏览器最终落在：

```text
https://open.douyin.com/platform/oauth/pc/auth
  ?client_key=awfc49ap1m97hhkt
  &client_id=awfc49ap1m97hhkt
  &redirect_uri=https%3A%2F%2Fwww.coze.cn%2Fauth%2Fcallback   ← 被硬编码
  &response_type=code
  &scope=user_info
  &state={"platform":"aweme_v2","navigatePath":"https://example.com/cb","type":"login"}
```

**判据**：`redirect_uri` 恒为 `www.coze.cn/auth/callback`，与请求参数无关；第三方地址只出现在 `state.navigatePath` 里。

### 证据 2：`/auth/callback` 只对站内 `navigatePath` 生效

隔离无痕 Chrome（无登录态）三组对照：

| 场景 | `state.navigatePath` | 最终落点 | 结论 |
|---|---|---|---|
| A | `https://www.coze.cn/home?login_from=space_desktop_landing` | 先到 `/home?...`，再因未登录被引导到火山引擎登录 | **站内地址被采用** |
| B | `https://example.org/evil` | `https://www.coze.cn/overview` | **站外地址被丢弃** |
| C | `https://example.com/cb?probe=1` | `https://www.coze.cn/overview` | **站外地址被丢弃** |

伪造 `x_tt_token` / `code` 时，`/auth/callback` 均回落到 `/overview`，说明失败兜底存在且不暴露 token。

### 证据 3：协议名与注册方式

- 注册表：`HKCU\Software\Classes\coze-desktop\shell\open\command` → `"D:\Coze\Coze.exe" "%1"`
- 主进程：`buildAuthUrlWithRedirect` 把 `redirect_uri` 设为 `${APP_PROTOCOL}://auth/callback`，`APP_PROTOCOL` 由 `resolveAppProtocol()` 解析
- 即：桌面端能收到 deep link，是因为 `coze-desktop` 协议由**安装器注册到系统**，不是服务端白名单

## 对实施计划的影响

**原方案（CYCLE-00 直接做 HTTP 回调桥接页）不成立**，因为扣子从不把 `x_tt_token` 发到第三方地址。

### 可行的替代路径

| 方案 | 机制 | 可行性 | 用户操作 |
|---|---|---|---|
| **D1：本地回环 + 系统浏览器** | 插件在本机监听 `http://127.0.0.1:<port>/callback`，把该地址作为 `redirect` 传给 `/sign/oauth` | **不可行**——站外地址被丢弃（证据 2） | — |
| **D2：站内回调 + 结果轮询** | 让浏览器完成授权后停在 `coze.cn/home`，插件通过**另一个通道**取登录态 | 需验证：是否有可轮询的「登录完成」信号 | 无感 |
| **D3：桌面端 deep link 复用** | 插件注册自己的协议（如 `cpa-coze://`），但扣子只跳 `coze-desktop://` | **不可行**——协议名固定 | — |
| **D4：授权码中转页** | 用户完成授权后，从扣子页面复制一次性凭据粘贴到插件面板 | 可行，但体验差 | 手动复制 |
| **D5：官方 OAuth 授权码（编程 API）** | 走 `/api/permission/oauth2/authorize` + `/token`，标准 OAuth2 | 可行，但对应的是**编程 API 额度**，不是桌面端积分 | 标准授权 |

### 推荐

**优先验证 D2**：授权完成后浏览器停在 `coze.cn` 站内，若存在「登录态检查」端点（`/passport/account/info/v2/`）且能被插件在**同一台机器**用浏览器 Cookie 访问，则可用「系统浏览器授权 + 插件读取本机浏览器 Cookie」组合；但这又回到 Cookie 依赖。

**更稳的是 D5 + D2 组合**：
1. 插件走官方 OAuth 授权码（`client_id` / `client_secret` 由用户在扣子开放平台创建），拿到 `access_token`（15 分钟）+ `refresh_token`（30 天）；
2. 该 token 调用的是**扣子编程 API**，不是桌面端积分；
3. 若用户目标是「用扣子 Agent 能力」，D5 是合规且稳定的路线；若目标是「白嫖桌面端每日积分」，D5 不覆盖。

## 阻断判定

- **H-00（原假设：插件可用自己的 HTTP 回调接收 `x_tt_token`）→ 证伪。**
- 原 CYCLE-00 的 TASK-001（回调桥接页）与 TASK-002（token beat 换 Cookie）**不能按原设计实施**。
- 需在进入编码前由用户裁定：走 **D5 官方 OAuth（编程 API）**，还是继续探索 **D2（桌面端登录态读取）**。

## 附：验证命令可复现性

```bash
# 证据 1
curl -A "<Chrome UA>" "https://www.coze.cn/sign/oauth?platform=aweme_v2&page_from=coze_pro_sign_in&redirect=<urlencoded>&state=<urlencoded json>"
# 证据 2：用隔离 profile 启动 Chrome，CDP 导航到 /auth/callback 后读 location.href
```

隔离 profile 已清理，端口 9333 已关闭。
