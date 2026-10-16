// policy.go is the pure decision layer for credit-driven lifecycle actions:
// given an account's region and current credits, decide whether to disable
// CN: disable when exhausted, re-enable after check-in restores credits, or
// leave it alone. No I/O happens here — reconcileOneAccount consumes these
// decisions and applies them via the lifecycle.go authfile helpers.
package main

import (
	"fmt"
	"strings"
	"sync"
)

// lifecycleAction is the policy decision for one account.
type lifecycleAction int

const (
	lifecycleNone lifecycleAction = iota
	lifecycleDisable
	lifecycleDelete
	lifecycleReenable
)

func (a lifecycleAction) String() string {
	switch a {
	case lifecycleDisable:
		return "disable"
	case lifecycleDelete:
		return "delete"
	case lifecycleReenable:
		return "reenable"
	default:
		return "none"
	}
}

// lifecycleAuto gates automatic disable/delete/reenable. Default false.
var (
	lifecycleAuto   = false
	lifecycleAutoMu sync.RWMutex
)

func lifecycleEnabled() bool {
	lifecycleAutoMu.RLock()
	defer lifecycleAutoMu.RUnlock()
	return lifecycleAuto
}

// shouldActOnCredits is true only when credits are *known* exhausted.
// nil / empty (no packages, no used) is unknown → false.
func shouldActOnCredits(cr *creditsSummary) bool {
	return isCreditsExhausted(cr)
}

// hardCreditMarkers are case-insensitive substrings in upstream error bodies.
var hardCreditMarkers = []string{
	"insufficient credit",
	"insufficient credits",
	"no credit",
	"no credits",
	"credit exhausted",
	"credits exhausted",
	"out of credit",
	"out of credits",
	"quota exceeded",
	"quota exhaust",
	"exceeded the quota",
	"14018",
	"payment required",
	"积分不足",
	"额度不足",
	"余额不足",
	"积分用完",
	"额度用尽",
	"没有积分",
	"credit not enough",
	"not enough credit",
}

// defaultMinRoutableCredits 是安全缓冲额度红线默认值（20 点）。
// 剩余额度低于该阈值的账号不再承载新请求，防止推理中途断开。
const defaultMinRoutableCredits int64 = 20

var (
	minRoutableCredits   int64 = defaultMinRoutableCredits
	minRoutableCreditsMu sync.RWMutex
)

// loadedMinRoutableCredits 返回当前生效的安全缓冲额度红线。
func loadedMinRoutableCredits() int64 {
	minRoutableCreditsMu.RLock()
	defer minRoutableCreditsMu.RUnlock()
	return minRoutableCredits
}

// setMinRoutableCredits 设置安全缓冲额度红线并返回清理函数，供单测或动态配置使用。
func setMinRoutableCredits(n int64) func() {
	minRoutableCreditsMu.Lock()
	old := minRoutableCredits
	minRoutableCredits = n
	minRoutableCreditsMu.Unlock()
	return func() {
		minRoutableCreditsMu.Lock()
		minRoutableCredits = old
		minRoutableCreditsMu.Unlock()
	}
}

// isCreditsLowOrExhausted 判定账号是否额度耗尽或已低于安全缓冲额度红线。
func isCreditsLowOrExhausted(cr *creditsSummary) bool {
	if cr == nil {
		return false
	}
	if isCreditsExhausted(cr) {
		return true
	}
	return cr.TotalRemain < loadedMinRoutableCredits()
}

// isDailyCountExceeded 判定上游是否返回 Qoder 专属的 110 日请求次数上限限制。
// [参数] status: HTTP 响应状态码；body: 错误响应体文本。
// [返回] 命中 110 限额错误时返回 true。
// 最近修改时间 2026-10-13（新增 Qoder 日调用上限专用判定）
func isDailyCountExceeded(status int, body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "billing daily count exceeded") ||
		strings.Contains(lower, "daily count exceeded") ||
		strings.Contains(body, `"110"`) ||
		strings.Contains(body, `code: 110`) ||
		strings.Contains(body, `code: "110"`) ||
		strings.Contains(body, `110:`)
}

// isHardCreditError reports business "out of credits" style failures.
// 402 is treated as payment/credit. Pure 429 is not hard unless body has credit markers.
func isHardCreditError(status int, body string) bool {
	if status == httpStatusPaymentRequired {
		return true
	}
	lower := strings.ToLower(body)
	for _, m := range hardCreditMarkers {
		if strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}
	// Chinese markers may not lower-map usefully; also scan raw.
	for _, m := range hardCreditMarkers {
		if strings.Contains(body, m) {
			return true
		}
	}
	return false
}

const httpStatusPaymentRequired = 402

// httpStatusServiceUnavailable 是排队帧对外的状态码（见 queueFrameHTTPStatus）。
const httpStatusServiceUnavailable = 503

// isSoftRateLimit is pure throttling without hard-credit semantics.
func isSoftRateLimit(status int, body string) bool {
	if isHardCreditError(status, body) {
		return false
	}
	if status == 429 {
		return true
	}
	lower := strings.ToLower(body)
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "throttl")
}

// isEmptyStreamBody reports whether the failure text describes an upstream
// zero-byte stream: the gateway accepted the connection and then closed it
// before delivering any payload (host-side empty_stream). The pump records
// such attempts with the canonical text "upstream stream closed before
// first payload".
func isEmptyStreamBody(body string) bool {
	return strings.Contains(body, "closed before first payload") ||
		strings.Contains(body, "empty stream") ||
		strings.Contains(body, "invalid SSE response: missing output and done event")
}

// isTransientThrottle 判定失败是否属于上游瞬时过载信号：429 无额度标记、
// 软限流文案或上游零字节断流。2026-10-16 起这类失败与硬失败共用
// recordAccountFailure 的计数与阶梯冷却路径，不再单独豁免计数；本函数仅用于
// 把「HTTP 200 承载的零字节断流文案」等不命中 isAccountFailure 状态码判据的
// 失败纳入可记账范围。硬额度错误优先排除：429 + 额度标记属账号耗尽。
// 历史沿革：2026-09-06 曾因网关抖动连续零字节失败误隔离 4 个健康账号而把
// 这类失败改为「只冷却不计数」；异常池于 2026-09-08 移除后计数不再触发
// 冻结，2026-10-16 起软失败重新并入阶梯计数，代价是抖动期间账号会沿阶梯
// 冷却更久，由「一次成功即清零」兜底回落。
func isTransientThrottle(status int, body string) bool {
	if isHardCreditError(status, body) {
		return false
	}
	return isSoftRateLimit(status, body) || isEmptyStreamBody(body)
}

// queueBusinessCode 是 Qoder 上游「排队 / 容量受限」的专属业务码。
const queueBusinessCode = "10605"

// queueFrameHTTPStatus 是排队帧对外呈现的 HTTP 状态码。
//
// 选 503 而非 403：宿主 MarkResult 对 403 会给凭据打 30 分钟冷却
// （conductor_cooldown.go:856），而排队是分钟级现象；503 只触发瞬时冷却
// （默认 1 分钟，conductor_cooldown.go:949），且同样在宿主跨 provider 轮转
// 白名单（isCredentialRetryRoundStatus）内，能触发换道。
const queueFrameHTTPStatus = httpStatusServiceUnavailable

// isQueueFrame reports whether an upstream error text describes Qoder's
// capacity-queue state rather than an account fault.
//
// 生产证据（2026-10-15）：排队帧为 `403: 10605: {"isQueued":true,
// "queueCount":14250,"retryAfterSeconds":30,"serviceAvailable":true,
// "waitTime":478}`。serviceAvailable:true 证明账号本身健康、饱和的只是上游
// 队列；多账号同时命中同一队列（queueCount 单调递增）亦证明排队是请求级 +
// 时段性。该帧此前被 remap 成 403 记账 + 15s 冷却，把上游容量问题放大成整池
// auth_unavailable，故单独识别以跳过账号级记账。
//
// [参数] body：上游错误文本（含 sseErrorFrame 展开后的排队帧）。
// [返回] 命中排队/容量语义时返回 true。
// 最近修改时间 2026-10-15（排队帧误判账号级故障致健康号池被逐个冷却）
func isQueueFrame(body string) bool {
	if strings.TrimSpace(body) == "" {
		return false
	}
	return strings.Contains(body, "isQueued") ||
		strings.Contains(body, "queueCount") ||
		strings.Contains(body, queueBusinessCode)
}

// envelopeHTTPStatusForError 决定失败信封对外携带的 HTTP 状态码。
//
// 宿主 statusCodeFromError（conductor_cooldown.go:1448）只认 StatusCode() int
// 数字接口，纯文本错误恒得 0，跨 provider 轮转（isCredentialRetryRoundStatus）
// 永不触发；排队帧据此带上 503，让宿主把请求换到另一家上游。
//
// [参数] message：失败信封的错误文本。
// [返回] 排队帧返回 503；其余返回 0，保持既有行为。
// 最近修改时间 2026-10-15（打通宿主跨 provider 轮转的状态码通道）
func envelopeHTTPStatusForError(message string) int {
	if isQueueFrame(message) {
		return queueFrameHTTPStatus
	}
	return 0
}

// lifecycleActionFor chooses disable/none from credits.
// Qoder AI is CN-only — disable (not delete) so check-in can restore credits
// without forcing the user to re-import a PAT.
func lifecycleActionFor(region string, cr *creditsSummary) lifecycleAction {
	if !shouldActOnCredits(cr) {
		return lifecycleNone
	}
	return lifecycleDisable
}

// shouldReenableCN is true when a CN account is disabled but now has credits.
func shouldReenableCN(disabled bool, cr *creditsSummary) bool {
	if !disabled {
		return false
	}
	if cr == nil {
		return false
	}
	if isCreditsExhausted(cr) {
		return false
	}
	// Known positive remain, or non-exhausted with packages still having room.
	return cr.TotalRemain > 0
}

// displayNote builds a one-line note for CPAMP Auth cards.
func displayNote(sa *storedAuth, cr *creditsSummary, disabled bool) string {
	region := strings.ToUpper("cn")
	if region == "CN" {
		region = "CN"
	} else {
		region = "CN"
	}
	parts := []string{region}
	if disabled {
		parts = append(parts, "已禁用")
	}
	switch {
	case cr == nil:
		parts = append(parts, "积分未知")
	case isCreditsExhausted(cr):
		parts = append(parts, fmt.Sprintf("耗尽 · 余%d 已用%d", cr.TotalRemain, cr.TotalUsed))
	default:
		// Show remain as primary (what you can still spend). Used is real cycle spend.
		// Size (capacity) grows with check-in packs — do not treat size↑ as usage↓.
		if cr.TotalSize > 0 {
			parts = append(parts, fmt.Sprintf("余%d 已用%d 池%d", cr.TotalRemain, cr.TotalUsed, cr.TotalSize))
		} else {
			parts = append(parts, fmt.Sprintf("余%d 已用%d", cr.TotalRemain, cr.TotalUsed))
		}
	}
	note := strings.Join(parts, " · ")
	if len(note) > 80 {
		note = note[:77] + "..."
	}
	return note
}

// labelForAuth adds [CN] for host labels.
func labelForAuth(sa *storedAuth) string {
	base := "Qoder AI"
	if sa != nil && strings.TrimSpace(sa.Account.Nickname) != "" {
		base = strings.TrimSpace(sa.Account.Nickname)
	}
	tag := "CN"
	if "cn" == "global" {
		tag = "CN"
	}
	return base + " [" + tag + "]"
}
