# Qoder 国际版账号「模型几乎无法使用」根因诊断——IP 风控证伪与上游 RESOURCE_LIMIT 取证

- 编号：BUG-QODER-INTL-RESOURCE-LIMIT-20261015
- 发现时间：2026-10-15（用户报告"qoder 国际版的账号依旧是几乎无法使用的状态，可以签到的积分，但是模型几乎都无法使用，类似于被 IP 风控"）
- 判定时间：2026-10-15 16:50（北京时间）
- 状态：**已定位**。用户假设（IP 风控）**已证伪**；真实成因是上游账号×模型级资源限制，非本仓库可修；配套的错误语义缺陷已由并行会话修复（见 §7）。
- 关联：`doc/4-bugs/2026-10-15_160000_Qoder排队帧被误判账号级故障且跨provider状态码通道缺失根因诊断.md`（同一上游现象的另一侧面：排队帧记账语义 + 跨 provider 状态码通道）

## 1. 现象

用户描述：qoder-ai-provider（Qoder 国际版）账号"几乎无法使用"——**签到正常、积分正常**，但**模型调用几乎全部失败**，怀疑是 IP 风控。

生产日志侧的可观测形态：

```
[qoder-ai] callModelsAPI: upstream status 403, body: {"code":"101","message":"Signature invalid"}
upstream execution failed: provider=qoder-ai-provider model=Qwen3.8-Max
  err=upstream 200: 403: 10605: {"isQueued":true,"modelKey":"qmodel_38max",
      "queueCount":0,"queueType":"p3","retryAfterSeconds":30,
      "serviceAvailable":false,"subCode":"RESOURCE_LIMIT","waitTime":30}
upstream execution failed: provider=qoder-ai-provider model=Qwen3.8-Max
  err=upstream 200: 112: {"pricingUrl":"https://qoder.com/pricing?client=qoder"}
```

## 2. 用户假设的证伪：不是 IP 风控

用户的核心疑问是"是不是被 IP 风控"。本轮用**独立复刻签名的探测脚本**（绕过插件与宿主，直连上游 `agent_chat_generation`）逐项排除：

### 2.1 签名算法、COSY 头、设备指纹全部有效

探测脚本复刻了插件同款算法（RSA-PKCS1v15 包裹 AES-128 session key + AES-128-CBC identity + MD5 请求签名 + QoderEncoding 自定义 base64），**同一出口 IP** 直连上游：

- 用极简 body 时，上游**通过签名校验**（无 `101 Signature invalid`、无 `10605`），仅因缺 agent 上下文被 `agent_router` 以 400 拒绝；
- 用插件真实 `baseprompt.json` 模板 body 时，上游**复现出与插件完全一致的 `10605`**。

**结论**：签名、COSY 头、设备指纹、出口 IP **全部有效**。若是 IP 被封或签名失效，上游不会返回业务层的 `10605`，而会是 `101 Signature invalid` 或直接拒绝连接。

### 2.2 头参数差异不是成因（A/B 实验）

协议调研（`bluechonk/modelmux` PROTOCOL.md）提示当前官方客户端 `COSY_VERSION = 1.1.64`、`Cosy-ClientType` 应为 `10`、`User-Agent` 应为 `Qoder`，而插件用的是 `0.1.43` / `5` / `Go-http-client/2.0`。据此做三组 A/B：

| 变体 | 头配置 | 结果 |
|---|---|---|
| A | 现状：`clienttype=5`、`ua=Go-http-client/2.0`、`cosy=0.1.43` | `10605` + `subCode:RESOURCE_LIMIT` |
| B | `clienttype=10`、`ua=Qoder`、`cosy=0.1.43` | `IncompleteRead`（连接被上游复位） |
| C | `clienttype=10`、`ua=Qoder`、`cosy=1.1.64` | `10605` + `subCode:RESOURCE_LIMIT` |

**三组全部失败**，且 A 与 C 错误体一致——**头参数、COSY 版本均非成因**。

### 2.3 签到成功不能证明 IP 正常，但 chat 端点独立可用性可以

签到接口与模型接口**同域名**（`openapi.qoder.sh`），只是路径不同，故"签到能过"本身不构成 IP 正常的证据。真正的判据是 §2.1 的直连签名校验通过。

### 2.4 与社区调研一致

GitHub 调研（子代理，详见 §6）显示同类反代项目 `shuishuipingan/qoder2api-hub` 的 README 明确写「**实测也没有『上游按 IP 风控』的信号**」；官方论坛亦承认风控按「设备指纹跨 CN/国际两套产品」做多账号关联判定，并建议「IP 仅作弱信号」。

**综上：IP 风控假设证伪。**

## 3. 取证方法论纠正（本轮自纠）

**必须记录一次方法论错误**：本轮早期曾用 `/v1/chat/completions`（宿主全局入口）测试，看到 `qwen-3.8-max`、`gpt-5.6-sol` 等"未映射模型名"返回 200，据此得出"透传成功、映射 key 失效"的结论。

**该结论是错的。** 查 feed 发现那些成功请求的 `provider` 是 **`traework-provider`**，不是 qoder-ai：

```
traework-provider | qwen3.8-max  | failed=False   ← 宿主把该模型路由给了 traework
qoder-ai-provider | qmodel_38max | failed=True    ← qoder-ai 的真实请求全部失败
```

宿主对同一模型名会在多个 provider 间路由，全局入口的成败**不能**归因到 qoder-ai。

**唯一有效的取证通道是账号级 `/v0/management/plugins/qoder-ai-provider/test-active`**（直连插件，绕过宿主路由）。后续全部结论均基于该通道。

## 4. 真实成因：上游账号×模型级资源限制

### 4.1 账号级真实成功率（test-active，各 8 轮）

```
qmodel_38max   OK=0/8   ERR=8   全部 10605 RESOURCE_LIMIT
qfmodel        OK=1/8   ERR=7   全部 10605 RESOURCE_LIMIT
```

13 个 qoder-ai 账号全部 `status=error`，`fail_count` 6–9，`success_count=0`，note 为 `CN · 积分未知`。

### 4.2 错误体语义

```json
{"isQueued":true,"modelKey":"qmodel_38max","queueCount":0,
 "queueType":"p3","retryAfterSeconds":30,"serviceAvailable":false,
 "subCode":"RESOURCE_LIMIT","waitTime":30}
```

**关键：`queueCount: 0`（没有排队）** + `serviceAvailable: false` + `subCode: RESOURCE_LIMIT`。

这与并行会话文档记录的形态（`serviceAvailable:true` + `queueCount:14250` + `waitTime:478`）**不同**：

| 字段 | 并行会话记录 | 本轮记录 |
|---|---|---|
| `serviceAvailable` | `true` | **`false`** |
| `queueCount` | 14250（上万） | **0** |
| `waitTime` | 478 | 30 |
| `subCode` | （无） | **`RESOURCE_LIMIT`** |

两者共享 `10605` 业务码，但语义不同：前者是**真排队**（服务可用、队列饱和），后者带 `RESOURCE_LIMIT` 且 `serviceAvailable:false`，更像**该账号对该模型被服务端限流/降级**。

同时日志中另有 `112`（带 `pricingUrl: qoder.com/pricing`）——额度/套餐类错误。6 小时窗口内 `10605` 83 次、`112` 9 次，且失败账号集中在特定几个。

### 4.3 是否本仓库可修

**不是。** 证据链：
- 独立探测脚本（不经过任何本仓库代码）复现同一错误 → 成因在上游；
- 签名/头/IP 均已证伪（§2）；
- 签到成功、额度充足（884/900）→ 账号未被封；
- 上游明确返回 `subCode: RESOURCE_LIMIT`（服务端资源限制）。

这是**上游账号侧状态**，本仓库无法修复。

## 5. 触发面探测（(b) 专项）

尝试用多账号 × 多模型矩阵定位 `RESOURCE_LIMIT` 的触发边界（Free 号 vs Pro Trial 号、不同模型、body 大小）。受两个客观限制，未能取得完整矩阵：

1. **合成 body 不稳定**：脚本合成的极简 body 大量返回 `IncompleteRead`（上游对非官方 body 形态会复位连接），只有真实 `baseprompt.json` 模板能稳定复现 `10605`；
2. **探测脚本的 HTTP/2 流式读取局限**：长连接场景下脚本侧超时/半读，产生 `NET` 噪声。

已获得的有限数据：以真实模板为 body 时，**多个账号、多个模型（`qmodel_38max` / `auto`）均返回同类 `10605`**，与 §4.2 一致。据此判定 `RESOURCE_LIMIT` 是**账号×模型级**而非单模型级，但**未完成**跨套餐类型（Free vs Pro Trial）的对照——如实记录为未覆盖项。

## 6. GitHub 同类反代调研（子代理，只读）

| 项目 | 价值 |
|---|---|
| [shuishuipingan/qoder2api-hub](https://github.com/shuishuipingan/qoder2api-hub) | 双区出口、设备指纹隔离、COSY 1.1.64；README 明确「实测也没有『上游按 IP 风控』的信号」 |
| [bluechonk/modelmux PROTOCOL.md](https://github.com/bluechonk/modelmux/blob/main/channels/qoder/docs/PROTOCOL.md) | 最完整的协议规格：必需头清单、COSY 版本、端点、错误码表 |
| [mmqz/cpa-multi-plugins](https://github.com/mmqz/cpa-multi-plugins) | 与本项目同形态的 CPA 插件集（含 `qoderwork/sign.go`），可对照 |
| [itandelin/qoder-free](https://github.com/itandelin/qoder-free) | 设备指纹字段清单 |

关键要点：
- 国际版 model key 清单（17 个）含 `ultimate` / `performance` / `efficient` / `smodel`——本项目映射表**未收录**；但本轮实测这些 key 同样返回 `RESOURCE_LIMIT`，故补全 key 表**不能**解决本问题。
- 错误码语义（[官方文档](https://docs.qoder.com/zh/cli/sdk/errors)）：`10605=模型排队`、`110=当日额度`、`113/118=Credits 耗尽`、`105=登录态过期`；**`101 Signature invalid` 官方未收录**。
- 端点：国际为 `api1/2/3.qoder.sh` + `openapi.qoder.sh`，国内为 `gateway/openapi.qoder.com.cn`——本项目只用单一 `openapi.qoder.sh`，理论上可评估多端点轮换（本轮未验证其对本问题的改善）。

**调研未找到**任何公开的 `QoderEncoding` 完整算法文档，也未找到专门讨论「同账号不同出口 IP 可用性差异」的实证项目。

## 7. 配套缺陷与修复状态

本轮发现一个**插件侧真实缺陷**：上游的 `10605` / `RESOURCE_LIMIT` 被包装成 `auth_unavailable` + HTTP 500，客户端无法区分"稍后重试"与"账号挂了"。

**该缺陷已由并行会话修复并提交**（`31ec39c`，qoderwork 0.9.39 / qoder-ai 0.1.19）：
- 新增 `isQueueFrame`（判据 `isQueued` / `queueCount` / `10605`）→ 命中时不进账号级记账、不冷却账号；
- 新增 `envelopeHTTPStatusForError` → 排队帧对外带 `http_status: 503`，打通宿主跨 provider 轮转通道。

**本轮独立验证**：本次发现的 `RESOURCE_LIMIT` 变体（`queueCount:0`、`serviceAvailable:false`）三个 marker（`isQueued` / `queueCount` / `10605`）**全部命中** `isQueueFrame`，即该修复对本次变体同样生效。

**生产状态**：生产运行 0.9.38 / 0.1.18，**尚未**包含该修复（registry 亦为 0.9.38 / 0.1.18）；修复的发布链路属并行会话在途工作。

## 8. 结论与建议

1. **不是 IP 风控**——签名、COSY 头、设备指纹、出口 IP 均经独立探测验证有效（§2）。
2. **不是本仓库可修的问题**——成因是上游账号×模型级资源限制（`subCode: RESOURCE_LIMIT`），独立于本仓库代码（§4）。
3. **签到能过、积分正常**与模型不可用**不矛盾**——二者是上游不同子系统的独立状态。
4. **配套的错误语义缺陷值得修**，且已由并行会话完成（§7）；本轮确认其对 `RESOURCE_LIMIT` 变体同样有效。
5. **建议**：
   - 账号层面：观察上游 `RESOURCE_LIMIT` 是否随时间恢复；如长期不恢复，考虑更换/新增账号或改用其他 provider 承载同名模型。
   - 可选后续：评估国际版多端点（`api1/2/3.qoder.sh`）轮换是否能绕开账号级限制（本轮未验证）。
   - 可选后续：补全映射表缺失的 `ultimate` / `performance` / `efficient` / `smodel`（**不影响**本问题，但补齐模型覆盖面）。

## 9. 未覆盖项（如实记录）

- 未完成 Free 号 vs Pro Trial 号在 `RESOURCE_LIMIT` 上的对照实验（探测脚本对非官方 body 形态不稳定，见 §5）。
- 未验证国际版多端点轮换对 `RESOURCE_LIMIT` 的改善。
- 未验证 `RESOURCE_LIMIT` 是否存在时间窗自愈。
- `callModelsAPI` 的 `403: 101 Signature invalid`（模型列表接口）与本问题**不同源**——chat 端点可用时它仍 403；本轮未深入定位（属独立小问题）。
- 本轮探测全部为只读；临时探测脚本与账号清单已从本地与服务器清理，全程未回显凭据原值。
