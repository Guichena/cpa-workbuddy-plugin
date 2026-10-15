package main

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// loginBridgeURL 构造设备码桥接页的相对 URL。
//
// 必须是相对路径：宿主 UI 从管理域 origin 用 window.open 打开它，相对路径在
// 本地与远程部署下都能解析到正确的服务地址（与 traework 桥接页同一约定）。
// [参数] state: 登录会话标识。
// [返回] 桥接页相对 URL。
// 最近修改时间：2026-10-16；改动原因：新增，宿主弹窗设备码不可见的绕过入口。
func loginBridgeURL(state string) string {
	return "/v0/resource/plugins/" + providerName + "/login-bridge?state=" + url.QueryEscape(state)
}

// handleLoginBridge 渲染宿主 OAuth 弹窗的设备码桥接页。
//
// 宿主 ServePluginAuthURL 只回 {status, url, state}，插件返回的
// AuthLoginStartResponse.Metadata.user_code 被写进宿主 session store 后不回传前端，
// 因此宿主通用弹窗拿不到设备码（内置 provider 把 user_code 放响应顶层才能显示）。
// 本页把设备码直接渲染进 HTML，而 handleStartLogin 返回的正是本页 URL，
// 宿主弹窗打开的即是本页，用户即可看到码并完成授权。
//
// 本页只做展示，不驱动轮询、不消费会话：授权结果轮询与凭据落盘由宿主
// get-auth-status → PollLogin 链路负责；若本页抢先调用 pollDeviceLogin，
// 会在成功时消费掉会话（该方法成功即 Delete），导致宿主拿不到凭据记录。
//
// [参数] state: 登录会话标识（resource 路由免鉴权 GET，state 即能力凭据）。
// [返回] 管理响应（HTML 页面）。
// 最近修改时间：2026-10-16；改动原因：新增，宿主弹窗设备码不可见的绕过方案。
func handleLoginBridge(state string) pluginapi.ManagementResponse {
	state = strings.TrimSpace(state)
	if state == "" {
		return loginBridgePage("", "", "缺少 state 参数：请从 CLIProxyAPI 管理页的「登录」入口重新发起。")
	}
	rawSession, ok := oauthSessions.Load(state)
	if !ok {
		return loginBridgePage("", "", "授权会话不存在或已完成（结果只保留一次，15 分钟有效）。请回到管理页重新点击「登录」。")
	}
	session, ok := rawSession.(*oauthLoginSession)
	if !ok || session == nil {
		return loginBridgePage("", "", "授权会话数据异常，请重新发起登录。")
	}
	if time.Since(session.CreatedAt) > githubDeviceCodeTTL {
		oauthSessions.Delete(state)
		return loginBridgePage("", "", "登录已超时（15 分钟）。请回到管理页重新点击「登录」。")
	}
	return loginBridgePage(session.UserCode, session.VerificationURI, "")
}

// loginBridgePage 渲染桥接页 HTML。detail 非空时为错误页，否则为设备码引导页。
//
// 页面用 WriteString 逐段拼接而非 Go 原生字符串：原生字符串内出现反引号会被
// 截断，逐段拼接可彻底规避该陷阱（与 traework 桥接页同款写法）。
// [参数] userCode: 设备码；verificationURI: GitHub 授权地址；detail: 错误说明。
// [返回] 管理响应（HTML）。
// 最近修改时间：2026-10-16；改动原因：新增，桥接页渲染。
func loginBridgePage(userCode, verificationURI, detail string) pluginapi.ManagementResponse {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<title>GitHub Copilot 登录</title>")
	b.WriteString("<style>")
	b.WriteString("body{font-family:-apple-system,'Segoe UI','Microsoft YaHei',sans-serif;background:#f5f6f8;color:#1f2328;margin:0;padding:40px 16px;display:flex;justify-content:center}")
	b.WriteString(".card{background:#fff;border-radius:12px;box-shadow:0 2px 12px rgba(0,0,0,.08);max-width:560px;width:100%;padding:32px}")
	b.WriteString("h1{font-size:20px;margin:0 0 12px}p{line-height:1.7;margin:8px 0;font-size:14px}")
	b.WriteString("ol{padding-left:20px}ol li{margin:8px 0;font-size:14px;line-height:1.6}")
	b.WriteString(".code{margin:16px 0;padding:18px;background:#0d1117;color:#f0f6fc;border-radius:10px;text-align:center;font-family:ui-monospace,Consolas,monospace;font-size:32px;letter-spacing:4px;font-weight:700;user-select:all}")
	b.WriteString("a.btn{display:inline-block;margin-top:8px;background:#2563eb;color:#fff;text-decoration:none;border-radius:8px;padding:10px 22px;font-size:14px}")
	b.WriteString("a.btn:hover{background:#1d4ed8}a{color:#2563eb}")
	b.WriteString(".err{background:#fef2f2;border:1px solid #fecaca;border-radius:8px;padding:10px 14px;margin-top:16px;font-size:14px;color:#991b1b}")
	b.WriteString(".note{background:#fff7e6;border:1px solid #ffd591;border-radius:8px;padding:10px 14px;margin-top:16px;font-size:13px;color:#874d00}")
	b.WriteString("code{background:#f0f1f3;border-radius:4px;padding:2px 6px;font-size:13px;word-break:break-all}")
	b.WriteString("</style></head><body><div class=\"card\">")

	if detail != "" {
		b.WriteString("<h1>无法完成登录</h1><div class=\"err\"><p>" + htmlEscape(detail) + "</p></div>")
	} else {
		b.WriteString("<h1>GitHub Copilot 设备授权</h1>")
		b.WriteString("<p>请按以下步骤完成 GitHub 授权，本窗口会在授权成功后自动完成账号绑定。</p>")
		b.WriteString("<ol>")
		b.WriteString("<li>点击下方按钮打开 GitHub 授权页</li>")
		b.WriteString("<li>把下面的<strong>设备码</strong>原样输入到授权页并确认</li>")
		b.WriteString("<li>授权完成后回到本页或管理页，稍候即会显示绑定成功</li>")
		b.WriteString("</ol>")
		b.WriteString("<div class=\"code\">" + htmlEscape(userCode) + "</div>")
		if verificationURI != "" {
			b.WriteString("<p><a class=\"btn\" href=\"" + htmlEscape(verificationURI) + "\" target=\"_blank\" rel=\"noopener noreferrer\">打开 GitHub 授权页</a></p>")
			b.WriteString("<p>授权地址：<code>" + htmlEscape(verificationURI) + "</code></p>")
		}
		b.WriteString("<div class=\"note\">此页面无需管理密钥，设备码仅用于本次登录（15 分钟内有效）。若提示会话过期，请回管理页重新发起。</div>")
	}
	b.WriteString("</div></body></html>")

	h := http.Header{}
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: h, Body: []byte(b.String())}
}

// htmlEscape 转义 HTML 关键字符，避免设备码或地址中的特殊字符破坏页面结构。
// [参数] s: 原始字符串。
// [返回] 转义后的字符串。
// 最近修改时间：2026-10-16；改动原因：新增，桥接页渲染所需。
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#39;")
	return r.Replace(s)
}
