# Changelog

## 0.1.2 (2026-10-18)

### 修复

- **面板授权脚本仍被 HTML 实体化**（0.1.1 修复不完整）：0.1.1 只改了
  `okEnvelope`，但 `handleLoginStartFromPanel` 在更早一层用 `json.Marshal`
  生成 payload，转义发生在那一步，信封层再关转义也无济于事。改为
  `marshalNoHTMLEscape`。生产复验：0.1.1 的 `login-start` 仍含 26 处
  `&#39;` / 2 处 `&gt;` / 0 处 `=>`。
- 补 `TestLoginStartFromPanel_ScriptNotEscaped` 直接打真实入口（此前只有
  信封级用例，覆盖不到这条路径）。

## 0.1.1 (2026-10-18)

### 修复

- **授权脚本被 HTML 实体化**：`okEnvelope` 原先用 `json.Marshal`，它默认开启
  HTML 转义，把脚本里的 `=>`、`'`、`&&` 变成 `=&gt;`、`&#39;`、`&amp;&amp;`，
  面板复制到的脚本无法执行。改用 `json.Encoder` + `SetEscapeHTML(false)`。
  生产实测证据：`login-start` 响应 774 字节中含 26 处 `&#39;`、2 处 `&gt;`、
  0 处 `=>`。

## 0.1.0 (2026-10-18)

CYCLE-00 首发：扣子（Coze）授权登录链路。

### 新增

- **授权登录**：面板生成一次性同源授权脚本，用户在扣子页面执行后把
  `x-tt-token` 交给插件；插件用该令牌调 `passport/token/beat/v2/` 换取会话
  Cookie 并落盘。全程不读浏览器 Cookie 库、不内嵌浏览器、不注册自定义协议。
  令牌经 URL fragment + 同源请求头两段式回传：fragment 不发给服务端，
  因此不会落进宿主的访问日志。
- **登录态判据**：`passport/account/info/v2/` 的 `message=success` 且
  `sec_user_id` 非空双条件判定；判据不通过不落盘。
- **Cookie 持久化**：按 `(name, domain, path)` 三元组存储，保留同名分域
  Cookie（真实端点下发的 13 条中 `uid_tt` / `uid_tt_ss` / `session_tlb_tag`
  各出现两次）。
- **滚动续期**：`passport/token/beat/v2/` 是双向换发端点——带 Cookie 换
  `x-tt-token`，带 token 换新 `Set-Cookie`；插件据此实现
  「Cookie → 新 token → 新 Cookie」循环，无需用户反复授权。
- **账号管理**：列表、删除、启用、禁用、手动续期；面板展示授权时间、
  最近续期时间与登录态状态。
- **宿主集成**：`auth.parse` / `auth.login.start` / `auth.login.poll` /
  `auth.refresh` 全链路实现，宿主「添加账号」弹窗可正常拉起。

### 已知限制

- 只实现认证链路（CYCLE-00）：**未声明 executor / model_provider**，
  对话能力（CYCLE-01）尚未实现。
- 授权需用户在扣子页面手动执行一次脚本（因官方 `redirect_uri` 协议白名单
  只认 `coze-desktop:` 系列，插件无法作为 OAuth 回调接收方）。
- 续期巡检尚未接入后台定时器，当前由宿主 `auth.refresh` 与面板手动触发。

### 实测依据

- 真实账号端到端验证：令牌 355 字符 → 13 条 Set-Cookie → `sec_user_id`
  76 字符 → 续期闭环成立。见
  `doc/5-tests/2026-10-17_235500_Coze授权链路端到端验证.md`。
