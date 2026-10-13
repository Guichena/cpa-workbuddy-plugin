package main

import (
	"net/http"
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

// TestDailyCooldown_MemoryCheck 验证内存中到期判定与归一化键支持。
func TestDailyCooldown_MemoryCheck(t *testing.T) {
	dailyCoolingMu.Lock()
	dailyCoolingMap = make(map[string]time.Time)
	dailyCoolingMu.Unlock()

	authID := "qoderwork-01a126de-b721-7e9d-b3b5-f1534aac824d.json"

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
