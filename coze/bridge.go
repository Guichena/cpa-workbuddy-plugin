package main

import (
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// 本文件渲染授权引导页，并处理脚本回传的令牌。
//
// 页面承担三件事：解释为什么需要手动一步、给出可直接复制的一次性脚本、
// 接收脚本跳转回来的令牌。提交走 resource 路由（免管理密钥）——
// 这是浏览器顶层打开的普通页面，拿不到也不该要求用户填管理密钥
// （与 copilot / doubao 桥接页同一做法）。

// handleLoginBridge 渲染授权引导页。
// [参数] state: 授权会话标识。
// [返回] 管理响应（HTML）。
// 最近修改时间：2026-10-18；改动原因：新增，引导页出口。
func handleLoginBridge(state string) pluginapi.ManagementResponse {
	state = strings.TrimSpace(state)
	if state == "" {
		return loginBridgePage("", "缺少 state 参数：请回到管理页重新点击「添加账号」。")
	}
	if !validLoginState(state) {
		return loginBridgePage("", "授权会话不存在或已过期（15 分钟有效）。请回到管理页重新发起。")
	}
	return loginBridgePage(state, "")
}

// callbackTokenHeader 是回调页同源回传令牌时使用的请求头。
//
// 令牌不走 query：resource 路由的访问日志会记录完整 URL，令牌会因此落进
// 宿主日志。改走「fragment（浏览器不发给服务端）+ 同源请求头」后，
// 服务端日志里只剩 state。
const callbackTokenHeader = "X-Coze-Token"

// callbackErrorHeader 是回调页同源上报失败原因时使用的请求头。
const callbackErrorHeader = "X-Coze-Error"

// handleLoginCallback 处理脚本回传的令牌。
//
// 脚本在扣子页面取到 x-tt-token 后跳转到本路径，令牌放在 URL fragment 里。
// 首次打开只返回接收页（服务端看不到 fragment），由页面脚本把令牌经
// 同源请求头回传，第二次请求才真正换 Cookie + 落盘。
//
// 安全边界：state 一次性且 15 分钟过期，等于一次性的短时凭证；
// 令牌本身只能兑换一次，且立即消费，不会留在插件内存里。
// [参数] req: 宿主透传的请求（state 在 query，令牌在请求头）。
// [返回] 管理响应（HTML 接收页或 JSON 结果）。
// 最近修改时间：2026-10-18；改动原因：令牌改走 fragment + 请求头，避免落进访问日志。
func handleLoginCallback(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	state := strings.TrimSpace(req.Query.Get("state"))
	if state == "" {
		return loginCallbackShell("", "缺少 state 参数，请重新执行脚本。")
	}
	if !validLoginState(state) {
		return loginCallbackShell("", "授权会话不存在或已过期（15 分钟有效）。请回到管理页重新发起。")
	}

	token := strings.TrimSpace(req.Headers.Get(callbackTokenHeader))
	upstreamErr := strings.TrimSpace(req.Headers.Get(callbackErrorHeader))

	// 首次打开：服务端拿不到 fragment，先返回接收页。
	if token == "" && upstreamErr == "" {
		return loginCallbackShell(state, "")
	}

	// 重放保护：会话已导入成功时直接回成功，不再二次兑换令牌。
	// 回调页可能被刷新或从浏览历史重新打开，而 x-tt-token 只能兑换一次。
	if loginImportedAuthID(state) != "" {
		return jsonManagementResponse(http.StatusOK, map[string]any{
			"ok":      true,
			"message": "该授权已完成，请回到管理页查看账号。",
		})
	}

	if token == "" {
		reason := "脚本未取到令牌"
		if upstreamErr != "" {
			reason = "脚本报告：" + upstreamErr
		}
		markLoginFailed(state, reason)
		return jsonManagementResponse(http.StatusOK, map[string]any{
			"ok":      false,
			"message": reason + "。请确认已在扣子页面完成登录后重试。",
		})
	}

	sa, errSave := saveFromToken(token, "")
	if errSave != nil {
		markLoginFailed(state, errSave.Error())
		return jsonManagementResponse(http.StatusOK, map[string]any{
			"ok":      false,
			"message": errSave.Error(),
		})
	}

	if !markLoginImported(state, sa.AuthID) {
		// 账号已落盘但会话刚好过期：不丢账号，只提示重来。
		return jsonManagementResponse(http.StatusOK, map[string]any{
			"ok":      true,
			"label":   sa.Label,
			"message": "账号已保存，但授权会话已过期；可直接在面板使用该账号。",
		})
	}
	return jsonManagementResponse(http.StatusOK, map[string]any{
		"ok":      true,
		"label":   sa.Label,
		"message": "授权成功，正在通知管理页…",
	})
}

// loginCallbackShell 渲染令牌接收页。
//
// 页面读 location.hash 里的令牌，经同源请求头回传后再展示结果；
// 回传成功后立即清掉地址栏里的 fragment，避免令牌留在浏览历史里。
// [参数] state: 授权会话标识；detail: 非空时为错误说明（不执行回传）。
// [返回] 管理响应（HTML）。
// 最近修改时间：2026-10-18；改动原因：新增，fragment 接收页。
func loginCallbackShell(state, detail string) pluginapi.ManagementResponse {
	var b strings.Builder
	writeBridgeHead(&b, state, "扣子账号授权")
	if detail != "" {
		b.WriteString("<h1>无法继续</h1><div class=\"err\">" + htmlEscape(detail) + "</div>")
		b.WriteString("<p class=\"note\">请回到管理页重新发起授权。</p></div></body></html>")
		return htmlManagementResponse(b.String())
	}
	b.WriteString("<h1>正在完成授权</h1><div id=\"result\" class=\"note\">正在接收令牌…</div>")
	b.WriteString("<script>")
	b.WriteString("var STATE=document.body.getAttribute(\"data-state\")||\"\";")
	b.WriteString("var IMPORT=\"/v0/resource/plugins/" + providerName + callbackPath + "?state=\"+encodeURIComponent(STATE);")
	b.WriteString("var p=new URLSearchParams(location.hash.replace(/^#/,\"\"));")
	b.WriteString("var token=p.get(\"token\")||\"\";var err=p.get(\"err\")||\"\";")
	b.WriteString("var box=document.getElementById(\"result\");")
	b.WriteString("function show(text,cls){box.className=cls;box.textContent=text}")
	b.WriteString("if(!token&&!err){show(\"未收到授权令牌，请回到管理页重新发起授权。\",\"err\")}else{")
	b.WriteString("  var headers={};")
	b.WriteString("  if(token){headers[\"" + callbackTokenHeader + "\"]=token}else{headers[\"" + callbackErrorHeader + "\"]=err}")
	b.WriteString("  fetch(IMPORT,{method:\"GET\",cache:\"no-store\",headers:headers})")
	b.WriteString("    .then(function(res){return res.json()})")
	b.WriteString("    .then(function(d){")
	b.WriteString("      history.replaceState(null,\"\",location.pathname+location.search);")
	b.WriteString("      if(d&&d.ok){document.querySelector(\"h1\").textContent=\"授权成功\";show((d.message||\"\")+(d.label?\"　账号：\"+d.label:\"\"),\"ok\")}")
	b.WriteString("      else{document.querySelector(\"h1\").textContent=\"授权失败\";show((d&&d.message)||\"未知错误\",\"err\")}")
	b.WriteString("    })")
	b.WriteString("    .catch(function(e){show(\"提交失败：\"+(e&&e.message?e.message:String(e)),\"err\")})")
	b.WriteString("}")
	b.WriteString("</script>")
	b.WriteString("</div></body></html>")
	return htmlManagementResponse(b.String())
}

// loginBridgePage 渲染引导页 HTML。detail 非空时为错误页。
//
// 逐段 WriteString 拼接而非原生字符串：页面里需要写 JS，反引号会截断
// Go 的原始字符串字面量（与 copilot / doubao 桥接页同一处理）。
// [参数] state: 授权会话标识；detail: 错误说明。
// [返回] 管理响应（HTML）。
// 最近修改时间：2026-10-18；改动原因：新增，引导页渲染。
func loginBridgePage(state, detail string) pluginapi.ManagementResponse {
	var b strings.Builder
	writeBridgeHead(&b, state, "扣子账号授权")
	if detail != "" {
		b.WriteString("<h1>无法继续</h1><div class=\"err\">" + htmlEscape(detail) + "</div>")
		b.WriteString("</div></body></html>")
		return htmlManagementResponse(b.String())
	}
	writeBridgeBody(&b, state)
	b.WriteString("</div></body></html>")
	return htmlManagementResponse(b.String())
}

// writeBridgeHead 写出页面头部与样式表。
// [参数] b: 输出缓冲；state: 授权会话标识（写入 body 的 data 属性）；title: 页面标题。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，从渲染函数中拆出。
func writeBridgeHead(b *strings.Builder, state, title string) {
	b.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">")
	b.WriteString("<title>" + htmlEscape(title) + "</title>")
	b.WriteString("<style>")
	b.WriteString("body{font-family:-apple-system,'Segoe UI','Microsoft YaHei',sans-serif;background:#f7f8fb;color:#22262e;margin:0;padding:32px 16px;display:flex;justify-content:center}")
	b.WriteString(".card{background:#fff;border:1px solid #e4e7ee;border-radius:14px;max-width:660px;width:100%;padding:26px 28px}")
	b.WriteString("h1{font-size:19px;margin:0 0 10px}p{line-height:1.7;margin:8px 0;font-size:14px;color:#5f6673}")
	b.WriteString("ol{padding-left:20px;color:#5f6673}ol li{margin:9px 0;font-size:14px;line-height:1.7}")
	b.WriteString("code{background:#f0f2f6;border-radius:4px;padding:2px 6px;font-size:13px}")
	b.WriteString("pre{background:#1e2430;color:#dfe5ef;border-radius:9px;padding:13px 15px;font:12px/1.6 ui-monospace,Consolas,monospace;overflow-x:auto;white-space:pre-wrap;word-break:break-all}")
	b.WriteString("button{margin-top:12px;background:#3b7cff;color:#fff;border:0;border-radius:8px;padding:9px 18px;font-size:14px;cursor:pointer}")
	b.WriteString("button:disabled{opacity:.55;cursor:default}")
	b.WriteString(".err{background:#fdf1ef;border:1px solid #e8b9b1;border-radius:8px;padding:10px 14px;margin-top:14px;font-size:14px;color:#a8452f}")
	b.WriteString(".ok{background:#eaf8f2;border:1px solid #a8dcc7;border-radius:8px;padding:10px 14px;margin-top:14px;font-size:14px;color:#0d7a55}")
	b.WriteString(".note{background:#fdf7e6;border:1px solid #edd9a3;border-radius:8px;padding:10px 14px;margin-top:14px;font-size:13px;color:#8a6a10}")
	b.WriteString("</style></head><body data-provider=\"" + htmlEscape(providerName) + "\" data-state=\"" + htmlEscape(state) + "\"><div class=\"card\">")
}

// writeBridgeBody 写出引导文案与一次性脚本。
//
// 脚本内容由 writeAuthScript 生成并写入 <pre>，用户可整段复制到扣子页面的
// DevTools Console 执行，或存为书签。
// [参数] b: 输出缓冲；state: 授权会话标识。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，引导正文与脚本注入。
func writeBridgeBody(b *strings.Builder, state string) {
	script := buildAuthScript(state)
	b.WriteString("<h1>授权扣子账号</h1>")
	b.WriteString("<p>扣子没有开放给第三方的一键授权：登录令牌只发给桌面端的 <code>coze-desktop://</code> 协议，插件无法直接接收。请按下面三步操作，全程约 30 秒。</p>")
	b.WriteString("<ol>")
	b.WriteString("<li>点击下方按钮打开 <code>https://www.coze.cn</code>，按官方页面完成登录（手机号 / 抖音 / 飞书 / 火山任一）。</li>")
	b.WriteString("<li>在扣子页面按 <code>F12</code> 打开开发者工具 → 切到 <code>Console</code> 标签。</li>")
	b.WriteString("<li>复制下方脚本粘贴到 Console 回车执行；页面会跳回本插件并自动完成授权。</li>")
	b.WriteString("</ol>")
	b.WriteString("<button id=\"openCoze\" type=\"button\">打开扣子并登录</button>")
	b.WriteString("<p style=\"margin-top:18px\">一次性授权脚本（15 分钟内有效）：</p>")
	b.WriteString("<pre id=\"authScript\">" + htmlEscape(script) + "</pre>")
	b.WriteString("<button id=\"copyScript\" type=\"button\">复制脚本</button>")
	b.WriteString("<div id=\"copyHint\"></div>")
	b.WriteString("<p class=\"note\">脚本只做一件事：在扣子页面内调用令牌接口取 <code>x-tt-token</code>，然后把令牌交给本插件。它不读取、也不上传你的 Cookie。</p>")
	b.WriteString("<script>")
	b.WriteString("var STATE=document.body.getAttribute(\"data-state\")||\"\";")
	// 脚本在扣子页面执行，那里的 location.origin 是 coze.cn；回调地址必须指向
	// 本插件所在域。因此占位符在桥接页（即 CPA 域）加载时替换为当前 origin。
	b.WriteString("var scriptEl=document.getElementById(\"authScript\");")
	b.WriteString("if(scriptEl){scriptEl.textContent=scriptEl.textContent.split(\"__CPA_ORIGIN__\").join(location.origin)}")
	b.WriteString("var openBtn=document.getElementById(\"openCoze\");")
	b.WriteString("if(openBtn){openBtn.addEventListener(\"click\",function(){window.open(\"https://www.coze.cn\",\"_blank\",\"noopener\")})}")
	b.WriteString("var copyBtn=document.getElementById(\"copyScript\");")
	b.WriteString("if(copyBtn){copyBtn.addEventListener(\"click\",function(){")
	b.WriteString("  var text=document.getElementById(\"authScript\").textContent||\"\";")
	b.WriteString("  var hint=document.getElementById(\"copyHint\");")
	b.WriteString("  var done=function(){hint.className=\"ok\";hint.textContent=\"已复制，请到扣子页面的 Console 粘贴执行。\"};")
	b.WriteString("  var fail=function(){hint.className=\"err\";hint.textContent=\"自动复制失败，请手动选中脚本文本复制。\"};")
	b.WriteString("  if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(text).then(done).catch(fail)}else{fail()}")
	b.WriteString("})}")
	b.WriteString("</script>")
}

// cpaOriginPlaceholder 是脚本里等待替换成插件所在域 origin 的占位符。
//
// 不能直接在服务端拼绝对地址：插件运行在容器内，看不到用户实际访问的域名
// （反向代理 / 端口映射都会改写）。桥接页在浏览器里加载，location.origin
// 就是用户真实可用的地址，替换后才不会把令牌发到 coze.cn。
const cpaOriginPlaceholder = "__CPA_ORIGIN__"

// buildAuthScript 生成一次性同源授权脚本。
//
// 脚本在扣子页面上下文执行，因此 fetch 天然带上登录 Cookie（credentials
// 必须显式设为 include：跨域或默认策略下不一定自动携带）。
// 取到 x-tt-token 后跳转插件回调页；失败时把上游 message 一并带回，
// 让回调页能显示可读原因而不是笼统的「失败」。
// [参数] state: 授权会话标识。
// [返回] 可粘贴执行的 JS 源码（含待替换的 origin 占位符）。
// 最近修改时间：2026-10-18；改动原因：新增，脚本生成；回调域改为占位符注入。
func buildAuthScript(state string) string {
	callback := "/v0/resource/plugins/" + providerName + callbackPath
	var b strings.Builder
	b.WriteString("(async()=>{\n")
	b.WriteString("  const r=await fetch('/passport/token/beat/v2/',{method:'POST',credentials:'include',")
	b.WriteString("headers:{'sdk-version':'2','Content-Type':'application/json'},body:'{}'});\n")
	b.WriteString("  const j=await r.json().catch(()=>({}));\n")
	b.WriteString("  const t=r.headers.get('x-tt-token');\n")
	b.WriteString("  const cb='")
	b.WriteString(cpaOriginPlaceholder)
	b.WriteString(callback)
	b.WriteString("?state=")
	b.WriteString(jsStringEscape(state))
	b.WriteString("';\n")
	b.WriteString("  if(!t){location.href=cb+'#err='+encodeURIComponent((j&&j.data&&j.data.description)||(j&&j.message)||'no_token');return;}\n")
	b.WriteString("  location.href=cb+'#token='+encodeURIComponent(t);\n")
	b.WriteString("})()")
	return b.String()
}

// jsStringEscape 转义要嵌入单引号 JS 字符串的文本。
// [参数] s: 原始文本。
// [返回] 转义后的文本。
// 最近修改时间：2026-10-18；改动原因：新增，避免 state 破坏脚本字面量。
func jsStringEscape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "'", "\\'", "\n", "\\n", "\r", "\\r", "<", "\\x3c")
	return r.Replace(s)
}
