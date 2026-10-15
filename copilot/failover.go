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

// failoverCooldown 是单次账号故障后的固定冷却窗口（对齐既有插件成熟策略）。
// 避免因瞬时并发满（429）动辄封禁数分钟导致整个插件无账号可用。
const failoverCooldown = 15 * time.Second

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

// noteAccountFailure 记录账号故障并设置固定冷却。
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
	entry.cooldown = time.Now().Add(failoverCooldown)
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
