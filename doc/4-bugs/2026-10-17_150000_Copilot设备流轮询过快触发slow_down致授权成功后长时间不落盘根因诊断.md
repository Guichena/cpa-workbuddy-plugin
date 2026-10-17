# Copilot 设备流轮询过快触发 slow_down 致授权成功后长时间不落盘根因诊断

- Bug ID：BUG-COPILOT-DEVICEFLOW-SLOWDOWN-20261017
- 报告时间：2026-10-17 15:00:00（北京时间）
- 插件：`copilot-provider`（生产 0.1.5）
- 环境：生产 `https://cpa.luode.vip`（服务器 45.207.222.65:18998，容器 `cli-proxy-api`）
- 严重级别：中（登录可用但用户感知为「卡住」，易被误判为功能失效）

## 一、现象

用户在管理台「OAuth 登录」点击 `copilot-provider` 的「+」，弹窗打开插件桥接页并展示设备码 `3374-FB08`。用户在 GitHub 完成设备授权，GitHub 页面显示 `Congratulations, you're all set! Your device is now connected.`，但：

- 管理台弹窗底部长期停留在「等待认证中...」；
- 设备码页提示「授权完成后回到本页或管理页，稍候即会显示绑定成功」，实际长时间无变化；
- 用户判定为「回调不会触发，导致卡在授权页」。

## 二、取证（生产实测）

### 2.1 宿主轮询确实在发生

按 state 精确检索生产日志（`docker logs cli-proxy-api`）：

```
[2026-10-17 14:46:12] GET /v8/management/oauth/auth-url?provider=copilot-provider      200
[2026-10-17 14:46:12] GET /v0/resource/plugins/copilot-provider/login-bridge?state=d5cadb2b...  200
[2026-10-17 14:46:16] GET /v8/management/oauth/status?state=d5cadb2b...  200  360ms
[2026-10-17 14:46:20] GET /v8/management/oauth/status?state=d5cadb2b...  200  403ms
[2026-10-17 14:46:24] ...（每约 4 秒一次，共 19 次）
[2026-10-17 14:47:21] GET /v8/management/oauth/status?state=d5cadb2b...  200
```

结论：**回调链路没有断**。宿主前端以约 3 秒间隔持续轮询 `/v8/management/oauth/status`，每次耗时 330~400ms（即每次都在真实访问 GitHub 令牌端点）。

### 2.2 账号最终确实落盘

```
-rw------- 1 root root 1566 Oct 17 14:54 copilot-268266f8b9ca6869.json
```

文件内容关键字段：`expires_at=2026-10-18T14:54:27+08:00`、`api_endpoint=https://api.individual.githubcopilot.com`、`models` 10 项、`disabled=false`。

结论：授权结果最终被写入文件，**但从首次轮询（14:46:16）到落盘（14:54:28）间隔约 8 分 12 秒**，远超用户预期。

### 2.3 上游轮询间隔契约实测

在生产服务器上直接对 GitHub 设备流做对照实验：

```
interval_from_github = 5
--- 3s 间隔轮询 6 次（模拟宿主前端 3s） ---
0 {'error': 'authorization_pending'}
1 {'error': 'slow_down', 'interval': 10}
2 {'error': 'slow_down', 'interval': 15}
3 {'error': 'slow_down', 'interval': 20}
4 {'error': 'slow_down', 'interval': 25}
5 {'error': 'slow_down', 'interval': 30}

--- 严格按 interval(5s) 轮询 4 次 ---
0 {'error': 'authorization_pending'}
1 {'error': 'authorization_pending'}
2 {'error': 'authorization_pending'}
3 {'error': 'authorization_pending'}

--- slow_down 阶梯形成后，停 35 秒再单次轮询 ---
after35s {'error': 'authorization_pending'}
```

结论：GitHub 令牌端点要求轮询间隔不小于 `interval`（本例 5 秒）；快于该值返回 `slow_down` 并逐次抬升要求间隔（10→15→20→25→30 秒）。阶梯形成后需静默一段时间才恢复。

## 三、根因

`copilot/oauth.go` 的 `pollDeviceLogin` **完全忽略轮询间隔**：每次宿主轮询都无条件请求一次 GitHub 令牌端点。

- 宿主前端固定 3 秒轮询（生产前端 bundle 中轮询常量 `3e3`）；
- 插件把每次轮询原样转发上游；
- 3 秒 < GitHub 要求的 5 秒 → 第二次起持续返回 `slow_down`，间隔阶梯一路抬到 30 秒以上；
- 阶梯期内即使 `slow_down` 响应里已给出「下一次要等 N 秒」，插件也未采纳（`Interval` 字段在 `startDeviceLogin` 写入后从未被读取使用，`githubSlowDownStepSec` 常量也从未被引用），于是节奏继续被 3 秒轮询踩住；
- 用户授权完成后，仍需等待阶梯回落才可能拿到 token，表现为「授权成功但一直卡住」。

同时存在一个次级缺陷：同一 state 可能被宿主与插件面板同时轮询，原实现无会话级互斥，存在重复消费设备码的窗口。

## 四、修复方案（已实施）

`copilot/oauth.go`：

1. `oauthLoginSession` 新增 `mu sync.Mutex` 与 `nextPollAt time.Time`；新增方法 `applySlowDown` 采纳上游给出的间隔。
2. `pollDeviceLogin` 在会话级互斥内执行；未到 `nextPollAt` 时直接返回 `pending`，**不访问上游**；放行时才把 `nextPollAt` 推进一个 `Interval`。
3. 收到 `slow_down` 时调用 `applySlowDown`：上游给了 `interval` 就用它，未给则按 `githubSlowDownStepSec` 递增；并立即把 `nextPollAt` 推后到新间隔之后。
4. 首次轮询不节流（`nextPollAt` 零值），符合「相邻两次轮询之间才受 interval 约束」的语义。

`copilot/main_test.go`：新增 `expirePollWindow` 辅助、`TestPollDeviceLoginThrottlesWithinInterval`、`TestPollDeviceLoginAdoptsSlowDownInterval`；原 `TestDeviceCodeFlowStartAndPoll` 在两次轮询之间调用 `expirePollWindow`。

## 五、验证证据

- `python scripts/cgo-shim-build.py copilot`：build / vet / test 全绿。
- 哨兵变异 2/2 精确命中：
  - 移除节流判断 → `TestPollDeviceLoginThrottlesWithinInterval` FAIL（`upstream calls=3, want 0 within interval window`）；
  - 移除 `slow_down` 间隔采纳 → `TestPollDeviceLoginAdoptsSlowDownInterval` FAIL（`interval=5s, want 30s`）。
- `gofmt -l`：干净（LF 归一后）。

## 六、结论与边界

- 本 Bug 属**插件实现问题**（未遵守上游设备流轮询契约），非宿主缺陷、非 GitHub 回调失效。
- 修复后宿主仍以 3 秒轮询，但插件在间隔窗口内直接返回 `pending`，上游实际请求频率被限制在 `interval` 以上，`slow_down` 阶梯不再形成。
- 未覆盖：宿主前端轮询常量（3 秒）由宿主管理台 bundle 决定，插件无法修改；如需更快的感知速度，只能由宿主侧调整，不在本插件范围。
