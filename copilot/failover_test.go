package main

import (
	"testing"
	"time"
)

// TestFailoverCooldownFor_Ladder 锁定阶梯时长契约：count 每增 1 冷却加一个基数，
// 到 failoverCooldownLadderCap 后恒为上限。
// 最近修改时间：2026-10-16 23:29:50；改动原因：固定 15s 改为 30s 递增阶梯、上限 10 分钟。
func TestFailoverCooldownFor_Ladder(t *testing.T) {
	cases := []struct {
		count int
		want  time.Duration
	}{
		{0, 0},
		{-1, 0},
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 90 * time.Second},
		{4, 120 * time.Second},
		{19, 570 * time.Second},
		{20, 10 * time.Minute},
		{21, 10 * time.Minute},
		{99, 10 * time.Minute},
	}
	for _, tc := range cases {
		if got := failoverCooldownFor(tc.count); got != tc.want {
			t.Fatalf("failoverCooldownFor(%d) = %v, want %v", tc.count, got, tc.want)
		}
	}
	if failoverCooldownLadderCap != 20 {
		t.Fatalf("failoverCooldownLadderCap = %d, want 20", failoverCooldownLadderCap)
	}
}

// TestNoteAccountFailure_LadderCooldown 锁定连续失败逐次递增：
// 30s → 60s → 90s，成功后清零回落。
func TestNoteAccountFailure_LadderCooldown(t *testing.T) {
	const authID = "copilot-ladder"
	resetAccountFailover(authID)
	t.Cleanup(func() { resetAccountFailover(authID) })

	wants := []time.Duration{30 * time.Second, 60 * time.Second, 90 * time.Second}
	for i, want := range wants {
		noteAccountFailure(authID, 429, "rate limit")
		failoverMu.RLock()
		entry := failoverRecords[authID]
		remain := time.Until(entry.cooldown)
		count := entry.failCount
		failoverMu.RUnlock()
		if count != i+1 {
			t.Fatalf("iteration %d: failCount = %d, want %d", i, count, i+1)
		}
		if remain <= want-5*time.Second || remain > want+5*time.Second {
			t.Fatalf("iteration %d: cooldown remain = %v, want ~%v", i, remain, want)
		}
	}
	// 成功后清零，下一次失败回到 30s。
	resetAccountFailover(authID)
	noteAccountFailure(authID, 429, "rate limit")
	failoverMu.RLock()
	remain := time.Until(failoverRecords[authID].cooldown)
	failoverMu.RUnlock()
	if remain <= 25*time.Second || remain > 35*time.Second {
		t.Fatalf("after reset: cooldown remain = %v, want ~30s", remain)
	}
}
