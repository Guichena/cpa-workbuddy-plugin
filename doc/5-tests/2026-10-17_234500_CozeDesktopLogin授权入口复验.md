# TASK-000 复验：`/desktop-login` 授权入口可行性（结论二次更正）

- 验证时间：2026-10-17 23:45（初版）/ 2026-10-17 23:58（二次更正）
- 触发：用户提供真实桌面端授权 URL，指出存在 `/desktop-login` 入口
- 桌面端版本：`D:\Coze\versions\1.1.45`
- 验证方式：app.asar 静态取证 + 隔离无痕 Chrome（CDP，独立 profile）
- 关联：`doc/5-tests/2026-10-17_233000_Coze授权登录redirect_uri可行性验证.md`（`/sign/oauth` 路径的证伪）

## 结论（二次更正）

**`/desktop-login` 的回调确实存在，但回调目标协议被硬编码白名单锁死为 `coze-desktop:` / `coze-desktop-beta:` / `coze-desktop-dev:`，插件无法作为回调接收方。**

初版结论「`redirect` 参数无同源校验，插件可复用」**不成立**——它只验证了 `redirect` 参数可被 `new URL()` 解析为外部地址，未追踪 token 的真实流向。实际登录过程中：

- 登录产生的 `x_tt_token` 只会写入 `redirect_uri`（deep link）的 `x_tt_token` 查询参数，**不会**写入 `redirect`；
- `redirect` 的裸跳转在 token 外送之后才设置，而 token 外送是立即导航，因此该跳转实际不可达。

## 证据

### 证据 1：`redirect_uri` 协议白名单硬编码

模块 `608342`（`renderer/static/js/index.dc9675393a.js`）：

```js
let l="coze-desktop", c="coze-desktop-beta", d="coze-desktop-dev",
    u=new Set([`${l}:`,`${c}:`,`${d}:`]);
let h=e=>{
  if(!e) return null;
  try{ let t=new URL(e); return u.has(t.protocol)?t.toString():null }
  catch{ return null }
};
```

`h()` 是 `redirect_uri` 的唯一校验入口，`http:` / `https:` 一律返回 `null`。

### 证据 2：token 只写入 `redirect_uri`

模块 `608342`：

```js
b=(e,t)=>{
  let r=new URL(e);
  r.searchParams.set("x_tt_token",t);
  v(r.toString())   // v = window.location.assign
}
```

`e` 是 `ZI(search)` 的返回值，即经 `h()` 校验通过的 `redirect_uri`；`t` 是 `fE()` 拿到的 `x-tt-token`。

`/desktop-login` 页面（`async/95506.c7d3be796b.js`）的调用：

```js
(0,u.fE)().then(async t=>{
  if(!t){ p("login"); return }
  ...
  if((0,u.T1)(g,t), await U()){ a(t); p("success"); return }
  s&&(e=setTimeout(()=>{ window.location.href=x },1e3))
})
```

`g` = `ZI(search)`（`redirect_uri` 解析结果），`x` = `redirect` 参数。`T1` 是 token 外送，`x` 是裸跳转——**两条路径互不相干**。

### 证据 3：`redirect` 在 token 外送之后才被使用，实际不可达

同上，执行顺序是：

```js
if((0,u.T1)(g,t), await U()){ ... }   // ① 先把 token 送去 redirect_uri
s&&(e=setTimeout(()=>{ window.location.href=x },1e3))  // ② 再跳 redirect
```

`T1 = (e,t) => { let r=new URL(e); r.searchParams.set("x_tt_token",t); window.location.assign(r) }` 是**立即导航**。只要 `redirect_uri` 合法（`g` 非空），页面在 ① 就已经离开当前文档，② 根本执行不到。

反过来，若 `redirect_uri` 非法（`g` 为 `null`），`T1(null, t)` 抛 `TypeError`，被外层 `.catch` 吞掉并回落 `p("login")`——**不会**跳到 `redirect`。

且外层 `useEffect` 本身有 `if(!g) return` 前置守卫：没有合法 `redirect_uri` 时整段逻辑不执行。

结论：`redirect` 既拿不到 token，也无法在无 `redirect_uri` 时承担回调职责。

### 证据 4：`/desktop-login` 页面在普通浏览器可渲染登录表单

隔离浏览器实测：`hasElectronAPI === false`，页面渲染 `个人版 / 团队版 / 企业版` + `+86 发送验证码` + `登录` + `其它登录方式`（抖音/飞书/火山）。

### 证据 5：`fE()` 的 token beat 契约

```js
fE = async () => {
  let e = await fetch("/passport/token/beat/v2/", {
    method:"POST", credentials:"include",
    headers:{"sdk-version":"2","Content-Type":"application/json"},
    body:JSON.stringify({})
  });
  let t = await e.json();
  return t?.message !== "success" || t?.data?.error_code ? null : e.headers.get("x-tt-token")
}
```

- **带 Cookie 调** → 返回 `x-tt-token` 响应头（浏览器模式）
- 桌面端 `exchangeTokenForSession`：**带 `x-tt-token` 请求头、不带 Cookie** → 返回 `Set-Cookie`（桌面端模式）

这是两条互逆的换发路径，构成续期基础。

### 证据 6：桌面端 Cookie 续期机制

`main/main.js` 的 `syncPassportAuthCookies`：拦截 `COZE_PASSPORT_URL_PREFIX`（`https://www.coze.cn/passport`）响应的 `Set-Cookie`，按 `AUTH_COOKIE_NAMES` 白名单写入 Electron `session.cookies`。桌面端**只发不收**的问题不存在——它靠拦截 passport 响应滚动续期。

## 对实施计划的修正

### 不可行方案

| 方案 | 阻断原因 |
|---|---|
| `redirect_uri` 指向插件 HTTP 回调 | `h()` 协议白名单只允许 `coze-desktop:` 系列 |
| `redirect` 指向插件回调页 | token 不写入 `redirect`；裸跳转在 token 外送（立即导航）之后才设置，不可达 |
| 插件注册 `coze-desktop://` 协议 | 插件运行在服务器；且与桌面端冲突 |

### 可行方案：书签/Console 授权

```text
插件面板点「登录扣子」
  → 打开 https://www.coze.cn（用户正常登录：手机号/抖音/飞书/火山）
  → 插件面板显示「完成授权」步骤 + 一次性书签/脚本
  → 用户在 www.coze.cn 页面执行脚本：
       fetch('/passport/token/beat/v2/', {method:'POST', credentials:'include',
         headers:{'sdk-version':'2','Content-Type':'application/json'}, body:'{}'})
         .then(r => r.headers.get('x-tt-token'))
         .then(t => location.href = '<CPA>/v0/resource/plugins/coze-provider/login-callback?state=<state>&token=' + encodeURIComponent(t))
  → 插件回调页收到 state + token → POST token beat（带 x-tt-token）→ Set-Cookie → 落盘
```

**为什么可行**：

- 脚本在 `www.coze.cn` 同源上下文执行，`credentials:'include'` 带登录 Cookie，能读到 `x-tt-token`；
- token 经 URL 传给插件回调页，插件用桌面端同一契约换 Cookie；
- 全程不读浏览器/桌面端 Cookie 库，符合用户「要授权流程」的裁定。

**体验**：比纯自动多一步「点击书签 / 粘贴脚本」，但不需要装本地程序、不需要注册协议、不需要读 Cookie。

### 续期机制

插件持有 Cookie 后：

1. 带 Cookie 调 `POST /passport/token/beat/v2/`（`sdk-version: 2`）→ 拿新 `x-tt-token`；
2. 带 `x-tt-token` 调同一端点（不带 Cookie）→ 拿新 `Set-Cookie`；
3. 写回 auth 文件。

若步骤 1 的响应同时下发 `Set-Cookie`，可直接用新 Cookie，省去步骤 2。

## 下一步

1. ~~更新实施文档 §2.2 / §3 / TASK-000~~（已完成 2026-10-17 23:58）；
2. 实现授权链路（TASK-001 回调页 + 授权脚本、TASK-002 token beat 换 Cookie）；
3. 真实账号验证：登录 → 执行脚本 → 插件收到 token → 换 Cookie → `account/info/v2` 返回 `sec_user_id`；
4. 验证续期：带 Cookie 调 token beat 是否返回 `x-tt-token`，再换回新 Cookie。

## 附：与前两次结论的关系

| 入口 | 是否可被插件复用 | 依据 |
|---|---|---|
| `/sign/oauth`（抖音 OAuth 中转） | ❌ 不可 | `redirect_uri` 硬编码为 `www.coze.cn/auth/callback` |
| `/desktop-login` 的 `redirect` 参数 | ❌ 不可（初版误判为可） | token 只流向 `redirect_uri`；`redirect` 的裸跳转在立即导航之后，不可达 |
| `/desktop-login` 的 `redirect_uri` 参数 | ❌ 不可 | 协议白名单硬编码 `coze-desktop:` 系列 |
| `/desktop-login` 页面 + 同源脚本 | ✅ 可 | 同源脚本可读 `x-tt-token`，经 URL 交给插件 |
