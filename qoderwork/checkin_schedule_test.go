package main

import (
	"testing"
	"time"
)

// TestCheckinHoursAreEveryFourHours 锁定签到时刻契约：本地时间每 4 小时一次，
// 与 keepalive 时段表取值一致，共享定时器无需额外门控。
func TestCheckinHoursAreEveryFourHours(t *testing.T) {
	want := []int{0, 4, 8, 12, 16, 20}
	if len(checkinHours) != len(want) {
		t.Fatalf("checkinHours = %v, want %v", checkinHours, want)
	}
	for i, h := range want {
		if checkinHours[i] != h {
			t.Fatalf("checkinHours = %v, want %v", checkinHours, want)
		}
	}
}

// TestNextCheckinTime 验证共享定时器唤醒点计算：覆盖日内下一槽位、跨天顺延，
// 以及 keepalive 时段被合并进并集。
func TestNextCheckinTime(t *testing.T) {
	loc := time.Now().Location()
	// 07:00 → 下一唤醒点 08:00（签到与 keepalive 同时段）。
	morning := time.Date(2026, 10, 14, 7, 0, 0, 0, loc)
	if got := nextCheckinTime(morning); got.Hour() != 8 || got.Day() != 14 {
		t.Fatalf("nextCheckinTime(07:00) = %v, want 08:00", got)
	}
	// 10:00 → 下一唤醒点 12:00。
	noon := time.Date(2026, 10, 14, 10, 0, 0, 0, loc)
	if got := nextCheckinTime(noon); got.Hour() != 12 {
		t.Fatalf("nextCheckinTime(10:00) = %v, want 12:00", got)
	}
	// 22:00 → 次日 00:00。
	late := time.Date(2026, 10, 14, 22, 0, 0, 0, loc)
	if got := nextCheckinTime(late); got.Hour() != 0 || got.Day() != 15 {
		t.Fatalf("nextCheckinTime(22:00) = %v, want next day 00:00", got)
	}
	// 保活时段被合并：临时改为 01/13 后，00:30 的下一唤醒点应为 01:00。
	prev := keepaliveHours
	keepaliveHours = []int{1, 13}
	merged := nextCheckinTime(time.Date(2026, 10, 14, 0, 30, 0, 0, loc))
	keepaliveHours = prev
	if merged.Hour() != 1 {
		t.Fatalf("merged schedule: nextCheckinTime(00:30) = %v, want 01:00", merged)
	}
}
