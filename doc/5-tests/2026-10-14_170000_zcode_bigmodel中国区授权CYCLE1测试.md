# zcode-provider bigmodel 中国区授权（CYCLE-1）测试记录

- 对应需求：`doc/2-需求/2026-10-14_153951_zcode支持bigmodel中国区授权需求.md`
- 对应实施：`doc/3-实施/2026-10-14_zcode支持bigmodel中国区授权实施总览.md`
- 测试时间：2026-10-14
- 测试范围：CYCLE-1（TASK-001~004），provider 参数化骨架与端点分支

## 一、测试环境

- 构建入口：`python scripts/cgo-shim-build.py zcode`（CGO_ENABLED=0 shim，覆盖 build + vet + test）
- Windows 本地 Go：go1.27.0（本轮由 winget 从 1.24.5 升级，满足 go.mod 声明的 `go >= 1.26.0`）
- 测试镜像目录：根 `test/zcode/`（当前仅 `panel_quota_render_repro.mjs`，本轮新增用例为 Go 单测，随源码目录直接编译）

## 二、测试结果

| 项 | 命令 | 结果 |
| --- | --- | --- |
| 构建 | `go build ./...` | OK |
| 静态检查 | `go vet ./...` | OK |
| 单元测试 | `go test ./...` | OK（zcode 2.924s / internal/mimic 0.460s） |
| 格式 | `gofmt -l`（LF 归一后） | 空（合规） |
| 测试污染 | 生产代码 grep `func Test` / `testing.` | 空（POLLUTION: PASS） |

## 三、新增测试用例

### provider 归属与参数化（`zcode/provider_family_test.go`）

| 用例 | 断言 |
| --- | --- |
| `TestNormalizeProviderFamily` | 仅 `bigmodel` 单独成族；空值/大小写变体/非法值回退 `zai` |
| `TestIsValidOAuthProvider` | 只接受 `zai` / `bigmodel`；`zai-cn`/`cn`/`zhipu` 等被拒 |
| `TestStartAccountOAuth_RejectsUnknownProvider` | 非法 provider 在插件侧拦截，错误含「不支持的 OAuth provider」 |
| `TestStartAccountOAuth_BigmodelProviderInjected` | bigmodel 原样透传 CLI init 的 provider 字段，会话记录归属 |
| `TestProviderFromMetadata` | 宿主 Metadata 是 provider 传递通道；nil/非字符串回退 `zai` |
| `TestSaveZaiAccount_PersistsProviderFamily` | 归属字段真实落盘（回读磁盘校验，非仅内存） |
| `TestProviderFamilyOf_LegacyDefault` | 历史账号（无该字段）按 `zai` 处理 |
| `TestHandleMethod_StartLoginCarriesProvider` | 宿主入口经 Metadata 指定 bigmodel 时返回中国站授权地址 |

### 端点分支（`zcode/upstream_target_test.go`）

| 用例 | 断言 |
| --- | --- |
| `TestResolveUpstreamTargets_ByProviderFamily` | 中国站 → `open.bigmodel.cn`；国际站 → `zcode.z.ai`；凭据与 Bearer 语义不变 |
| `TestResolveUpstreamTargets_LegacyDefaultsToZai` | 历史账号端点保持 zai，零回归 |
| `TestBusinessLoginEndpointFor` | 业务令牌端点按 family 分支（`api.z.ai` vs `bigmodel.cn`） |
| `TestResolveBusinessToken_CacheIsolatedByFamily` | 缓存键纳入 family，同 token 跨站不互相命中 |
| `TestModelCredentialFor_BigmodelUsesFamilyEndpoint` | bigmodel 账号换取业务令牌走中国站端点 |
| `TestBusinessTokenTTLGuard` | 守住缓存时长常量语义 |

## 四、判别力反证（哨兵变异）

新测试必须证明真实进编译且具备判别力，采用两次变异反证：

| 变异 | 注入内容 | 结果 |
| --- | --- | --- |
| 哨兵 1 | `bigmodelCodingPlanBaseURL` 改回 zai 端点 | **首轮未 FAIL**，暴露断言盲区 |
| — | 修正：断言从「等于常量」改为「包含字面量主机」 | 二次注入后 `TestResolveUpstreamTargets_ByProviderFamily` 精确 FAIL（`got [{URL:https://zcode.z.ai/...}]`） |
| 哨兵 2 | `normalizeProviderFamily` 恒返回 `zai` | 6 个用例 FAIL（`TestNormalizeProviderFamily` / `..._BigmodelProviderInjected` / `TestProviderFromMetadata` / `..._PersistsProviderFamily` / `TestResolveUpstreamTargets_ByProviderFamily` / `TestBusinessLoginEndpointFor`） |

**关键发现**：哨兵 1 首轮未失败，说明原断言（`targets[0].URL != bigmodelCodingPlanBaseURL`）在两个常量被改成同值时退化为恒真。已改为字面量主机断言（`strings.Contains(url, "open.bigmodel.cn")`），修复后判别力恢复。

## 五、AC 覆盖情况

| AC | 状态 | 说明 |
| --- | --- | --- |
| AC-001 面板可选 bigmodel 发起授权 | 部分 | 后端链路已通；面板 UI 属 CYCLE-2 |
| AC-002 bigmodel 授权端到端可用 | 未覆盖 | 需真实 bigmodel 账号（GAP-004/005） |
| AC-003 zai 既有行为零回归 | **达成** | 现有测试全绿 + legacy 默认值用例 |
| AC-004 卡片区分归属 | 未覆盖 | 属 CYCLE-2 |
| AC-005 cgo-shim 全绿 | **达成** | 见第二节 |
| AC-006 未知 provider 明确报错 | **达成** | `TestStartAccountOAuth_RejectsUnknownProvider` |

## 六、未覆盖与说明

- CYCLE-1 为纯逻辑骨架，未触及面板 UI 与真实上游调用；`AC-002`/`AC-004` 需后续周期。
- `quota.go` 额度端点**未做分支**：实测 `bigmodel.cn/api/v1/zcode-plan/billing/current` 返回 404，而 `zcode.z.ai` 同路径返回 401（端点存在），证明 start-plan 额度端点两家共用 `zcode.z.ai`，故无需分支。此结论已回写需求文档 GAP-002。
