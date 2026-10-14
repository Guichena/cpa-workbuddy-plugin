# zcode-provider bigmodel 中国区授权（CYCLE-3）真实账号端到端测试记录

- 对应需求：`doc/2-需求/2026-10-14_153951_zcode支持bigmodel中国区授权需求.md`（`REQ-ZCODE-BIGMODEL-OAUTH-20261014`）
- 对应实施：`doc/3-实施/2026-10-14_zcode支持bigmodel中国区授权实施总览.md`
- 前序测试：CYCLE-1（`doc/5-tests/2026-10-14_170000_*`）、CYCLE-2（`doc/5-tests/2026-10-14_183000_*`）
- 测试时间：2026-10-14
- 测试范围：TASK-006，真实 bigmodel 账号端到端（授权 → 额度 → 模型清单 → 推理）
- 生产版本：`zcode-provider 0.1.26`（热重载，`active_version=0.1.26 retired_version=0.1.25`）

## 一、测试环境

- 生产入口：`https://cpa.luode.vip`（管理 API 经服务器本机 `http://127.0.0.1:8317`）
- 生产账号基线：5 个（4 个 `zai` + 1 个 `bigmodel`）
- 真实账号：`ZCode upjrjje0`（`auth_id=zcode-5f59450c99a565fb`，`provider_family=bigmodel`），由用户在生产面板选「中国区」发起授权并在 `bigmodel.cn/login` 完成登录

## 二、测试结果

| # | 环节 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 面板发起 bigmodel 授权 | ✅ PASS | `POST /activate-start {"provider":"bigmodel"}` → `authorize_url` 主机 = `bigmodel.cn`，含 `appId=zcode` |
| 2 | 授权落盘与归属 | ✅ PASS | 账号落盘 `provider_family=bigmodel`，`handleListAccounts` 正确透出；面板卡片渲染归属徽标 |
| 3 | 额度拉取（GAP-004） | ✅ PASS | `GET /credits?force=1` 返回 `plan_id=zcode-v3-start-plan-0817`、`plan_name=ZCode Start Plan`、`plan_status=active`、`total_grant=8000000` |
| 4 | 模型清单（GAP-005） | ✅ PASS | 与 `zai` 账号**完全一致**：`GLM-5.3` / `GLM-5.3-Flash` / `GLM-4-Plus` / `GLM-4-Air` / `GLM-4-Flash` |
| 5 | 真实推理 | ❌ **FAIL** | `POST /test-active {"auth_id":"zcode-5f59450c99a565fb","model":"GLM-5.3"}` → `upstream status 401: {"error":{"message":"令牌已过期或验证不正确"}}`（3/3 稳定复现） |

## 三、GAP-004 解除（核心业务价值确认）

**中国区账号授权后自带 start-plan 每日额度，无需客户端手动领取** —— 这是本需求的核心业务价值。

`GET /credits?auth_id=zcode-5f59450c99a565fb&force=1` 响应（脱敏）：

```json
{"quota":{
  "description":"免费 GLM 旗舰模型体验",
  "plan_id":"zcode-v3-start-plan-0817",
  "plan_name":"ZCode Start Plan",
  "plan_status":"active",
  "total_grant":8000000,
  "entitlements":[
    {"capability":"model:glm-5.3","grant_units":3000000,"period":"daily","show_name":"GLM-5.3"},
    {"capability":"model:glm-5.3-flash","grant_units":5000000,"period":"daily","show_name":"GLM-5.3-Flash"}
  ]}}
```

**关键差异**：与国际站 `zai` 不同（国际站需在 ZCode 桌面客户端内通过阿里云人机验证手动 `claim`），中国区 `bigmodel` 授权完成后**额度即已 active**（`plan_status=active`，`total_grant=8000000`）。且额度查询端点两家**共用** `zcode.z.ai`（与 CYCLE-1 的 GAP-002 结论一致）。

## 四、GAP-005 解除（模型清单无差异）

`bigmodel` 与 `zai` 账号的 `models` 字段**逐项相同**（5 个模型全一致），故 `models.go` **无需** family 分支。

## 五、新发现缺口：bigmodel 推理鉴权失败（GAP-006，P0 阻断）

### 5.1 现象

`bigmodel` 账号在插件探活路径（`pingUpstreamOnce` → `resolveUpstreamTargets` 首目标）返回 **HTTP 401「令牌已过期或验证不正确」**，3/3 稳定复现；`zai` 账号对照返回 405/3012（已知风控，见记忆 `reference-zcode-app-version-risk-control`），两者症状不同。

### 5.2 凭证 × 端点矩阵（生产实测）

| 凭证 | `open.bigmodel.cn/api/anthropic` | `bigmodel.cn/api/anthropic` | `zcode.z.ai/api/v1/zcode-plan/anthropic` |
| --- | --- | --- | --- |
| `token`（JWT，204B） | **401** 令牌无效 | **401** 令牌无效 | 405/3012 风控 |
| `api_key`（JWT，204B） | **401** 令牌无效 | **401** 令牌无效 | — |
| `access_token`（335B） | **500** Internal Network Failure | **500** Internal Network Failure | — |

**关键判读**：`access_token` 得到 **500**（非 401）—— 说明**鉴权层已通过**，失败发生在鉴权之后的上游内部链路；而 JWT（`token`/`api_key`）得到 401，说明 **JWT 不是 bigmodel 推理的正确凭证**。

### 5.3 业务令牌换取路径（生产实测）

| 端点 | 请求 | 结果 |
| --- | --- | --- |
| `bigmodel.cn/api/auth/z/login` | `{"token": <JWT>}` | HTTP 200 + `{"code":500,"msg":"z.ai用户信息异常","success":false}` |
| `open.bigmodel.cn/api/auth/z/login` | `{"token": <JWT>}` | 同上 |
| `bigmodel.cn/api/auth/z/login` | `{"token": <access_token>}` | 同上 |

**判读**：业务令牌换取端点报「**z.ai用户信息异常**」——bigmodel 中国区账号在 z.ai 侧无用户信息，故 CYCLE-1 设计的「复用 zai 业务令牌链路」对 bigmodel **不成立**。

### 5.4 端点变体探测（JWT + `X-ZCode-App-Version: unknown`）

| 端点 | 结果 |
| --- | --- |
| `open.bigmodel.cn/api/v1/zcode-plan/anthropic/v1/messages` | 500 `404 NOT_FOUND`（路径不存在） |
| `bigmodel.cn/api/v1/zcode-plan/anthropic/v1/messages` | 500 `404 NOT_FOUND`（路径不存在） |
| `open.bigmodel.cn/api/anthropic/v1/messages` | 401 |
| `bigmodel.cn/api/anthropic/v1/messages` | 401 |

`X-ZCode-App-Version`（`unknown` / `3.15.1`）对 401/500 结果**无影响**，排除风控头因素。

### 5.5 待验证假设

1. **凭证形态**：bigmodel 推理可能需 `access_token` 之外的第三种凭证（如 `resolveBigModelStartPlanZcodeJwt` 从 credentialService 载入的 JWT，见需求 GAP-003），需逆向 `D:/zcode/resources/app.asar` 中 `family==="bigmodel"` 分支确认。
2. **poll 响应字段取用顺序**：`settleZaiPollResult` 优先取 `resp.Data.Zai.AccessToken`、仅在其为空时回退 `resp.Data.Bigmodel.AccessToken`。若 bigmodel 的 poll 响应**同时**含 `zai` 与 `bigmodel` 两段且语义不同，可能取错了段。需抓取一次真实 bigmodel poll 原始响应确认。
3. **500 的成因**：`access_token` 触发的 `Internal Network Failure` 可能是 bigmodel 侧网关在凭证正确但套餐/权限未就绪时的表现，需在客户端内确认该账号能否正常推理（排除账号本身问题）。

## 六、AC 覆盖情况

| AC | 状态 | 说明 |
| --- | --- | --- |
| AC-001 面板可选 bigmodel 发起授权 | **达成** | 生产实测 `authorize_url` → `bigmodel.cn` |
| AC-002 bigmodel 授权端到端可用 | **部分** | 授权 / 额度 / 模型清单 3 项通过；**推理 FAIL（GAP-006）** |
| AC-003 zai 既有授权行为零回归 | **达成** | 三臂复测 `zai` → `chat.z.ai` 3/3 |
| AC-004 卡片区分归属 | **达成** | 生产账号 `provider_family=bigmodel` 正确透出 |
| AC-005 cgo-shim 全绿 | **达成** | 见 0.1.26 测试记录 |
| AC-006 未知 provider 明确报错 | **达成** | 0.1.26 已修复（HTTP 400） |

## 七、结论

- **业务价值确认**：中国区 bigmodel 账号**自带每日 800 万额度**（无需客户端 claim），需求动因成立。
- **GAP-004 / GAP-005 已解除**。
- **新增 P0 阻断 GAP-006**：bigmodel 推理鉴权失败（JWT → 401，access_token → 500），当前实现复用的 zai 链路对 bigmodel 不适用，需进一步定位正确凭证与端点组合。

## 八、未覆盖与说明

- 本轮为生产只读验证（除面板授权由用户操作外，未改动生产配置）。
- GAP-006 定位需客户端逆向与真实 poll 响应抓取，属后续周期。
