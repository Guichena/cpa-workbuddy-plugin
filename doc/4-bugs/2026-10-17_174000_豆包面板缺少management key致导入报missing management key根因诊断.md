# 豆包面板缺少 management key 致导入报 missing management key 根因诊断

- Bug ID：BUG-DOUBAO-PANEL-NO-MGMT-KEY-20261017
- 报告时间：2026-10-17 17:40:00（北京时间）
- 插件：`doubao-provider`（生产 0.1.0）
- 环境：生产 `https://cpa.luode.vip/management.html`（服务器 45.207.222.65:18998，容器 `cli-proxy-api`）
- 严重级别：高（面板全部功能不可用：导入、刷新账号、启停、删除）

## 一、现象

用户在生产管理台「插件 → Doubao」面板粘贴浏览器 Cookie 后点击「导入」，页面回显：

```
导入失败：missing management key
```

同一面板的「刷新账号」同样无数据返回。用户据此判定导入功能损坏。

## 二、取证

### 2.1 宿主对未鉴权请求的响应（生产实测）

在服务器本机对同一路由发两次请求：

```
# 不带 Authorization
curl -s -w "\nHTTP=%{http_code}\n" http://127.0.0.1:8317/v0/management/plugins/doubao-provider/accounts
{"error":"missing management key"}
HTTP=401

# 带 Authorization
curl -s -o /dev/null -w "HTTP=%{http_code}\n" -H "Authorization: Bearer $KEY" \
  http://127.0.0.1:8317/v0/management/plugins/doubao-provider/accounts
HTTP=200
```

结论：`missing management key` 由**宿主中间件**产生（401），不是插件业务错误。插件本身在该路由上工作正常。

### 2.2 面板请求构造（源码定位）

`doubao/panel.html`（0.1.0）的 `api()`：

```js
async function api(path, opts) {
  const headers = Object.assign({ "Content-Type": "application/json" }, (opts && opts.headers) || {});
  const res = await fetch(API + path, Object.assign({}, opts, { headers, cache: "no-store" }));
  ...
}
```

只设置 `Content-Type`，**从不设置 `Authorization`**，也没有任何获取 management key 的逻辑。

### 2.3 与同仓库其他插件的对照

| 插件 | 是否有 key 获取逻辑 | `authBox` 手动输入 | `saveKey()` |
|---|---|---|---|
| `copilot-provider` | 有（父窗口 localStorage） | 无（纯嵌入场景） | 无 |
| `workbuddy-provider` | 有（三段式） | 有 | 有 |
| `qoderwork-provider` | 有（三段式） | 有 | 有 |
| `zcode-provider` | 有（三段式） | 有 | 有 |
| `traework-provider` | 有（三段式） | 有 | 有 |
| **`doubao-provider`** | **无** | **无** | **无** |

（`grep -c 'authBox'` / `grep -c 'function saveKey'` 逐插件统计）

## 三、根因

豆包面板在创建时未移植同仓库既有插件的 management key 获取范式。宿主对 `/v0/management/*` 统一鉴权，面板发出的每个请求都缺 `Authorization`，因此全部被宿主以 401 拦断，前端把宿主的 `error` 字段直接拼进「导入失败：」提示，表现为「导入失败」。

## 四、修复（0.1.1）

按 `copilot` / `workbuddy` 的同构实现补齐三段式取 key：

1. **父窗口 localStorage**：iframe 嵌入时读 `cli-proxy-auth`，按 `enc::v1::` + XOR(base64, salt|host|UA) 解密，取 `state.managementKey`；
2. **URL `?key=`**：读取后从地址栏剔除（保留 `theme` 等其它 query）并落 `sessionStorage`；
3. **手动输入**：面板顶部新增密钥输入框与「连接」按钮，写入 `sessionStorage` 后重新拉取账号。

同时补 401/403 处理：清除失效 key、重新弹框、给出可读提示（不再回显「未知错误」），且不自动重发。

## 五、验证

| 测试点 | 结果 |
|---|---|
| 无 key 时不发请求 + 弹框 + 可操作提示 | PASS |
| sessionStorage 已有 key → 请求带 `Authorization: Bearer` | PASS |
| 父窗口 localStorage 兜底 → 解出 key 并固化 | PASS |
| URL `?key=` 兜底 → 剔除地址栏参数、保留 theme、落盘 | PASS |
| 手动填入 → 写 sessionStorage 并重新拉取账号 | PASS |
| 401 → 清 key、弹框、可读提示、不自动重发 | PASS |
| 既有账号管理回归（17 项） | 全部通过 |
| 既有导入回归 | 全部通过 |
| 变异自测①：删除 `authHeaders(key)` | 4 项断言失败（证明断言有效） |
| 变异自测②：删除无 key 拦截 | 3 项断言失败（证明断言有效） |
| `cgo-shim-build.py doubao` | build / vet / test 全绿 |

## 六、生产部署与真实浏览器验收（0.1.1）

| 项目 | 结果 |
| --- | --- |
| CI | dispatch `39021666413` → **success**；7 平台全部 success；Release `doubao-provider-v0.1.1` 8 个资产 |
| 产物校验 | `ALL CHECKSUMS OK`（7/7）；zip 根条目为裸库名 |
| registry | 仅 `doubao-provider` 由 0.1.0 → 0.1.1（+22/−22），其余 9 条未变；远端 7 个 artifact 哈希全一致 |
| 生产安装 | HTTP 200，`restart_required=false` |
| 落盘哈希 | `47d0411e18baded483e885f26a509940ae5ca9700a3fe420c5f9317b4da02c4a`，7,883,392 字节，与 release zip 内 `.so` 一致 |
| 热重载 | `plugin hot reloaded plugin_id=doubao-provider active_version=0.1.1 retired_version=0.1.0` |
| 生产面板资源 | 200，15,504 字节；含 `management key acquisition` / `doubao-mgmt-key` / `authBox` / `authHeaders` |
| 容器状态 | 未重启（`RestartCount=0`） |

### 真实浏览器验收（生产 `cpa.luode.vip`）

| 场景 | 结果 |
| --- | --- |
| 无 key 打开面板 | `authBox` 显示 `block`；提示「加载账号失败：需要管理密钥，请在 CPA 主面板中打开本页，或在下方向输入框填入」——不再是 `missing management key` |
| 输入错误 key | 401 → 清 sessionStorage → 重新弹框；提示「管理密钥无效或已失效，请重新填写」 |
| 输入正确 key | 弹框关闭；账号列表渲染「生产验收」；计数「共 1 个账号」 |
| 事后清理 | 浏览器 sessionStorage 中测试密钥已删除（`ssKeys=0`） |

## 七、结论

- 根因：面板缺少 management key 鉴权逻辑（移植遗漏），非宿主缺陷、非导入链路缺陷。
- 修复：0.1.1 补齐三段式取 key 与 401 处理，已发布并热重载到生产。
- 回归资产：`test/doubao/panel_auth_repro.mjs`（16 项断言，含两条变异自测验证）。
- 遗留：生产导入的账号文件仍缺 `device_id` / `web_id` / `web_tab_id`（见 CYCLE-04 测试文档 §3.4），建议单独立项。
