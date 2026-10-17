package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// storeCredits seeds accountCache with a credits snapshot for one auth ID and
// auto-clears it on test end.
func storeCredits(t *testing.T, id string, remain, used, total int64) {
	t.Helper()
	accountCache.Store(id, &accountCacheEntry{credits: &creditsSummary{
		TotalRemain: remain,
		TotalUsed:   used,
		TotalSize:   total,
	}})
	t.Cleanup(func() { accountCache.Delete(id) })
}

// TestSchedulerPick_LowCreditFirstAndUnknownLast pins the ordering contract:
// a below-floor account is hard-excluded, and an account with no cached
// snapshot (-1) never jumps ahead of a measured one. (The test name predates
// the low-credit-first reversal; current routing is HIGH-CREDIT-FIRST.)
func TestSchedulerPick_LowCreditFirstAndUnknownLast(t *testing.T) {
	resetActiveAuth(t)
	resetFailover(t)
	storeCredits(t, "qw-mid", 300, 0, 300)
	storeCredits(t, "qw-low", 12, 288, 300)
	accountCache.Store("qw-unknown", &accountCacheEntry{})
	defer accountCache.Delete("qw-unknown")
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "qw-unknown", Provider: providerName},
			{ID: "qw-mid", Provider: providerName},
			{ID: "qw-low", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	// qw-low (12 < 20) 低于安全红线被硬排除，qw-mid (300) 排在 qw-unknown (-1) 之前被选中
	if !resp.Handled || resp.AuthID != "qw-mid" {
		t.Fatalf("want healthy qw-mid routed (qw-low < 20 excluded), got %+v", resp)
	}
}

// TestSchedulerPick_UnknownCreditsRankLast: with only a measured account and an
// unmeasured one left, the measured account must win.
func TestSchedulerPick_UnknownCreditsRankLast(t *testing.T) {
	resetActiveAuth(t)
	resetFailover(t)
	storeCredits(t, "qw-known", 900, 0, 900)
	accountCache.Store("qw-unknown", &accountCacheEntry{})
	defer accountCache.Delete("qw-unknown")
	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "qw-unknown", Provider: providerName},
			{ID: "qw-known", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "qw-known" {
		t.Fatalf("want measured qw-known ahead of unknown-credits account, got %+v", resp)
	}
}
