package main

// 断流换号回归：上游在响应头已返回后复位连接（scanner.Err 非空）时，
// 只要客户端尚未收到任何分片就必须换号续试；已泄漏分片时换号会产生
// 双份输出，只能透传错误。决策逻辑收敛在 shouldRotateOnStreamReadError，
// 本文件只覆盖该纯函数与错误文本识别，不触 host RPC。

import (
	"testing"
)

// TestIsStreamReadError 验证断流文本识别命中真实传输层错误，且不误伤业务错误。
func TestIsStreamReadError(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"扫描器断流原文", "upstream stream read error: read tcp 10.0.0.1:443: connection reset by peer", true},
		{"仅错误码文本", "INTERNAL_ERROR", true},
		{"标准断流前缀", "upstream stream read error: unexpected EOF", true},
		{"普通4xx文本", "upstream 403: 10605: isQueued", false},
		{"额度耗尽文本", "insufficient credit", false},
		{"已泄漏断流标记", "upstream stream read error: connection reset by peer (" + streamReadErrorLeakedMarker + ")", false},
		{"空响应体", "", false},
		{"纯空白", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStreamReadError(tc.body); got != tc.want {
				t.Fatalf("isStreamReadError(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestShouldRotateOnStreamReadError 验证换号决策的四个边界：零泄漏换号、
// 已泄漏不换号、预算耗尽不换号、缺少请求体或候选不换号。
func TestShouldRotateOnStreamReadError(t *testing.T) {
	cases := []struct {
		name           string
		emitted        bool
		attempt        int
		budget         int
		hasEncodedBody bool
		hasNextAuth    bool
		want           bool
	}{
		{"零泄漏且有预算与候选", false, 0, 10, true, true, true},
		{"已泄漏不换号", true, 0, 10, true, true, false},
		{"预算耗尽不换号", false, 10, 10, true, true, false},
		{"超出预算不换号", false, 11, 10, true, true, false},
		{"无可用候选不换号", false, 0, 10, true, false, false},
		{"无法重建请求体不换号", false, 0, 10, false, true, false},
		{"零预算不换号", false, 0, 0, true, true, false},
		{"末次尝试仍可换号", false, 9, 10, true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRotateOnStreamReadError(tc.emitted, tc.attempt, tc.budget, tc.hasEncodedBody, tc.hasNextAuth)
			if got != tc.want {
				t.Fatalf("shouldRotateOnStreamReadError(emitted=%v, attempt=%d, budget=%d, body=%v, next=%v) = %v, want %v",
					tc.emitted, tc.attempt, tc.budget, tc.hasEncodedBody, tc.hasNextAuth, got, tc.want)
			}
		})
	}
}
