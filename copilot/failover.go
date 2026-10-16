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
//
// GitHub Copilot 的鉴权失败（401/403）、配额耗尽（429）与上游 5xx 都属账号级
// 或配额级故障，换号可能恢复；400 类业务参数错误换号无意义，不重试。
// [参数] statusCode: 上游 HTTP 状态码；errMsg: 错误文本。
// [返回] true 表示应换号重试。
// 最近修改时间：2026-10-15；改动原因：新增，Copilot 上游错误换号判定。
func shouldRotateOnUpstreamErr(statusCode int, errMsg string) bool {
	switch statusCode {
	case http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	lower := strings.ToLower(errMsg)
	for _, kw := range []string{
		"unauthorized",
		"forbidden",
		"rate limit",
		"quota",
		"exceeded",
		"insufficient",
		"bad credentials",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
