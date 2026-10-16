// policy.go owns the failure-classification and check-in verdict helpers.
// The status/body → failure mapping mirrors the workbuddy-provider plugin
// (accountFailover.go) with markers adapted to the Trae SOLO upstream
// (4011 rate-limit, quota markers, device-dedupe wording).
package main

import (
	"strings"
	"sync"
)

const httpStatusPaymentRequired = 402

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

// hardCreditMarkers match upstream bodies that indicate the account's credit
// quota is exhausted (as opposed to a transient/rate-limit failure).
var hardCreditMarkers = []string{
	"credit", "quota", "额度", "积分不足", "余额不足", "已用完", "exhausted",
}

// isHardCreditError reports whether the upstream response indicates the
// account ran out of credits (402 or a marker in the body).
func isHardCreditError(status int, body string) bool {
	if status == httpStatusPaymentRequired {
		return true
	}
	lower := strings.ToLower(body)
	for _, m := range hardCreditMarkers {
		if strings.Contains(lower, strings.ToLower(m)) || strings.Contains(body, m) {
			return true
		}
	}
	return false
}

// isSoftRateLimit reports whether the response is a soft per-account rate
// limit (429 or a marker), distinct from a hard credit error.
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
		strings.Contains(lower, "throttl") ||
		strings.Contains(lower, "4011") // Trae WAF rate-limit event code
}

// isEmptyStreamBody reports whether the failure text describes an upstream
// zero-byte stream: the gateway accepted the connection and then closed it
// before delivering any payload (host-side empty_stream / the collect error
// for a 200 SSE body carrying neither output nor done). Pumps and collectors
// record such attempts with these canonical texts.
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

// DeviceBlocked reports whether the check-in body indicates a device-level
// block (Trae dedupes check-in per device per day).
func DeviceBlocked(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(body, "设备") ||
		strings.Contains(lower, "device") ||
		strings.Contains(lower, "machine")
}

// AlreadyCheckedIn reports whether the body indicates the account already
// checked in today (as opposed to a device-level block).
func AlreadyCheckedIn(body string) bool {
	if DeviceBlocked(body) {
		return false
	}
	lower := strings.ToLower(body)
	return strings.Contains(body, "已签到") ||
		strings.Contains(body, "已经签到") ||
		strings.Contains(body, "明日再来") ||
		strings.Contains(body, "今日已完成") ||
		strings.Contains(body, "已领取") ||
		strings.Contains(lower, "already") ||
		strings.Contains(lower, "checked") ||
		strings.Contains(lower, "claimed")
}
