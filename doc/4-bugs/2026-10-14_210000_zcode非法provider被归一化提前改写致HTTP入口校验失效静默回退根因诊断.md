# zcode-provider 非法 provider 被归一化提前改写致 HTTP 入口校验失效静默回退根因诊断

## 一、问题描述

- **现象**：CYCLE-3 生产前置验证中，向 `POST /v0/management/plugins/zcode-provider/activate-start` 传入非法 `provider`（如 `zhipu`），接口未按契约报错，而是**静默回退为 `zai`** 并返回了 `chat.z.ai` 的授权地址。
- **发现方式**：2026-10-14 生产直连探测（0.1.25 已热重载后）。
- **结论（本诊断）**：**不是上游问题、也不是面板问题**。根因是 HTTP 入口在调用核心逻辑前先做了 `normalizeProviderFamily()` 归一化，非法值被提前改写成 `zai`，导致下游 `isValidOAuthProvider()` 永远拿不到原始非法值，校验形同虚设。

## 二、证据链（生产实测）

### 2.1 三臂对照（0.1.25 生产直连）

| 请求体 | 期望 | 实测 |
|---|---|---|
| `{"provider":"bigmodel"}` | 返回 `bigmodel.cn` 授权地址 | ✅ `authorize_url` 主机 = `bigmodel.cn`，含 `appId=zcode` |
| `{"provider":"zai"}` | 返回 `chat.z.ai` 授权地址 | ✅ `authorize_url` 主机 = `chat.z.ai`（零回归） |
| `{"provider":"zhipu"}` | **报错**（AC-006） | ❌ **HTTP 200 + `chat.z.ai` 授权地址**（静默回退） |

### 2.2 代码定位

`zcode/management.go`（修复前）：

```go
provider := normalizeProviderFamily(gjson.GetBytes(body, "provider").String())
...
initResp, state, err := startAccountOAuth(authID, label, provider)
```

`zcode/oauth.go`（修复前）：

```go
func startAccountOAuth(targetAuthID, label, provider string) (*zaiCLIInitResponse, string, error) {
	family := normalizeProviderFamily(provider)
	if !isValidOAuthProvider(provider) {   // ← 收到的已是 "zai"，永不触发
		return nil, "", fmt.Errorf("不支持的 OAuth provider: %q（仅支持 zai / bigmodel）", provider)
	}
```

**执行时序缺陷**：入口 `normalizeProviderFamily("zhipu")` → `"zai"` → 传入 `startAccountOAuth` → `isValidOAuthProvider("zai")` = true → 校验通过 → 以 `zai` 发起授权。

### 2.3 同类第二入口

`zcode/oauth.go` 的 `providerFromMetadata()` 同样在返回前做了归一化：

```go
if raw, ok := metadata["provider"].(string); ok {
	return normalizeProviderFamily(raw)   // ← 宿主入口同样提前改写
}
```

宿主 `AuthLoginStart` 入口经 `handleStartLogin` 调用该函数，非法值同样被静默回退。

### 2.4 单元测试为何没拦住

CYCLE-1 的 `TestStartAccountOAuth_RejectsUnknownProvider` 直接调用 `startAccountOAuth("", "", "zai-cn")`，**绕过了入口层归一化**，因此函数层校验通过测试，而 HTTP 入口层的缺陷未被覆盖。

## 三、根因

**校验点与归一化点顺序颠倒**：`normalizeProviderFamily` 的语义是「空值与非法值均回退 `zai`」，用于**存量数据读取**的向后兼容；但被误用为**入口入参解析**，使非法值在校验前就丢失了原始形态。

## 四、修复方案

引入统一入口解析函数 `resolveOAuthProvider(raw)`，**先校验、后归一化**：

```go
func resolveOAuthProvider(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return providerFamilyZai, nil          // 未带 provider → 默认 zai（兼容）
	}
	if !isValidOAuthProvider(trimmed) {
		return "", fmt.Errorf("不支持的 OAuth provider: %q（仅支持 zai / bigmodel）", raw)
	}
	return normalizeProviderFamily(trimmed), nil
}
```

三处落点同步调整：

| 落点 | 修复前 | 修复后 |
|---|---|---|
| `handleActivateStart`（面板入口） | 先归一化再传参 | 调 `resolveOAuthProvider`，非法值返回 **400** |
| `providerFromMetadata`（宿主入口） | 返回归一化值 | **原样透传**原始取值（空/非字符串仍回退 `zai`） |
| `startAccountOAuth`（核心逻辑） | 先归一化再校验 | 调 `resolveOAuthProvider` 统一校验 |

`normalizeProviderFamily` 保留原语义，仅服务 `providerFamilyOf`（存量账号读取）与落盘写入。

## 五、影响面

- **用户路径不可达**：面板 `#oauthProvider` 下拉只有 `zai` / `bigmodel` 两个选项，正常操作不会产生非法值。
- **契约不一致**：直接调管理 API 的集成方会拿到静默回退结果而非明确错误，违反 AC-006「未知 provider 值返回明确错误，不静默回退」。
- **风险等级**：低（无数据损坏、无安全影响），但属真实契约缺陷，按用户决策前向修复。

## 六、验证

- `python scripts/cgo-shim-build.py zcode`：build / vet / test 全绿。
- 新增用例：`TestResolveOAuthProvider`（含 4 个非法值）、`TestHandleActivateStart_RejectsUnknownProvider`（HTTP 层 400 + 不触达上游）、`TestHandleActivateStart_AcceptsBigmodelProvider`（合法路径不回归）；`TestProviderFromMetadata` 增补非法值透传断言。
- **哨兵反证**：临时移除 `resolveOAuthProvider` 中的校验分支后，3 个用例精确 FAIL（含 `TestHandleActivateStart_RejectsUnknownProvider` 报「非法值穿透到上游 mock」），恢复后全绿。
- 面板回归脚本 `node test/zcode/panel_quota_render_repro.mjs`：PASS。

## 七、发布闭环

- 版本 `0.1.26`，提交链见 CHANGELOG 与 git log。
- 生产 `plugin-store install` 热重载验证。
