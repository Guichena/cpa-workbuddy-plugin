package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// TestSchedulerPick_SkipsDailyCoolingAccounts 验证处于 24h 每日冷却中的账号被调度器直接排除。
func TestSchedulerPick_SkipsDailyCoolingAccounts(t *testing.T) {
	resetActiveAuth(t)
	resetFailover(t)

	dailyCoolingMu.Lock()
	dailyCoolingMap = make(map[string]time.Time)
	dailyCoolingMu.Unlock()

	// 准备两个账号，一个冷却中（积分低本应优先选中），一个健康（积分高）
	storeCredits(t, "qai-cooling", 10, 0, 100)
	storeCredits(t, "qai-healthy", 90, 0, 100)

	future := time.Now().Add(12 * time.Hour)
	dailyCoolingMu.Lock()
	dailyCoolingMap["qai-cooling"] = future
	dailyCoolingMu.Unlock()

	raw, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "qai-cooling", Provider: providerName},
			{ID: "qai-healthy", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := parsePickResponse(t, raw)
	if !resp.Handled || resp.AuthID != "qai-healthy" {
		t.Fatalf("want healthy account picked, got %+v", resp)
	}

	// 若两个账号都在每日冷却中，调度器必须 defer (Handled: false)
	dailyCoolingMu.Lock()
	dailyCoolingMap["qai-healthy"] = future
	dailyCoolingMu.Unlock()

	rawAll, err := handleSchedulerPick(mustMarshal(t, pluginapi.SchedulerPickRequest{
		Provider: providerName,
		Candidates: []pluginapi.SchedulerAuthCandidate{
			{ID: "qai-cooling", Provider: providerName},
			{ID: "qai-healthy", Provider: providerName},
		},
	}))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	respAll := parsePickResponse(t, rawAll)
	if respAll.Handled {
		t.Fatalf("expected all-cooling fleet to defer, got %+v", respAll)
	}
}

// TestResetAccountFailover_PreservesDailyCooling 验证常规成功重置绝不消除 24h 每日冷却。
func TestResetAccountFailover_PreservesDailyCooling(t *testing.T) {
	resetFailover(t)
	dailyCoolingMu.Lock()
	dailyCoolingMap = make(map[string]time.Time)
	dailyCoolingMu.Unlock()

	authID := "qai-test-daily"
	future := time.Now().Add(24 * time.Hour)
	dailyCoolingMu.Lock()
	dailyCoolingMap[authID] = future
	dailyCoolingMu.Unlock()

	// 记录常规失败，使其产生常规失败计数与 15s 冷却
	recordAccountFailure(authID, http.StatusBadGateway, "bad gateway")

	// 验证处于冷却中
	if !isAccountCoolingDown(authID) {
		t.Fatalf("expected account to be cooling down")
	}

	// 模拟一次成功调用（触发常规重置）
	resetAccountFailover(authID)

	// 常规计数应被重置为 0
	count, _, _ := failoverStateSnapshot(authID)
	if count != 0 {
		t.Fatalf("expected fail count 0 after reset, got %d", count)
	}

	// 核心断言：24h 每日冷却必须依然有效！
	if !isAccountDailyCooling(authID) {
		t.Fatalf("expected daily cooling to persist across normal failover reset")
	}
	if !isAccountCoolingDown(authID) {
		t.Fatalf("expected account to still be excluded from routing")
	}
}

// TestIsAccountFailure_DailyCountExceeded 验证 110 错误码被分类为账号级故障，确保换号重试逻辑放行。
func TestIsAccountFailure_DailyCountExceeded(t *testing.T) {
	err110 := "upstream 200: 110: Billing daily count exceeded"
	if !isAccountFailure(http.StatusOK, err110) {
		t.Fatalf("isAccountFailure must return true for 110 on HTTP 200")
	}
	if !shouldRotateOnUpstreamErr(http.StatusOK, err110) {
		t.Fatalf("shouldRotateOnUpstreamErr must return true for 110 to trigger account rotation")
	}
}
