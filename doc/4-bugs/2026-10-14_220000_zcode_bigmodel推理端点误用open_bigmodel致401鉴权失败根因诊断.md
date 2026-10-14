# zcode-provider bigmodel 账号推理端点误用致 401 鉴权失败根因诊断

## 一、问题描述

- **现象**：CYCLE-3 真实 bigmodel 账号（`ZCode upjrjje0`）授权成功、额度可查（800 万/日）、模型清单可拉（5 个），但**推理稳定 401**：`upstream status 401: {"error":{"message":"令牌已过期或验证不正确"}}`（3/3 复现）。
- **发现方式**：2026-10-14 CYCLE-3 生产端到端验证（`doc/5-tests/2026-10-14_214500_*`），GAP-006。
- **结论（本诊断）**：**不是账号问题、不是凭证问题**。根因是 CYCLE-1 为 bigmodel 选择的推理端点 `open.bigmodel.cn/api/anthropic` **不接受 ZCode JWT**；bigmodel 的 start-plan 推理端点与 zai **共用** `zcode.z.ai/api/v1/zcode-plan/anthropic`。

## 二、证据链（生产实测）

### 2.1 端点 × 凭证矩阵（决定性）

同一生产服务器 IP、同一 bigmodel JWT（`token`，`sub=38601791725770984`）：

| 凭证 | 端点 | 结果 | 判读 |
| --- | --- | --- | --- |
| 有效 bigmodel JWT | `open.bigmodel.cn/api/anthropic` | **401** 令牌已过期或验证不正确 | ❌ 端点不认该 JWT |
| 有效 bigmodel JWT | `api.z.ai/api/anthropic` | **401** token expired or incorrect | ❌ 同上 |
| 有效 bigmodel JWT | `zcode.z.ai/api/v1/zcode-plan/anthropic` | **429/3009 或 405/3012** | ✅ **鉴权通过**（限流/风控） |

### 2.2 鉴权与风控的执行顺序（排除"风控掩盖"）

**同一 IP、同一端点 `zcode.z.ai`，仅更换凭证**：

| 凭证 | 结果 | 判读 |
| --- | --- | --- |
| 完全无效 JWT（`invalid.jwt.token.xxx`） | **401** | 鉴权失败 |
| 空 JWT（无 Authorization） | **401** | 鉴权失败 |
| **有效 bigmodel JWT** | **405/3012 或 429/3009** | **鉴权通过**，被 WAF/限流拦截 |

**关键判读**：若 405/3012 是 IP 级风控（与凭证无关），则无效 JWT 也应返回 405；实测无效 JWT 返回 **401**，证明**鉴权在风控之前执行**，405/429 是**鉴权通过之后**的响应。故 bigmodel JWT 在 `zcode.z.ai` 上**鉴权有效**。

### 2.3 交叉对照（排除端点对 JWT 的通用行为）

| 凭证 | 端点 | 结果 |
| --- | --- | --- |
| **zai** 账号 JWT | `open.bigmodel.cn/api/anthropic` | **401** |
| **bigmodel** 账号 JWT | `open.bigmodel.cn/api/anthropic` | **401** |

**判读**：`open.bigmodel.cn` 对**两类账号的 JWT 都返回 401**，说明该端点根本不接受 ZCode OAuth JWT（它服务的是 `bigmodelIndividualCodingPlan` 付费形态，用另一套 API Key，见下）。

### 2.4 header 组合矩阵（`zcode.z.ai` 端点）

| 组合 | 结果 | 判读 |
| --- | --- | --- |
| **仅 `Authorization: Bearer <JWT>`** | **429/3009** | ✅ 鉴权通过 |
| `Bearer <JWT>` + `x-api-key: <JWT>` | 405/3012 | 鉴权通过（多带头触发风控） |
| 仅 `x-api-key: <JWT>` | **401** | ❌ 该端点不认 x-api-key |
| `Bearer <JWT>` + `X-Coding-Plan-Api-Key: <JWT>` | 405/3012 | 鉴权通过 |

**判读**：`zcode.z.ai` 推理端点要求 **`Authorization: Bearer <JWT>`**，与 zai 完全一致（插件现有实现即为此）。

### 2.5 三账号一致性

三个生产账号（含 1 个 bigmodel、2 个 zai）打 `zcode.z.ai/zcode-plan` **均返回 405/3012 或 429/3009，无一 401**，证明该端点对两个 family 的 JWT 均鉴权有效。

### 2.6 客户端逆向证据（`D:/zcode/resources/app.asar`，ZCode 3.15.1）

**凭证解析**（`resolveCurrent`，`accountRequestAuthService`）：

```js
async resolveCurrent(t){
  ...
  if(r.planKind==="start-plan"){
    let c=await this.#e.loadOAuthTokenSet(y1(r.family));   // 按 family 加载
    return {apiKey: w1(c?.zcodeJwtToken, n)}               // 返回 zcodeJwtToken
  }
  ...
}
```

**判读**：**start-plan（免费版）不分 family，统一使用 `zcodeJwtToken`**——bigmodel 与 zai 在 start-plan 下凭证来源相同。

**端点解析**（`resolveOpenCodeProviderEndpoints`）：

```js
function _Me(e,t,n){
  let r=Wi(e.options?.baseURL)??Wi(e.api);
  return r ? (KY(n,r) ? {baseURL: zY(void 0), paths:{anthropic:"/v1/messages"}}
                     : n===Yt.bigmodelIndividualCodingPlan ? {baseURL: zY(r), paths:{anthropic:"/v1/messages"}}
                     : {baseURL: Mf(r), paths:{[t]: NR(t)}})
           : e.endpoints??e.zcode?.endpoints??{}
}
```

**判读**：`open.bigmodel.cn`（`UY` / `zY()`）仅用于 **`bigmodelIndividualCodingPlan`**（付费个人版）；`bigmodelStartPlan`（免费版）**不在该分支**，走默认端点解析。

**planKind 常量**：

```js
J={zaiStartPlan:"account:zai-start-plan", bigmodelStartPlan:"account:bigmodel-start-plan", ...}
os={zai:"builtin:zai-coding-plan", bigmodel:"builtin:bigmodel-coding-plan"}
```

## 三、根因

CYCLE-1 在 `resolveUpstreamTargets` 中为 bigmodel 选择 `open.bigmodel.cn/api/anthropic`，依据是「客户端 asar 中 family=bigmodel 使用该地址」。但该结论**只对 `bigmodelIndividualCodingPlan`（付费个人版）成立**；`bigmodelStartPlan`（免费版，即 OAuth 授权得到的形态）走的是与 zai 共用的 `zcode.z.ai/api/v1/zcode-plan/anthropic`。

**一句话**：bigmodel 的**授权**与**推理**都应走 `zcode.z.ai`（与 zai 共用），`open.bigmodel.cn` 仅适用于付费 individual-coding-plan 形态。

## 四、修复方案（待实施）

`zcode/upstream_target.go` 的 `resolveUpstreamTargets`：

- **移除** bigmodel 的 `open.bigmodel.cn` 分支（`bigmodelCodingPlanBaseURL`），bigmodel 与 zai 统一使用 `zaiCodingPlanBaseURL`（`zcode.z.ai/api/v1/zcode-plan/anthropic`）。
- `provider_family` 字段**保留**（用于面板归属展示与未来付费形态扩展），但不再影响 start-plan 的端点选择。
- 同步修正 `upstream_target_test.go` 的断言（当前断言 bigmodel → `open.bigmodel.cn`，需改为 `zcode.z.ai`）。
- `business_token.go` 的 bigmodel 业务令牌端点分支：start-plan 下不会被调用（仅 `accessToken` 非空且首目标失败时才尝试），需确认是否保留。

**验证方式**：生产三臂复测——bigmodel 账号探活应不再返回 401（预期 405/429 或 200）。

## 五、影响面

- **仅影响 bigmodel（中国区）账号的推理**；zai 账号不受影响（本就使用 `zcode.z.ai`）。
- 面板归属展示、额度查询、模型清单**均正常**，无需改动。
- 修复为**单点改动**（端点选择），风险低。

## 六、验证与发布

- 本地：`cgo-shim-build.py zcode` build/vet/test 全绿 + 哨兵反证。
- 生产：三臂复测 + 真实推理（预期不再 401）。
- 发布闭环见后续 CHANGELOG 与 git log。

## 七、附：405/3012 与 429/3009 说明

- `405 + code 3012 request has been blocked due to unusual activity`：上游 WAF 风控，与 `X-ZCode-App-Version` 取值强相关（具体版本号必被拦，`unknown` 类字面量才放行，见 `zcode/internal/mimic/fingerprint.go` 与记忆 `reference-zcode-app-version-risk-control`）。
- `429 + code 3009 model concurrency limit exceeded`：业务层并发限流。
- 两者均**在鉴权之后**，故可作为「鉴权通过」的判据。本诊断的对照实验（无效 JWT → 401）证明该判据成立。
