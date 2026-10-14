package main

import (
	"net/http"
	"strings"
)

// Z.ai / ZCode 官方上游端点常量（依据 zcode.z.ai 线上实测 + ZCode 3.15.1 桌面端逆向）。
const (
	// zaiCodingPlanBaseURL 是 start-plan（免费版）账号的模型端点前缀。
	// 桌面端对 `planKind==="start-plan"` 直接使用 `zcodeJwtToken` 作为 apiKey 访问该前缀。
	zaiCodingPlanBaseURL = "https://zcode.z.ai/api/v1/zcode-plan/anthropic"
	// zaiAPIAuthropicBaseURL 是付费 Coding Plan 账号的模型端点前缀
	// （官方 zcode-builtin.json 中 zai-api 模板：access.type=zhipu-coding-plan-api-key）。
	zaiAPIAuthropicBaseURL = "https://api.z.ai/api/anthropic"
	// bigmodelCodingPlanBaseURL 是中国站（bigmodel）账号的模型端点前缀
	// （2026-10-14 实测存活，返回 401 鉴权拒绝；客户端 asar 中 family=bigmodel 使用该地址）。
	bigmodelCodingPlanBaseURL = "https://open.bigmodel.cn/api/anthropic"
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
// 中国站（bigmodel，2026-10-14 实测）复用同一条 start-plan 链路，但模型端点换为
// `open.bigmodel.cn/api/anthropic`，业务令牌换取端点同步切换（见 business_token.go）。
//
// 用户显式配置的 base_url 优先且不改写其鉴权方式，保证可手动接管。
// [参数] sa: 账号凭据；cfg: 当前插件配置。
// [返回] 候选目标列表，按优先级排序；调用方按顺序尝试，遇 401 切换下一个。
// 最近修改时间：2026-10-14；改动原因：按 provider family 分支中国站模型端点。
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

	// 1. 按站点归属选择模型端点前缀：中国站走 open.bigmodel.cn，国际站走 zcode.z.ai。
	codingPlanBase := zaiCodingPlanBaseURL
	if providerFamilyOf(sa) == providerFamilyBigmodel {
		codingPlanBase = bigmodelCodingPlanBaseURL
	}

	var targets []upstreamTarget
	// 2. start-plan 链路：OAuth JWT 直接作为 Bearer 凭证访问 coding-plan 端点。
	if strings.TrimSpace(sa.Token) != "" {
		targets = append(targets, upstreamTarget{
			URL:        codingPlanBase,
			Credential: strings.TrimSpace(sa.Token),
			Bearer:     true,
		})
	}
	// 3. 付费 Coding Plan 链路：先换业务令牌，再以 x-api-key 访问 api.z.ai。
	if accessToken != "" {
		if businessToken, errResolve := resolveBusinessToken(accessToken, providerFamilyOf(sa)); errResolve == nil && businessToken != "" {
			targets = append(targets, upstreamTarget{
				URL:        zaiAPIAuthropicBaseURL,
				Credential: businessToken,
			})
		}
	}
	// 4. 兜底：仅有静态 API Key 的账号沿用默认端点。
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
