package main

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
)

// GitHub Copilot 客户端指纹常量。
//
// 关键事实（来源：ericc-ch/copilot-api master 分支源码，2026-10-15 调研）：
// Copilot 上游对请求头做客户端指纹校验，缺少或取值异常会拒绝服务。这些取值
// 复刻 VS Code Copilot Chat 扩展，插件侧集中在此文件，便于随 VS Code 版本单点更新。
const (
	// defaultEditorVersion 是模拟的 VS Code 版本（editor-version 头）。
	defaultEditorVersion = "vscode/1.104.3"
	// defaultPluginVersion 是模拟的 Copilot Chat 扩展版本（editor-plugin-version 头）。
	defaultPluginVersion = "copilot-chat/0.26.7"
	// defaultUserAgent 是模拟的客户端 UA。
	defaultUserAgent = "GitHubCopilotChat/0.26.7"
	// defaultAccountType 是缺省账号类型，决定上游主机名。
	defaultAccountType = "individual"
	// copilotIntegrationID 标识调用方集成形态，上游据此路由与计量。
	copilotIntegrationID = "vscode-chat"
	// copilotIntent 标识请求意图为对话面板。
	copilotIntent = "conversation-panel"
	// githubAPIVersion 是 GitHub REST API 版本头取值。
	githubAPIVersion = "2025-04-01"
	// vscodeUserAgentLibrary 标识 VS Code 扩展使用的 HTTP 库。
	vscodeUserAgentLibrary = "electron-fetch"
)

// normalizeAccountType 归一化账号类型取值，非法值与空值回退 individual。
// [参数] raw: 原始账号类型取值。
// [返回] 归一化后的账号类型。
// 最近修改时间：2026-10-15；改动原因：新增，上游主机名按账号类型分支所需。
func normalizeAccountType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "business":
		return "business"
	case "enterprise":
		return "enterprise"
	default:
		return defaultAccountType
	}
}

// resolveCopilotBaseURL 解析账号的 Copilot 推理端点。
//
// 优先级：配置显式 base_url > 令牌交换返回的 endpoints.api（上游权威值）
// > 按账号类型推导（individual 走 api.githubcopilot.com，business/enterprise
// 走 api.<accountType>.githubcopilot.com）。
// 之所以让 endpoints.api 优先：实测免费账号返回 api.individual.githubcopilot.com，
// 与按账号类型硬推导的 api.githubcopilot.com 不同，硬推导会在上游调整时失准。
// endpoints.api 属外部 API 返回值且该 URL 会承载 Bearer 令牌，故只接受
// githubcopilot.com 域下的 https 地址；不匹配时回退按类型推导，避免异常响应把凭据带离域。
// [参数] sa: 目标账号凭据；cfg: 当前插件配置。
// [返回] 推理端点基础 URL（不含 /v1 前缀）。
// 最近修改时间：2026-10-15；改动原因：消费令牌响应的 endpoints.api，关闭 GAP-002。
func resolveCopilotBaseURL(sa *StoredAuth, cfg Config) string {
	if base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"); base != "" && base != defaultBaseURL {
		return base
	}
	if sa != nil {
		if ep := strings.TrimRight(strings.TrimSpace(sa.APIEndpoint), "/"); ep != "" && isCopilotEndpoint(ep) {
			return ep
		}
	}
	accountType := normalizeAccountType(cfg.AccountType)
	if sa != nil && strings.TrimSpace(sa.AccountType) != "" {
		accountType = normalizeAccountType(sa.AccountType)
	}
	if accountType == defaultAccountType {
		return defaultBaseURL
	}
	return "https://api." + accountType + ".githubcopilot.com"
}

// isCopilotEndpoint 校验端点是否为 githubcopilot.com 域下的 https 地址。
// [参数] raw: 待校验端点。
// [返回] 合法返回 true。
func isCopilotEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "githubcopilot.com" || strings.HasSuffix(host, ".githubcopilot.com")
}

// copilotTokenOf 返回账号用于推理请求的 Copilot 内部令牌。
//
// Copilot 推理端点的 Authorization 只认 Copilot 内部令牌（Bearer），
// 不认 GitHub token；账号缺 Copilot 令牌时由 ensureCopilotToken 先行换取。
// [参数] sa: 目标账号凭据。
// [返回] Copilot 内部令牌；缺失时返回空串。
// 最近修改时间：2026-10-15；改动原因：新增，统一推理鉴权取值口径。
func copilotTokenOf(sa *StoredAuth) string {
	if sa == nil {
		return ""
	}
	if t := strings.TrimSpace(sa.CopilotToken); t != "" {
		return t
	}
	return strings.TrimSpace(sa.APIKey)
}

// applyCopilotHeaders 向请求注入 Copilot 客户端指纹头。
//
// 1. 鉴权头使用 Copilot 内部令牌（Bearer），不使用 GitHub token；
// 2. 集成标识、编辑器版本、插件版本、UA 等复刻 VS Code Copilot Chat；
// 3. X-Initiator 依消息角色判定：含 assistant / tool 消息视为 agent 续跑，否则为 user。
// [参数] h: 待注入的请求头；sa: 目标账号凭据；cfg: 当前插件配置；payload: 请求体（用于判定 X-Initiator）。
// [返回] 无。
// 最近修改时间：2026-10-15；改动原因：新增，Copilot 上游指纹头集中注入。
func applyCopilotHeaders(h http.Header, sa *StoredAuth, cfg Config, payload []byte) {
	token := copilotTokenOf(sa)
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("Copilot-Integration-Id", copilotIntegrationID)
	h.Set("Openai-Intent", copilotIntent)
	h.Set("X-GitHub-Api-Version", githubAPIVersion)
	h.Set("X-Vscode-User-Agent-Library-Version", vscodeUserAgentLibrary)
	h.Set("X-Request-Id", randomUUID())

	editorVersion := strings.TrimSpace(cfg.EditorVersion)
	if editorVersion == "" {
		editorVersion = defaultEditorVersion
	}
	h.Set("Editor-Version", editorVersion)

	pluginVersion := strings.TrimSpace(cfg.PluginVersion)
	if pluginVersion == "" {
		pluginVersion = defaultPluginVersion
	}
	h.Set("Editor-Plugin-Version", pluginVersion)

	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		ua = defaultUserAgent
	}
	h.Set("User-Agent", ua)

	h.Set("X-Initiator", resolveInitiator(payload))
}

// resolveInitiator 依请求消息角色判定 X-Initiator 取值。
//
// 上游用该头区分「用户新发起」与「agent 续跑」：请求中出现 assistant 或 tool
// 角色的消息即视为多轮续跑，取 agent；否则取 user。解析失败按 user 兜底。
// [参数] payload: 原始请求体 JSON。
// [返回] "agent" 或 "user"。
// 最近修改时间：2026-10-15；改动原因：新增，复刻上游 X-Initiator 判定语义。
func resolveInitiator(payload []byte) string {
	if len(payload) == 0 {
		return "user"
	}
	messages := gjson.GetBytes(payload, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return "user"
	}
	for _, msg := range messages.Array() {
		switch strings.ToLower(strings.TrimSpace(msg.Get("role").String())) {
		case "assistant", "tool":
			return "agent"
		}
	}
	return "user"
}

// applyGitHubHeaders 向 GitHub API 请求注入鉴权与版本头。
//
// GitHub REST API 要求显式版本头；鉴权用 `token <githubToken>` 形式
// （非 Bearer），这是 GitHub 对 OAuth access token 的传统约定。
// [参数] h: 待注入的请求头；githubToken: GitHub access token；cfg: 当前插件配置。
// [返回] 无。
// 最近修改时间：2026-10-15；改动原因：新增，GitHub API 侧请求头集中构造。
func applyGitHubHeaders(h http.Header, githubToken string, cfg Config) {
	h.Set("Authorization", "token "+strings.TrimSpace(githubToken))
	h.Set("Accept", "application/json")
	h.Set("X-GitHub-Api-Version", githubAPIVersion)
	editorVersion := strings.TrimSpace(cfg.EditorVersion)
	if editorVersion == "" {
		editorVersion = defaultEditorVersion
	}
	h.Set("Editor-Version", editorVersion)
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		ua = defaultUserAgent
	}
	h.Set("User-Agent", ua)
}
