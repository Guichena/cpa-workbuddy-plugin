package main

import (
	"crypto/rand"
	"fmt"
	"net/url"
)

// 本文件集中定义豆包上游协议的常量与 query 参数集构造。
//
// 协议事实来源（2026-10-17）：Lxapk/DouBao-CPA-Plugin 的 protocol.go 源码 +
// 本仓库浏览器实测网络日志交叉验证。关键结论：
//   - 业务 API 必须携带完整 query 参数集，缺参会返回 {"code":0,"data":null}；
//   - msToken 与 a_bogus 均不在必需参数中——上游接受无签名调用，
//     这消除了重实现字节风控签名算法的必要；
//   - IM 系端点（/im/*）的 Content-Type 必须是 "application/json; encoding=utf-8"；
//     而 /chat/completion 相反，须用纯 "application/json"。

const (
	// webUserAgent 是模拟网页端请求的 User-Agent。
	webUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	// doubaoAID 是豆包网页端的应用标识。
	doubaoAID = "497858"

	// doubaoVersionCode 是客户端版本编码。
	doubaoVersionCode = "20800"

	// doubaoPCVersion 是 PC 客户端版本号。
	doubaoPCVersion = "3.41.4"

	// defaultBotID 是豆包默认助手 ID（realm 级常量）。
	// 网页客户端从 /alice/basic/launch 响应的 assistant_bot_id 获取，
	// 此处作为兜底常量；凭据校验成功后可被上游返回值覆盖。
	defaultBotID = "7338286299411103781"

	// accountInfoSDKVersion 是判据端点使用的账号 SDK 版本。
	accountInfoSDKVersion = "2.2.10"

	// contentTypeJSON 是 /chat/completion 唯一接受的 Content-Type。
	//
	// 该端点与 /im/* 族相反：/im/* 要求 "application/json; encoding=utf-8"，
	// 而 /chat/completion 只接受纯 "application/json"；带 encoding 参数会被
	// 上游按参数错误拒绝（实测 710020202）。
	contentTypeJSON = "application/json"

	// blockTypeText 是消息文本块类型（BlockType.java: BLOCK_TYPE_TEXT）。
	blockTypeText = 10000

	// blockTypeDeepThink 是深度思考块类型。
	blockTypeDeepThink = 10040

	// blockTypeCreation 是生成结果块类型（图片/视频）。
	blockTypeCreation = 2074

	// skillImageGen 是图像生成能力的上游 skill id。
	//
	// 值来自上游 /samantha/skill/recommend。缺少它时「画一只猫」只会让模型
	// 描述画面而不会真正绘制——这是静默降级，不报错。
	skillImageGen = 3

	// skillVideoGen 是视频生成能力的上游 skill id。
	skillVideoGen = 5
)

// generationMode 描述一次请求期望的回复形态。
//
// Skill 为上游能力开关：0 表示普通对话，3 表示生图，5 表示生视频。
// 缺 Skill 时「画一只猫」只会被模型描述而不会真正绘制。
type generationMode struct {
	// Skill 是上游 skill id，0 表示纯文本对话。
	Skill int
	// Kind 是回复解析类型：text / image / video。
	Kind string
}

// textMode 返回普通对话模式。
// [参数] 无。
// [返回] Skill 为 0 的文本模式。
// 最近修改时间：2026-10-17；改动原因：新增，避免各调用方重复构造零值结构。
func textMode() generationMode {
	return generationMode{Skill: 0, Kind: "text"}
}

// buildBusinessQuery 构造业务 API（/alice/*、/chat/*）的 query 参数集。
//
// 实测缺参会返回 {"code":0,"data":null}（HTTP 200 但无数据），必须补齐整个常量块。
// device_id / web_id / tea_uuid 是设备级标识，需与凭据一同持久化；
// 多账号若共用同一组标识可能被上游关联（见需求 GAP-004）。
// [参数] deviceID: 设备标识；webID: web 会话标识（同时用作 tea_uuid）；
// webTabID: 标签页标识；extra: 端点专属附加参数。
// [返回] 编码后的 query 字符串。
// 最近修改时间：2026-10-17；改动原因：新增，业务 API 参数集。
func buildBusinessQuery(deviceID, webID, webTabID string, extra map[string]string) string {
	q := url.Values{}
	q.Set("version_code", doubaoVersionCode)
	q.Set("language", "zh")
	q.Set("device_platform", "web")
	q.Set("doubao_device_platform", "web")
	q.Set("aid", doubaoAID)
	q.Set("real_aid", doubaoAID)
	q.Set("pkg_type", "release_version")
	q.Set("device_id", deviceID)
	q.Set("pc_version", doubaoPCVersion)
	q.Set("doubao_pc_version", doubaoPCVersion)
	q.Set("web_id", webID)
	q.Set("tea_uuid", webID)
	q.Set("region", "CN")
	q.Set("sys_region", "CN")
	q.Set("samantha_web", "1")
	q.Set("web_platform", "browser")
	q.Set("use-olympus-account", "1")
	if webTabID != "" {
		q.Set("web_tab_id", webTabID)
	}
	q.Set("flow_im_arch", "v2")
	for k, v := range extra {
		q.Set(k, v)
	}
	return q.Encode()
}

// endpointURL 拼接业务端点完整 URL。
// [参数] sa: 账号（提供设备级标识）；path: 端点路径；extra: 附加 query 参数。
// [返回] 完整 URL。
// 最近修改时间：2026-10-17；改动原因：新增，业务端点地址构造。
func endpointURL(sa *StoredAuth, path string, extra map[string]string) string {
	return doubaoSiteHost + path + "?" + buildBusinessQuery(sa.DeviceID, sa.WebID, sa.WebTabID, extra)
}

// upstreamError 表示一次被判定为上游侧的失败。
//
// 携带状态码与上游业务码，供调用方区分「账号级故障」（需换号/冷却）
// 与「请求本身有问题」（不该换号）。
type upstreamError struct {
	// StatusCode 是 HTTP 状态码；传输层失败时为 0。
	StatusCode int
	// Code 是上游业务码（如 710012001）；无业务码时为 0。
	Code int
	// Message 是面向用户的错误说明。
	Message string
}

// Error 实现 error 接口。
// [参数] 无。
// [返回] 错误说明。
// 最近修改时间：2026-10-17；改动原因：新增，上游错误类型。
func (e *upstreamError) Error() string { return e.Message }

// isAuthFailure 判断错误是否意味着登录态失效。
// [参数] 无。
// [返回] 属于登录态失效时返回 true。
// 最近修改时间：2026-10-17；改动原因：新增，失效识别（供账号状态上报）。
func (e *upstreamError) isAuthFailure() bool {
	if e.StatusCode == 401 || e.StatusCode == 403 {
		return true
	}
	return e.Code == 710012001
}

// isRateLimit 判断错误是否意味着上游限流。
// [参数] 无。
// [返回] 属于限流时返回 true。
// 最近修改时间：2026-10-17；改动原因：新增，限流识别（供阶梯冷却使用）。
func (e *upstreamError) isRateLimit() bool {
	if e.StatusCode == 429 {
		return true
	}
	return e.Code == 710022004 || e.Code == 710022005
}

// statusCodeHint 把上游业务码翻译成可操作的中文提示。
//
// 码值来自实测与上游客户端行为：直接回显数字对用户没有意义，
// 提示里写清「该查什么」。
//
// 710022004 的提示必须点明「账号级风控」而不是笼统的「稍后重试」：2026-10-17
// 对照实测确认它由 shark_admin/verify/semantic_reasoning 下发，同一 Cookie 换
// 出口 IP、换设备标识、换深度思考参数都照样触发，而账号信息端点仍返回 success。
// 也就是说这不是网络抖动，等几分钟通常无效，只有换账号或等风控窗口解除。
// [参数] code: 上游业务码；desc: 上游原始描述（可空）。
// [返回] 中文提示。
// 最近修改时间：2026-10-17；改动原因：限流提示改为可操作的账号级风控说明。
func statusCodeHint(code int, desc string) string {
	switch code {
	case 0:
		return ""
	case 710012001:
		return "登录态已失效，请在面板重新导入 Cookie"
	case 710022004, 710022005:
		return "账号触发上游风控（semantic_reasoning），非网络问题；该账号的推理请求被临时拦截，请换用其他账号或等待风控解除（账号信息与登录态本身仍有效）"
	case 710020202:
		return "上游拒绝了请求参数：请确认 Content-Type 为 application/json 且请求体结构完整"
	case 710010202:
		return "上游返回系统错误：通常是 Cookie 与设备标识不匹配，建议重新导入账号"
	}
	if desc == "" {
		desc = "未知错误"
	}
	return fmt.Sprintf("上游错误 %d：%s", code, desc)
}

// randomUUID 生成 UUID v4 字符串。
// [参数] 无。
// [返回] 标准 UUID v4 文本；随机源失败时返回全零 UUID。
// 最近修改时间：2026-10-17；改动原因：新增，请求体需要稳定格式的唯一标识。
func randomUUID() string {
	var b [16]byte
	if _, errRead := rand.Read(b[:]); errRead != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// buildAccountInfoQuery 构造判据端点 /passport/account/info/v2/ 的 query 参数集。
//
// 判据端点使用与业务 API 不同的参数子集（含 account_sdk_source / sdk_version /
// doubao_from），实测缺参会返回空 data。
// [参数] 无。
// [返回] 编码后的 query 字符串。
// 最近修改时间：2026-10-17；改动原因：新增，判据端点参数集。
func buildAccountInfoQuery() string {
	q := url.Values{}
	q.Set("pc_version", doubaoPCVersion)
	q.Set("version_code", doubaoVersionCode)
	q.Set("language", "zh")
	q.Set("device_platform", "web")
	q.Set("aid", doubaoAID)
	q.Set("real_aid", doubaoAID)
	q.Set("pkg_type", "release_version")
	q.Set("use-olympus-account", "1")
	q.Set("region", "CN")
	q.Set("sys_region", "CN")
	q.Set("account_sdk_source", "web")
	q.Set("sdk_version", accountInfoSDKVersion)
	q.Set("doubao_from", "inject_html")
	return q.Encode()
}
