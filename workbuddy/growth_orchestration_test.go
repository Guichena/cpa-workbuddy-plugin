package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// growthFullStub 起一个同时提供成长中心全部分段端点的 httptest 服务。
// 通过 counters 记录每个写端点的请求次数，供幂等与限速断言使用。
// [参数] t：测试上下文；failPath：需要返回 500 的路径（空串表示全绿）。
// [返回] 清理函数与写端点请求计数 map。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthFullStub(t *testing.T, failPath string) (func(), map[string]*int32) {
	t.Helper()
	counters := map[string]*int32{}
	counterFor := func(path string) *int32 {
		if c, ok := counters[path]; ok {
			return c
		}
		c := new(int32)
		counters[path] = c
		return c
	}
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == http.MethodPost {
			atomic.AddInt32(counterFor(path), 1)
		}
		if failPath != "" && path == failPath {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(growthStubBody(path)))
	})
	return cleanup, counters
}

// growthStubBody 返回各端点对应的成功响应体（data 内字段对齐 EXT-1 契约）。
// [参数] path：请求路径。
// [返回] envelope JSON 字符串。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthStubBody(path string) string {
	switch path {
	case "/v2/billing/meter/checkin-activity-status":
		return `{"code":0,"msg":"OK","data":{"active":true,"today_checked_in":false}}`
	case "/v2/billing/meter/daily-checkin":
		return `{"code":0,"msg":"OK","data":{"credit":5}}`
	case growthPathPrefix + growthPathTravelStatus:
		return `{"code":0,"msg":"OK","data":{"state":"arrived","record_id":9001,"arrive_at":1760000000,"server_now":1759990000,"daily_limit_reached":false}}`
	case growthPathPrefix + growthPathTravelClaim:
		return `{"code":0,"msg":"OK","data":{"reward_credit":20}}`
	case growthPathPrefix + growthPathTravelConfig:
		return `{"code":0,"msg":"OK","data":{"locations":[{"id":"loc-1","name":"Xihu","duration_hours":4}]}}`
	case growthPathPrefix + growthPathTravelDepart:
		return `{"code":0,"msg":"OK","data":{"location":{"name":"Xihu"},"duration_hours":4}}`
	case growthPathPrefix + growthPathTasks:
		return `{"code":0,"msg":"OK","data":{"tasks":[{"task_code":"t-open","locked":false,"accept_status":"not_accepted","reward_credit":10},{"task_code":"t-done","locked":false,"accept_status":"completed","reward_credit":30}]}}`
	case growthPathPrefix + growthPathTasksAccept:
		return `{"code":0,"msg":"OK","data":{"results":[]}}`
	case growthPathPrefix + "/tasks/t-done/claim":
		return `{"code":0,"msg":"OK","data":{"credit":30,"energy":1}}`
	case growthPathPrefix + growthPathStreak:
		return `{"code":0,"msg":"OK","data":{"launch_date":"2026-09-01","makeup_dates":[],"streak":{"days":12},"makeup_cards":{"balance":1}}}`
	case growthPathPrefix + growthPathHeatmap:
		return `{"code":0,"msg":"OK","data":{"cells":[{"date":"2026-10-15","score":1},{"date":"2026-10-14","score":1}]}}`
	case growthPathPrefix + growthPathMakeupCardsUse:
		return `{"code":0,"msg":"OK","data":{"makeup_cards":{"balance":0}}}`
	case growthPathPrefix + growthPathRedeemSummary:
		return `{"code":0,"msg":"OK","data":{"starter_status":"ready","advanced_status":"locked","legendary_status":"locked"}}`
	case growthPathPrefix + growthPathRedeem:
		return `{"code":0,"msg":"OK","data":{"credit_granted":100,"energy_granted":2}}`
	case growthPathPrefix + growthPathLotteryChances:
		return `{"code":0,"msg":"OK","data":{"balance":2}}`
	case growthPathPrefix + growthPathLotteryDraw:
		return `{"code":0,"msg":"OK","data":{"prize_name":"small","credit":1}}`
	case growthPathPrefix + growthPathBuddyQuota:
		return `{"code":0,"msg":"OK","data":{"affordable":true,"max_open_count":3}}`
	case growthPathPrefix + growthPathBuddyOpen:
		return `{"code":0,"msg":"OK","data":{"energy":3,"credit":0}}`
	case growthPathPrefix + growthPathEnergy:
		return `{"code":0,"msg":"OK","data":{"balance":42}}`
	}
	return `{"code":0,"msg":"OK","data":{}}`
}

// TestRunGrowthForAccount_AllSegmentsGreen 覆盖全绿路径与收益累加。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestRunGrowthForAccount_AllSegmentsGreen(t *testing.T) {
	cleanup, _ := growthFullStub(t, "")
	defer cleanup()

	res := runGrowthForAccount(&storedAuth{}, "idx-1")
	if len(res.Errors) != 0 {
		t.Fatalf("expected no errors, got %v", res.Errors)
	}
	// 签到 5 + 旅行收礼 20 + 任务领奖 30 + 兑换 100 + 抽奖 1 = 156
	if res.CreditsGained != 156 {
		t.Fatalf("CreditsGained = %d, want 156 (segments=%v)", res.CreditsGained, res.Segments)
	}
	// 能量收益只统计各段实发值（任务 1 + 兑换 2 + 盲盒 3 = 6）；
	// /energy 的 42 是余额快照，必须落在 EnergyBalance 而非 EnergyGained（0.16.2 回归）。
	if res.EnergyGained != 6 {
		t.Fatalf("EnergyGained = %d, want 6 (segments=%v)", res.EnergyGained, res.Segments)
	}
	if res.EnergyBalance != 42 {
		t.Fatalf("EnergyBalance = %d, want 42", res.EnergyBalance)
	}
	if res.TasksAccepted != 1 || res.TasksClaimed != 1 {
		t.Fatalf("tasks accepted=%d claimed=%d", res.TasksAccepted, res.TasksClaimed)
	}
	for _, want := range []string{"checkin", "travel_claim", "travel_depart", "tasks_accept", "tasks_claim", "makeup:", "redeem:7d", "lottery:", "buddy_open"} {
		found := false
		for _, s := range res.Segments {
			if strings.HasPrefix(s, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing segment %q in %v", want, res.Segments)
		}
	}
}

// TestRunGrowthForAccount_SegmentIsolation 覆盖单段失败不影响其余段。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestRunGrowthForAccount_SegmentIsolation(t *testing.T) {
	cleanup, counters := growthFullStub(t, growthPathPrefix+growthPathTasksAccept)
	defer cleanup()

	res := runGrowthForAccount(&storedAuth{}, "idx-2")
	hasAcceptErr := false
	for _, e := range res.Errors {
		if strings.HasPrefix(e, "tasks_accept:") {
			hasAcceptErr = true
		}
	}
	if !hasAcceptErr {
		t.Fatalf("expected tasks_accept error, got %v", res.Errors)
	}
	for _, p := range []string{growthPathTravelClaim, growthPathTravelDepart, growthPathMakeupCardsUse, growthPathRedeem, growthPathLotteryDraw, growthPathBuddyOpen} {
		if got := atomic.LoadInt32(counters[growthPathPrefix+p]); got != 1 {
			t.Fatalf("segment %s should still run once, got %d", p, got)
		}
	}
}

// TestRunGrowthForAccount_IdempotentSecondRun 覆盖二次执行不重复调用写端点。
// 第一次执行后补登卡余额、抽奖次数、任务状态、旅行状态均已收敛，第二次不应再发写请求。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestRunGrowthForAccount_IdempotentSecondRun(t *testing.T) {
	var checkedIn, makeupUsed, lotteryDrawn, redeemed, departed, taskClaimed int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		ok := func(data string) { _, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":` + data + `}`)) }
		switch path {
		case "/v2/billing/meter/checkin-activity-status":
			ok(fmt.Sprintf(`{"active":true,"today_checked_in":%t}`, atomic.LoadInt32(&checkedIn) == 1))
		case "/v2/billing/meter/daily-checkin":
			atomic.StoreInt32(&checkedIn, 1)
			ok(`{"credit":5}`)
		case growthPathPrefix + growthPathTravelStatus:
			state := "arrived"
			if atomic.LoadInt32(&departed) == 1 {
				state = "traveling"
			}
			ok(fmt.Sprintf(`{"state":"%s","record_id":9001,"daily_limit_reached":false}`, state))
		case growthPathPrefix + growthPathTravelClaim:
			ok(`{"reward_credit":20}`)
		case growthPathPrefix + growthPathTravelConfig:
			ok(`{"locations":[{"id":"loc-1"}]}`)
		case growthPathPrefix + growthPathTravelDepart:
			atomic.StoreInt32(&departed, 1)
			ok(`{}`)
		case growthPathPrefix + growthPathTasks:
			status := "completed"
			if atomic.LoadInt32(&taskClaimed) == 1 {
				status = "claimed"
			}
			ok(fmt.Sprintf(`{"tasks":[{"task_code":"t-done","locked":false,"accept_status":"%s","reward_credit":30}]}`, status))
		case growthPathPrefix + "/tasks/t-done/claim":
			atomic.StoreInt32(&taskClaimed, 1)
			ok(`{"credit":30}`)
		case growthPathPrefix + growthPathStreak:
			balance := 1
			if atomic.LoadInt32(&makeupUsed) == 1 {
				balance = 0
			}
			ok(fmt.Sprintf(`{"launch_date":"2026-09-01","makeup_dates":[],"streak":{"days":12},"makeup_cards":{"balance":%d}}`, balance))
		case growthPathPrefix + growthPathHeatmap:
			ok(`{"cells":[{"date":"2026-10-15","score":1},{"date":"2026-10-14","score":1}]}`)
		case growthPathPrefix + growthPathMakeupCardsUse:
			atomic.StoreInt32(&makeupUsed, 1)
			ok(`{"makeup_cards":{"balance":0}}`)
		case growthPathPrefix + growthPathRedeemSummary:
			st := "ready"
			if atomic.LoadInt32(&redeemed) == 1 {
				st = "claimed"
			}
			ok(fmt.Sprintf(`{"starter_status":"%s","advanced_status":"locked","legendary_status":"locked"}`, st))
		case growthPathPrefix + growthPathRedeem:
			atomic.StoreInt32(&redeemed, 1)
			ok(`{"credit_granted":100}`)
		case growthPathPrefix + growthPathLotteryChances:
			b := 2
			if atomic.LoadInt32(&lotteryDrawn) == 1 {
				b = 0
			}
			ok(fmt.Sprintf(`{"balance":%d}`, b))
		case growthPathPrefix + growthPathLotteryDraw:
			atomic.StoreInt32(&lotteryDrawn, 1)
			ok(`{"prize_name":"small"}`)
		case growthPathPrefix + growthPathBuddyQuota:
			ok(`{"affordable":false,"max_open_count":0}`)
		case growthPathPrefix + growthPathEnergy:
			ok(`{"balance":42}`)
		default:
			ok(`{}`)
		}
	})
	defer cleanup()

	first := runGrowthForAccount(&storedAuth{}, "idx-3")
	if first.CreditsGained == 0 {
		t.Fatalf("first run should gain credits, segments=%v errors=%v", first.Segments, first.Errors)
	}

	second := runGrowthForAccount(&storedAuth{}, "idx-3")
	if second.TasksClaimed != 0 {
		t.Fatalf("second run must not re-claim tasks, got %d", second.TasksClaimed)
	}
	// 生产症状回归（0.16.2）：零领取轮次不得报能量收益；余额 42 只能进 EnergyBalance。
	// 修复前此处 EnergyGained 被 /energy 的 balance 污染为 42，面板显示「+42 能量」。
	if second.EnergyGained != 0 {
		t.Fatalf("zero-claim run must not report energy gain, got %d (segments=%v)", second.EnergyGained, second.Segments)
	}
	if second.EnergyBalance != 42 {
		t.Fatalf("EnergyBalance = %d, want 42", second.EnergyBalance)
	}
	if atomic.LoadInt32(&checkedIn) != 1 || atomic.LoadInt32(&makeupUsed) != 1 || atomic.LoadInt32(&lotteryDrawn) != 1 || atomic.LoadInt32(&redeemed) != 1 {
		t.Fatalf("write endpoints re-fired: checkedIn=%d makeup=%d lottery=%d redeemed=%d",
			checkedIn, makeupUsed, lotteryDrawn, redeemed)
	}
	if atomic.LoadInt32(&departed) != 1 {
		t.Fatalf("travel should not re-depart, departed=%d", departed)
	}
}

// TestRunGrowthForAccount_MakeupAndLotteryRateLimit 覆盖补登卡与抽奖每轮最多一次。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestRunGrowthForAccount_MakeupAndLotteryRateLimit(t *testing.T) {
	cleanup, counters := growthFullStub(t, "")
	defer cleanup()

	runGrowthForAccount(&storedAuth{}, "idx-4")
	if got := atomic.LoadInt32(counters[growthPathPrefix+growthPathMakeupCardsUse]); got != 1 {
		t.Fatalf("makeup card must be used at most once per run, got %d", got)
	}
	if got := atomic.LoadInt32(counters[growthPathPrefix+growthPathLotteryDraw]); got != 1 {
		t.Fatalf("lottery must be drawn at most once per run, got %d", got)
	}
}

// TestRunGrowthForAccount_TravelSkipsWhenDailyLimitReached 覆盖到达上限时不派出。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestRunGrowthForAccount_TravelSkipsWhenDailyLimitReached(t *testing.T) {
	var departed int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case growthPathPrefix + growthPathTravelStatus:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"state":"arrived","record_id":9001,"daily_limit_reached":true}}`))
		case growthPathPrefix + growthPathTravelClaim:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"reward_credit":20}}`))
		case growthPathPrefix + growthPathTravelConfig:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"locations":[{"id":"loc-1"}]}}`))
		case growthPathPrefix + growthPathTravelDepart:
			atomic.AddInt32(&departed, 1)
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
		case "/v2/billing/meter/checkin-activity-status":
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"active":false,"today_checked_in":true}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
		}
	})
	defer cleanup()

	runGrowthForAccount(&storedAuth{}, "idx-5")
	if atomic.LoadInt32(&departed) != 0 {
		t.Fatalf("must not depart when daily limit reached, got %d", departed)
	}
}

// TestGrowthMakeupTarget_NoGapReturnsEmpty 覆盖无断登缺口时返回空串。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestGrowthMakeupTarget_NoGapReturnsEmpty(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	streak := map[string]any{
		"launch_date":  "2026-10-01",
		"makeup_dates": []any{},
		"makeup_cards": map[string]any{"balance": float64(2)},
	}
	var cells []any
	for d := 1; d <= 14; d++ {
		cells = append(cells, map[string]any{"date": fmt.Sprintf("2026-10-%02d", d), "score": float64(1)})
	}
	heatmap := map[string]any{"cells": cells}
	if got := growthMakeupTarget(streak, heatmap, now); got != "" {
		t.Fatalf("expected empty target, got %q", got)
	}
}

// TestGrowthMakeupTarget_ZeroBalanceReturnsEmpty 覆盖补登卡余额为 0 时不补登。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestGrowthMakeupTarget_ZeroBalanceReturnsEmpty(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	streak := map[string]any{"makeup_cards": map[string]any{"balance": float64(0)}}
	heatmap := map[string]any{"cells": []any{}}
	if got := growthMakeupTarget(streak, heatmap, now); got != "" {
		t.Fatalf("expected empty target with zero balance, got %q", got)
	}
}

// TestGrowthMakeupTarget_PicksEarliestGap 覆盖定位当月最早断登日。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestGrowthMakeupTarget_PicksEarliestGap(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	streak := map[string]any{
		"launch_date":  "2026-10-01",
		"makeup_dates": []any{"2026-10-02"},
		"makeup_cards": map[string]any{"balance": float64(3)},
	}
	heatmap := map[string]any{"cells": []any{
		map[string]any{"date": "2026-10-01", "score": float64(1)},
		map[string]any{"date": "2026-10-15", "score": float64(1)},
	}}
	if got := growthMakeupTarget(streak, heatmap, now); got != "2026-10-03" {
		t.Fatalf("expected 2026-10-03, got %q", got)
	}
}

// TestGrowthStatusRedeemable 覆盖兑换状态判定的保守语义。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestGrowthStatusRedeemable(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true},
		{false, false},
		{"ready", true},
		{"locked", false},
		{"claimed", false},
		{nil, false},
		{1.0, false},
	}
	for _, c := range cases {
		if got := growthStatusRedeemable(c.in); got != c.want {
			t.Errorf("growthStatusRedeemable(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestRunGrowthForAccount_DeadlineSkipsRemainingSegments 覆盖超时预算触发后不再发新请求。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func TestRunGrowthForAccount_DeadlineSkipsRemainingSegments(t *testing.T) {
	orig := growthBudget
	growthBudget = -1 * time.Second
	defer func() { growthBudget = orig }()

	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
	})
	defer cleanup()

	res := runGrowthForAccount(&storedAuth{}, "idx-6")
	if res == nil {
		t.Fatal("expected non-nil result even when budget exhausted")
	}
	if calls != 0 {
		t.Fatalf("expired budget must skip all segments, got %d calls", calls)
	}
}
