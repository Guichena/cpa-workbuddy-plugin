package main

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

type failoverEntry struct {
	failCount int
	cooldown  time.Time
	lastError string
}

var (
	failoverMu      sync.RWMutex
	failoverRecords = make(map[string]*failoverEntry)
)

// failoverCooldownBase 是阶梯基数：每次连续失败使冷却窗口增加一个基数，
// 首次失败即冷却一个基数（30s），第 n 次失败冷却 n × 30s。
const failoverCooldownBase = 30 * time.Second

// failoverCooldownMax 是阶梯上限：连续失败再多也不超过 10 分钟。
const failoverCooldownMax = 10 * time.Minute

// failoverCooldownLadderCap 是达到上限所需的连续失败次数（10min / 30s = 20）。
// 先比较次数再相乘，避免 count 极大时的乘法溢出。
const failoverCooldownLadderCap = int(failoverCooldownMax / failoverCooldownBase)

// failoverCooldownFor 返回第 count 次连续失败对应的阶梯冷却时长。
// 时长按 failoverCooldownBase 线性递增并在 failoverCooldownMax 封顶，
// count <= 0 返回零值表示不冷却。
// [参数] count: 当前连续失败次数，由 noteAccountFailure 递增、成功后清零。
// [返回] 本次失败应施加的冷却时长。
// 最近修改时间：2026-10-16 23:29:50；改动原因：固定 15s 改为 30s 递增阶梯、上限 10 分钟。
func failoverCooldownFor(count int) time.Duration {
	if count <= 0 {
		return 0
	}
	if count >= failoverCooldownLadderCap {
		return failoverCooldownMax
	}
	return failoverCooldownBase * time.Duration(count)
}

// isAccountCoolingDown 检查账号是否处于冷却期。
func isAccountCoolingDown(authID string) bool {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return false
	}
	failoverMu.RLock()
	defer failoverMu.RUnlock()

	entry, ok := failoverRecords[authID]
	if !ok {
		return false
	}
	return time.Now().Before(entry.cooldown)
}

// noteAccountFailure 记录账号故障并按连续失败次数施加阶梯冷却。
// [参数] authID: 账号唯一标识；statusCode: 上游 HTTP 状态码；errMsg: 错误文本。
// [返回] 无。
// 最近修改时间：2026-10-16 23:29:50；改动原因：固定 15s 改为 30s 递增阶梯、上限 10 分钟。
func noteAccountFailure(authID string, statusCode int, errMsg string) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	failoverMu.Lock()
	defer failoverMu.Unlock()

	entry, ok := failoverRecords[authID]
	if !ok {
		entry = &failoverEntry{}
		failoverRecords[authID] = entry
	}

	entry.failCount++
	entry.lastError = errMsg
	entry.cooldown = time.Now().Add(failoverCooldownFor(entry.failCount))
}

// resetAccountFailover 成功调用后重置故障计数与冷却状态。
func resetAccountFailover(authID string) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	failoverMu.Lock()
	defer failoverMu.Unlock()

	delete(failoverRecords, authID)
}

// shouldRotateOnUpstreamErr 判断上游错误是否应触发同请求换号重试。
func shouldRotateOnUpstreamErr(statusCode int, errMsg string) bool {
	if statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusForbidden ||
		statusCode == http.StatusNotFound ||
		statusCode == http.StatusMethodNotAllowed ||
		statusCode == http.StatusTooManyRequests {
		return true
	}
	lower := strings.ToLower(errMsg)
	if strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "exceeded the quota") ||
		strings.Contains(lower, "14018") ||
		strings.Contains(lower, "额度已用尽") ||
		strings.Contains(lower, "insufficient") ||
		strings.Contains(lower, "concurrency") ||
		strings.Contains(lower, "3009") {
		return true
	}
	return false
}
