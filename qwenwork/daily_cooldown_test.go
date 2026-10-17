package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIsDailyCountExceeded 验证 Qoder 专属 110 错误码与错误文本的准确识别与正常帧防误判。
func TestIsDailyCountExceeded(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"典型110纯文本", http.StatusOK, "upstream 200: 110: Billing daily count exceeded", true},
		{"内嵌JSON code 110", http.StatusOK, `{"code":"110","message":"Billing daily count exceeded"}`, true},
		{"全小写文本匹配", http.StatusOK, "error: billing daily count exceeded for user", true},
		{"不带billing的日常次数文本", http.StatusOK, "upstream daily count exceeded", true},
		{"冒号格式110", http.StatusOK, "110: daily limit reached", true},
		{"正常业务帧含普通数字", http.StatusOK, `{"choices":[{"delta":{"content":"110 is a number"}}]}`, false},
		{"常规429限流帧", http.StatusTooManyRequests, `{"error":{"message":"Rate limit reached"}}`, false},
		{"常规403权限帧", http.StatusForbidden, `{"code":"403","message":"Forbidden"}`, false},
		{"排队错误帧10605", http.StatusOK, `{"code":"403","message":"{\"code\":\"10605\",\"message\":\"{\\\"isQueued\\\":true}\"}"}`, false},
		{"空响应体", http.StatusOK, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isDailyCountExceeded(tc.status, tc.body)
			if got != tc.want {
				t.Fatalf("isDailyCountExceeded(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
			}
		})
	}
}

// TestParseDailyCoolingUntilFromAuthJSON 验证从物理 auth JSON 解析时间戳。
func TestParseDailyCoolingUntilFromAuthJSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	rfc := now.Format(time.RFC3339)

	validJSON := []byte(`{"name":"test","daily_cooling_until":"` + rfc + `"}`)
	parsed := parseDailyCoolingUntilFromAuthJSON(validJSON)
	if !parsed.Equal(now) {
		t.Fatalf("parseDailyCoolingUntilFromAuthJSON got %v, want %v", parsed, now)
	}

	missingJSON := []byte(`{"name":"test"}`)
	if parsed := parseDailyCoolingUntilFromAuthJSON(missingJSON); !parsed.IsZero() {
		t.Fatalf("expected zero time for missing field, got %v", parsed)
	}

	invalidJSON := []byte(`{"daily_cooling_until":"not-a-date"}`)
	if parsed := parseDailyCoolingUntilFromAuthJSON(invalidJSON); !parsed.IsZero() {
		t.Fatalf("expected zero time for invalid date, got %v", parsed)
	}
}

// TestDailyCooldownPersistFailureReason 验证落盘失败原因分类：
// 成功不产生噪声日志，路径缺失与一般落盘错误给出可定位的中文原因。
func TestDailyCooldownPersistFailureReason(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantPart string
		wantText bool
	}{
		{"成功不产生日志", nil, "", false},
		{"物理路径缺失", fmt.Errorf("daily cooling persist: %w", errAuthMissing()), "物理路径缺失", true},
		{"一般落盘错误保留原文", fmt.Errorf("daily cooling persist: write auth file: %w", errors.New("permission denied")), "permission denied", true},
		{"空错误文本兜底", errors.New(""), "未知落盘错误", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dailyCooldownPersistFailureReason(tc.err)
			if tc.err == nil {
				if got != "" {
					t.Fatalf("nil error must yield empty reason, got %q", got)
				}
				return
			}
			if !tc.wantText || !strings.Contains(got, tc.wantPart) {
				t.Fatalf("reason %q must contain %q", got, tc.wantPart)
			}
		})
	}
}

// TestTriggerDailyCooldownKeepsMemoryOnPersistFailure 验证落盘失败时内存冷却仍然生效：
// host RPC 在 shim 环境不可用，triggerDailyCooldown 必然在落盘阶段失败，
// 但内存镜像已在落盘之前写入，账号仍须被路由排除。
func TestTriggerDailyCooldownKeepsMemoryOnPersistFailure(t *testing.T) {
	dailyCoolingMu.Lock()
	dailyCoolingMap = make(map[string]time.Time)
	dailyCoolingMu.Unlock()

	authID := "qwenwork-01a126de-b721-7e9d-b3b5-f1534aac824d.json"
	err := triggerDailyCooldown("", authID)
	if err == nil {
		t.Fatalf("expected persist failure in shim environment (host RPC unavailable)")
	}
	if reason := dailyCooldownPersistFailureReason(err); reason == "" {
		t.Fatalf("persist failure must produce a non-empty reason")
	}
	if !isAccountDailyCooling(authID) {
		t.Fatalf("memory cooling must survive persist failure for %s", authID)
	}
	if !isAccountDailyCooling("01a126de-b721-7e9d-b3b5-f1534aac824d") {
		t.Fatalf("normalized key must also see memory cooling")
	}
}

// TestMergeActiveDailyCooling 验证 watchdog 刷新不会丢弃未落盘的内存冷却，
// 同时让到期条目自然淘汰、磁盘条目优先。
func TestMergeActiveDailyCooling(t *testing.T) {
	now := time.Now()
	future := now.Add(2 * time.Hour)
	past := now.Add(-time.Hour)

	fromDisk := map[string]time.Time{
		"disk-active": future,
		"disk-stale":  past,
	}
	inMemory := map[string]time.Time{
		"memory-only":  future,
		"memory-stale": past,
		"disk-active":  future.Add(time.Hour),
	}
	merged := mergeActiveDailyCooling(fromDisk, inMemory, now)

	if until, ok := merged["memory-only"]; !ok || !until.Equal(future) {
		t.Fatalf("memory-only active entry must survive refresh: %v ok=%v", until, ok)
	}
	if _, ok := merged["memory-stale"]; ok {
		t.Fatalf("expired memory entry must be dropped")
	}
	if until, ok := merged["disk-active"]; !ok || !until.Equal(future) {
		t.Fatalf("disk entry must win on key conflict: %v ok=%v", until, ok)
	}
	if _, ok := merged["disk-stale"]; !ok {
		t.Fatalf("disk scan result must be preserved as-is")
	}
	// 入参不得被修改
	if _, ok := fromDisk["memory-only"]; ok {
		t.Fatalf("merge must not mutate fromDisk")
	}
}

// TestNoCallerDiscardsDailyCooldownError 静态锁定调用点不再丢弃错误。
func TestNoCallerDiscardsDailyCooldownError(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, readErr := os.ReadFile(f)
		if readErr != nil {
			t.Fatalf("read %s: %v", f, readErr)
		}
		if strings.Contains(string(raw), "_ = triggerDailyCooldown") {
			t.Fatalf("%s still discards triggerDailyCooldown error", f)
		}
	}
}

func TestDailyCooldown_MemoryCheck(t *testing.T) {
	dailyCoolingMu.Lock()
	dailyCoolingMap = make(map[string]time.Time)
	dailyCoolingMu.Unlock()

	authID := "qwenwork-01a126de-b721-7e9d-b3b5-f1534aac824d.json"

	// 1. 未设置时为 false
	if isAccountDailyCooling(authID) {
		t.Fatalf("expected false before cooling set")
	}

	// 2. 设置未到期（未来 2 小时）
	future := time.Now().Add(2 * time.Hour)
	dailyCoolingMu.Lock()
	dailyCoolingMap[authID] = future
	dailyCoolingMu.Unlock()

	if !isAccountDailyCooling(authID) {
		t.Fatalf("expected true for active cooling")
	}
	// 归一化 key 同样有效
	normID := "01a126de-b721-7e9d-b3b5-f1534aac824d"
	if !isAccountDailyCooling(normID) {
		t.Fatalf("expected true for normalized key lookup")
	}

	until := getAccountDailyCoolingUntil(authID)
	if until.Sub(future) > time.Second || future.Sub(until) > time.Second {
		t.Fatalf("until mismatch: got %v, want %v", until, future)
	}

	// 3. 设置已过期时间点（过去 1 小时）
	past := time.Now().Add(-1 * time.Hour)
	dailyCoolingMu.Lock()
	dailyCoolingMap[authID] = past
	dailyCoolingMu.Unlock()

	if isAccountDailyCooling(authID) {
		t.Fatalf("expected false for expired cooling")
	}
	if isAccountDailyCooling(normID) {
		t.Fatalf("expected false for normalized key on expired cooling")
	}
}
