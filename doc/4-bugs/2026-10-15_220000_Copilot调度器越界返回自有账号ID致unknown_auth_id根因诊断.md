# Copilot 调度器越界返回自有账号 ID 致 unknown auth id 根因诊断与修复

- Bug 编号：BUG-COPILOT-SCHEDULER-OUT-OF-SCOPE-20261015
- 发现时间：2026-10-15（北京时间）
- 影响范围：copilot-provider 调度路径 + 生产实例全部 provider 的调度请求
- 严重级别：高（插件调度意图被完全忽略；活跃 scheduler 拦截全部 provider 调度请求）
- 状态：**已定位 / 已修复**

## 1. 现象

用户报告「授权完成了但没有跳转成功，面板也没有成功的账号」，随后在生产日志中持续观察到告警刷屏：

```
[2026-10-15 21:22:49] [warn] [scheduler.go:27] pluginhost: scheduler returned invalid response:
unknown auth id plugin_id=copilot-provider
```

3 小时窗口内累计 **1165 条**（约每 1~2 秒一条）。

## 2. 根因

### 2.1 直接原因：插件忽略宿主候选集，返回自有账号池 ID

`copilot/scheduler.go:82 handleSchedulerPick` 完全忽略宿主下发的 `req.Candidates` 与 `req.Provider`，直接调用插件自有的 `pickActiveAuth()`（从落盘账号池轮询）并返回其 `AuthID`：

```go
sa := pickActiveAuth()
if sa == nil {
    return okEnvelope(pluginapi.SchedulerPickResponse{Handled: false})
}
return okEnvelope(pluginapi.SchedulerPickResponse{
    Handled: true,
    AuthID:  sa.AuthID,   // ← 越界：未在 req.Candidates 内校验
})
```

### 2.2 ID 口径不一致（触发条件）

宿主 `internal/pluginhost/scheduler.go:104 normalizeSchedulerResponse` 强制要求返回的 `AuthID` 逐字命中 `req.Candidates[].ID`：

```go
if hasAuthID {
    if !schedulerCandidateExists(req.Candidates, resp.AuthID) {
        return pluginapi.SchedulerPickResponse{}, false, "unknown auth id"
    }
    ...
}
```

两侧 ID 形态实测不一致：

| 侧 | ID 形态 | 证据 |
| --- | --- | --- |
| 宿主 `req.Candidates[].ID` | `copilot-f13aadb583ef01ba.json`（**带** `.json`） | 生产 `/v0/management/auth-files` 返回 `"id": "copilot-f13aadb583ef01ba.json"`；`schedulerAuthCandidates` 直接取 `auth.ID`（`conductor_selection.go:860`） |
| 插件 `pickActiveAuth().AuthID` | `copilot-f13aadb583ef01ba`（**不带**） | 落盘 `auth_id` 字段；`authfile.go:103 authIDFor` 生成 `copilot-<sha256[:8]>` 无扩展名 |

宿主 ID 由 `pluginAuthDataToCoreAuth`（`auth_provider.go:634`）在 `data.ID` 为空时回填 `authIDForPath(path, authDir)` = 相对 authDir 的路径形态；登录路径上插件返回的 `ID: stored.AuthID` 虽非空，但 watcher 合成路径经 `ParseAuth`（插件对导入文件返回 `ID: ""`）走 `authIDForPath` 得带 `.json` 的形态。两条路径最终统一为带扩展名的文件相对路径。

### 2.3 放大效应：copilot-provider 是当前唯一活跃 scheduler

宿主 `schedulerRecord()`（`internal/pluginhost/scheduler.go:45`）遍历 `activeRecords()`（按 priority 降序、同值按 id 字母序，见 `snapshot.go:149 sortRecords`）返回**第一个**具备 `Capabilities.Scheduler` 且未 fuse 的记录。生产 6 个插件均声明 `Scheduler: true`，排序后 `copilot-provider` 居首。

**因此该插件会收到所有 provider 的调度请求，并对每个请求都返回 copilot 账号 ID** —— 对非 copilot 的请求而言该 ID 必然越界，全部判 `unknown auth id`。

### 2.4 佐证：告警爆发时的真实流量非 copilot

`21:22:49` 告警爆发的同一秒，日志中的实际请求是 mixed provider 流量：

```
[21:22:49] session-affinity: cache hit | auth=workbuddyai-d6d15470-... provider=mixed model=deepseek-v4.1-flash
[21:22:52] session-affinity: cache hit | auth=antigravity-siradjtaim04@gmail.com.json provider=mixed
```

坐实「copilot 调度器在拦截并破坏其它 provider 的调度语义」。

### 2.5 影响面与严重度

- **非致命但全面**：宿主 `PickAuth` 在 invalid 时返回 `(zero, false, nil)`，上游 `pickViaPluginScheduler` 得 `handled=false` 后回落内建选择器，**请求仍可被服务**。因此表现为「警告刷屏 + 插件调度意图被完全忽略」，而非服务中断。
- **横向破坏**：copilot 的越界 ID 使**所有** provider 的插件级调度失效，全部退化为宿主内建选择器。
- **日志噪声**：高频告警污染生产日志，掩盖真实故障信号。

## 3. 取证过程（生产只读探测）

| 步骤 | 调用 | 结果 |
| --- | --- | --- |
| 1 | `docker logs cli-proxy-api --since 3h \| grep -c "unknown auth id"` | `1165` |
| 2 | `GET /v0/management/auth-files` | copilot 条目 `"id": "copilot-f13aadb583ef01ba.json"`（带扩展名） |
| 3 | 容器内 `cat /root/.antigravity_cockpit/copilot_accounts/copilot-*.json` | `"auth_id": "copilot-f13aadb583ef01ba"`（无扩展名） |
| 4 | `GET /v8/management/plugins` | 6 插件均 `enabled: true`；copilot-provider 排序居首 |
| 5 | `docker logs \| grep "21:22:4[5-9]"` | 告警同秒实际为 mixed provider 流量 |

## 4. 对照基线：qoder-ai 的正确实现

`qoder-ai/scheduler.go:81 handleSchedulerPick` 的正确语义：

1. 按 `c.Provider != providerName` 过滤候选，**只在宿主下发的候选集内挑选**；
2. `candidateDisabled(c)` 跳过宿主禁用账号；
3. 无候选时 `Handled: false`，注释明确「defer so host can fail over to OTHER providers」；
4. 返回的 `AuthID` 取自候选对象自身，天然命中宿主口径。

## 5. 修复方案

**采纳方案**：对齐 qoder-ai 语义，`handleSchedulerPick` 改为在宿主候选集内挑选。

### 5.1 改动点（`copilot/scheduler.go`）

- `handleSchedulerPick` 重写：按 `c.Provider == providerName` 过滤 → `candidateDisabled` 跳过禁用 → 冷却过滤（兼容 `copilot-<hash>` 与 `copilot-<hash>.json` 两种 key 形态）→ 无候选返回 `Handled: false` 让位内建调度 → 候选集内轮转并返回 `candidates[idx].ID`（**逐字取宿主口径**）。
- 新增 `candidateDisabled(c)`：读候选 `Status` 与 `Metadata["disabled"]` 判定宿主禁用态（对齐 qoder-ai 同名实现）。
- 保留 `pickActiveAuth` / `pickNextAuth` / `accountRoutable`：仍被 `executor.go:207 resolveTargetAuth` 与换号重试链路使用，不作为调度入口。

### 5.2 设计约束

- **绝不越界**：返回的 `AuthID` 只可能来自 `req.Candidates[].ID`，从结构上消除 `unknown auth id`。
- **不抢占其它 provider**：非本 provider 候选全部跳过，无候选即让位内建调度。
- **不做额外上游调用**：调度路径不引入新的账号探测或网络请求。

## 6. 验证

### 6.1 单元测试（`copilot/scheduler_test.go`）

- `TestHandleSchedulerPickReturnsCandidateIDVerbatim` — 返回的 `AuthID` 必须逐字等于候选 `ID`（含 `.json`），**本次 Bug 的直接回归判据**
- `TestHandleSchedulerPickSkipsForeignProvider` — 候选全为其它 provider 时 `Handled: false`
- `TestHandleSchedulerPickSkipsDisabled` — 禁用候选被跳过；全禁用时 `Handled: false`
- `TestHandleSchedulerPickSkipsCoolingDown` — 冷却候选被跳过
- `TestHandleSchedulerPickNoCandidates` — 空候选时 `Handled: false`
- `TestHandleSchedulerPickRoundRobinWithinCandidates` — 多候选轮转且始终落在候选集内

**哨兵反证**：将 `AuthID` 改回 `strings.TrimSuffix(candidates[idx].ID, ".json")` 后 `TestHandleSchedulerPickReturnsCandidateIDVerbatim` 精确 FAIL，恢复后全绿。

### 6.2 真实 .so ABI 端到端

单测直调 Go 函数，绕过了 cgo 入口与 JSON 编解码；而生产路径宿主与插件之间只走 JSON，字段名不匹配会静默失配。故补一层真实产物验证：

- 构建：WSL gcc `CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -o copilot.so .` → 产出 10,770,888 字节真实 `.so`
- 驱动：ctypes 构造 `cliproxy_host_api` 函数指针表 → `cliproxy_plugin_init` → `cliproxyPluginCall("scheduler.pick", ...)`，等价「把插件装进 CPA 宿主」的最小复刻
- 断言（5 组全 PASS）：① 单候选返回逐字 ID（含 `.json`）② 双候选 4 次轮转落在集内且两者均出现 ③ 外部 provider 候选让位 `Handled=false` ④ 禁用跳过 / 全禁用让位 ⑤ 空候选让位
- 覆盖增量：ABI 边界 + JSON 字段名契约（`SchedulerAuthCandidate.ID` / `SchedulerPickResponse.AuthID` 均无 json tag，走 Go 字段名）

### 6.3 门禁

- `python scripts/cgo-shim-build.py copilot` — build / vet / test 全绿
- `6-review` 风格回归（`STYLE: PASS`）
- `code-change-finalization-gate-rules` 收口

### 6.4 生产行为验收（0.1.2 部署后）

发布链：`0054c28`（fix）→ CI run `38751333875` success → assets `ba471b0` → registry `bca1264` → 资产清理 `78b4069`。生产 `plugin-store install?version=0.1.2` 返回 `restart_required:false`，热重载日志 `active_version=0.1.2 retired_version=0.1.1`；落盘 `.so` sha256 `d9dd3b40…540b3`（7812672 字节）与本地 release zip 内二进制逐字节一致。

**A/B 对照（同一生产实例，热重载时刻 `22:43:40`）**：

| 窗口 | 真实 `/v1` 请求数 | `unknown auth id` 告警数 | 告警占比 |
| --- | --- | --- | --- |
| 热重载前 5 分钟（`22:38:40`–`22:43:40`） | 50 | 50（全部 `plugin_id=copilot-provider`） | 100% |
| 热重载后（`22:43:40` 起） | 57+ | 0 | 0% |

**正向路径验证**：发起真实 copilot 请求 `POST /v1/chat/completions`（`model=gpt-4o`），`http=200`、2.04s、响应 `content=PONG`，usage tracker 记录 `provider=copilot-provider model=gpt-4o failed=false`。即插件返回的候选 ID 被宿主采纳、请求经 copilot 账号正常出网。

**替代解释排除**：零告警不能是「宿主不再调用插件调度器」导致——插件 `main.go:282` 声明 `Scheduler: true`，热重载日志含 `plugin loaded` + `plugin registered`，且 `unknown auth id` 的触发条件（[pluginhost/scheduler.go:83-86](file:///C:/Users/luode/go/pkg/mod/github.com/router-for-me/!c!l!i!proxy!a!p!i/v7@v7.2.129/internal/pluginhost/scheduler.go#L83-L86)）仅在「插件返回非空 AuthID 且不在候选集内」时命中；同窗口有真实流量且插件确被调用，故零告警等价于「返回 ID 全部命中候选集」。

## 7. 与用户原始症状的关系

本 Bug 与用户报告的「授权完成但弹窗未跳转 / 面板无账号」是**两个独立问题**：

| 问题 | 根因 | 状态 |
| --- | --- | --- |
| 弹窗停在「等待认证中…」/ 面板无账号 | 授权链路本身是通的（账号已落盘、宿主已保存）。前端轮询窗口约 56 秒（`20:34:58`–`20:35:54`，每 4 秒一次）短于用户实际完成 GitHub 授权的时间（`20:45:55` 落盘），用户在账号生成前 10 分钟就查看了面板 | 时序问题，非缺陷 |
| 生产日志 `unknown auth id` 刷屏 | 本 Bug（调度器越界返回自有账号 ID） | 已修复 |

## 8. 未覆盖 / 后续

- **多账号轮转未观察**：生产 copilot 当前仅 1 个账号（`copilot-f13aadb583ef01ba`），单候选场景下轮转逻辑退化为「总是返回唯一候选」，与修复前的行为差异只体现在 ID 口径。轮转语义（`schedulerIndex` 取模）已由单测 `TestHandleSchedulerPickRoundRobinWithinCandidates` 与真实 `.so` ABI 双候选用例覆盖，但生产多账号场景仍待第二账号接入后观察。
- **存量告警**：修复上线前产生的 1165 条历史告警不影响功能，仅在日志中留存。
