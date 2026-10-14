package main

import (
	"net/http"
	"strings"
)

// Z.ai / ZCode 官方上游端点常量（依据 zcode.z.ai 线上实测 + ZCode 3.15.1 桌面端逆向）。
const (
	// zaiCodingPlanBaseURL 是 start-plan（免费版）账号的模型端点前缀。
	// 桌面端对 `planKind==="start-plan"` 直接使用 `zcodeJwtToken` 作为 apiKey 访问该前缀。
	// 中国站（bigmodel）start-plan 与国际站共用该端点（见 resolveUpstreamTargets 注释）。
	zaiCodingPlanBaseURL = "https://zcode.z.ai/api/v1/zcode-plan/anthropic"
	// zaiAPIAuthropicBaseURL 是付费 Coding Plan 账号的模型端点前缀
	// （官方 zcode-builtin.json 中 zai-api 模板：access.type=zhipu-coding-plan-api-key）。
	zaiAPIAuthropicBaseURL = "https://api.z.ai/api/anthropic"
)

// upstreamTarget 描述一次上游模型请求的端点与鉴权方式。
type upstreamTarget struct {
	// URL 是模型端点前缀（实际请求追加 /v1/messages 或 /v1/models）。
	URL string
	// Credential 是该端点对应的鉴权凭证。
	Credential string
	// Bearer 为 true 时以 `Authorization: Bearer` 发送凭证，否则使用 `x-api-key`。
	Bearer bool
}

// resolveUpstreamTargets 按账号类型返回候选上游目标，按优先级排序。
//
// 两条官方链路的差异（2026-10-12 实测）：
//   - start-plan（免费版）：以 `zcodeJwtToken` 走 `zcode.z.ai/api/v1/zcode-plan/anthropic`；
//   - 付费 Coding Plan：以业务令牌（zai access_token 换取）走 `api.z.ai/api/anthropic`。
//
// 中国站（bigmodel，2026-10-14 CYCLE-3 实测修正）：start-plan 形态的 bigmodel 账号与 zai
// **共用** `zcode.z.ai/api/v1/zcode-plan/anthropic`——同一 JWT 打该端点鉴权通过（无效 JWT 返回
// 401、有效 JWT 返回 405/3012 或 429/3009，证明鉴权在风控之前生效）；而 `open.bigmodel.cn`
// 对两类账号的 ZCode JWT 一律返回 401。asar 逆向佐证：`open.bigmodel.cn` 仅服务
// `bigmodelIndividualCodingPlan`（付费个人版），`bigmodelStartPlan` 不在该分支。
//
// 用户显式配置的 base_url 优先且不改写其鉴权方式，保证可手动接管。
// [参数] sa: 账号凭据；cfg: 当前插件配置。
// [返回] 候选目标列表，按优先级排序；调用方按顺序尝试，遇 401 切换下一个。
// 最近修改时间：2026-10-14；改动原因：移除 bigmodel 专用端点，start-plan 两站共用 zcode.z.ai。
func resolveUpstreamTargets(sa *StoredAuth, cfg Config) []upstreamTarget {
	if sa == nil {
		return nil
	}

	configuredBase := strings.TrimSpace(cfg.BaseURL)
	jwt := strings.TrimSpace(sa.Token)
	if jwt == "" {
		jwt = strings.TrimSpace(sa.APIKey)
	}
	accessToken := strings.TrimSpace(sa.AccessToken)
	staticKey := strings.TrimSpace(sa.APIKey)

	// 用户显式配置了非默认 base_url 时，尊重配置，仅用单一目标。
	if configuredBase != "" && configuredBase != defaultBaseURL {
		credential := staticKey
		if credential == "" {
			credential = jwt
		}
		return []upstreamTarget{{URL: configuredBase, Credential: credential}}
	}

	var targets []upstreamTarget
	// 1. start-plan 链路：OAuth JWT 直接作为 Bearer 凭证访问 coding-plan 端点。
	//    中国站与国际站共用同一端点（见函数注释）。
	if strings.TrimSpace(sa.Token) != "" {
		targets = append(targets, upstreamTarget{
			URL:        zaiCodingPlanBaseURL,
			Credential: strings.TrimSpace(sa.Token),
			Bearer:     true,
		})
	}
	// 2. 付费 Coding Plan 链路：先换业务令牌，再以 x-api-key 访问 api.z.ai。
	if accessToken != "" {
		if businessToken, errResolve := resolveBusinessToken(accessToken, providerFamilyOf(sa)); errResolve == nil && businessToken != "" {
			targets = append(targets, upstreamTarget{
				URL:        zaiAPIAuthropicBaseURL,
				Credential: businessToken,
			})
		}
	}
	// 3. 兜底：仅有静态 API Key 的账号沿用默认端点。
	if len(targets) == 0 && staticKey != "" {
		targets = append(targets, upstreamTarget{URL: defaultBaseURL, Credential: staticKey})
	}
	return targets
}

// applyTargetAuth 将目标凭证写入请求头。
// [参数] h: 请求头；target: 目标定义。
// [返回] 无。
func applyTargetAuth(h http.Header, target upstreamTarget) {
	if target.Bearer {
		h.Set("Authorization", "Bearer "+target.Credential)
		h.Del("x-api-key")
		return
	}
	h.Set("x-api-key", target.Credential)
	h.Del("Authorization")
}
