package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newGrowthTestServer 起一个 httptest 服务并临时把 billing base 指向它。
// [参数] t：测试上下文；handler：请求处理函数。
// [返回] 清理函数（还原 billing base 并关闭服务）。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func newGrowthTestServer(t *testing.T, handler http.HandlerFunc) func() {
	t.Helper()
	srv := httptest.NewServer(handler)
	restore := setBillingBase(srv.URL)
	return func() {
		restore()
		srv.Close()
	}
}

// TestGrowthCall_GetUnwrapsEnvelope 覆盖 GET 成功路径与 envelope 解包。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func TestGrowthCall_GetUnwrapsEnvelope(t *testing.T) {
	var gotPath, gotMethod string
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"streak_days":7}}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	data, err := growthCall(sa, http.MethodGet, growthPathStreak, nil)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if string(data) != `{"streak_days":7}` {
		t.Fatalf("unexpected data: %s", string(data))
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("expected GET, got %s", gotMethod)
	}
	if want := growthPathPrefix + growthPathStreak; gotPath != want {
		t.Fatalf("expected path %s, got %s", want, gotPath)
	}
}

// TestGrowthCall_PostSendsBodyAndHeaders 覆盖 POST 请求体与鉴权头透传。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func TestGrowthCall_PostSendsBodyAndHeaders(t *testing.T) {
	var gotAuth, gotUserID string
	var gotBody map[string]any
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUserID = r.Header.Get("X-User-Id")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"reward_credit":10}}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	sa.Auth.AccessToken = "tok"
	sa.Account.UID = "uid-1"
	_, err := growthCall(sa, http.MethodPost, "/buddy/travel/claim", map[string]any{"record_id": "r1"})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("expected Bearer token header, got %q", gotAuth)
	}
	if gotUserID != "uid-1" {
		t.Fatalf("expected X-User-Id uid-1, got %q", gotUserID)
	}
	if gotBody["record_id"] != "r1" {
		t.Fatalf("expected record_id r1, got %v", gotBody)
	}
}

// TestGrowthCall_GetRetriesOn5xx 覆盖 GET 遇 5xx 的重试语义。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func TestGrowthCall_GetRetriesOn5xx(t *testing.T) {
	orig := billingRetryDelays
	billingRetryDelays = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { billingRetryDelays = orig }()

	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"ok":true}}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	if _, err := growthCall(sa, http.MethodGet, growthPathStreak, nil); err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls (2 retries), got %d", calls)
	}
}

// TestGrowthCall_PostDoesNotRetryOn5xx 覆盖写操作遇 5xx 不重试的幂等保护。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func TestGrowthCall_PostDoesNotRetryOn5xx(t *testing.T) {
	orig := billingRetryDelays
	billingRetryDelays = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { billingRetryDelays = orig }()

	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()

	sa := &storedAuth{}
	if _, err := growthCall(sa, http.MethodPost, "/lottery/draw", map[string]any{"client_token": "u-1"}); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if calls != 1 {
		t.Fatalf("write op must not retry: expected 1 call, got %d", calls)
	}
}

// TestGrowthCall_NoRetryOn4xx 覆盖业务 4xx 不重试。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func TestGrowthCall_NoRetryOn4xx(t *testing.T) {
	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"msg":"bad request"}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	if _, err := growthCall(sa, http.MethodGet, growthPathStreak, nil); err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry on 4xx), got %d", calls)
	}
}

// TestGrowthCall_BusinessCodeError 覆盖 200 + code!=0 的业务错误语义。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func TestGrowthCall_BusinessCodeError(t *testing.T) {
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":10001,"msg":"already claimed","data":null}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	_, err := growthCall(sa, http.MethodGet, growthPathStreak, nil)
	if err == nil {
		t.Fatal("expected business error, got nil")
	}
	if got := err.Error(); got != "code=10001 msg=already claimed" {
		t.Fatalf("unexpected error text: %s", got)
	}
}

// TestFetchGrowthStreak_ParsesFields 覆盖连登域字段解析（含补登卡两种形态）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func TestFetchGrowthStreak_ParsesFields(t *testing.T) {
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != growthPathPrefix+growthPathStreak {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{
			"launch_date":"2026-09-01",
			"makeup_dates":["2026-10-01","2026-10-03"],
			"streak":{"days":12},
			"makeup_cards":{"balance":2},
			"redemption_status":{"tiers":[{"tier":"7d"},{"tier":"14d"}]}
		}}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	m, err := fetchGrowthStreak(sa)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if got := jsonStr(m, "launch_date"); got != "2026-09-01" {
		t.Fatalf("launch_date = %q", got)
	}
	streak, _ := m["streak"].(map[string]any)
	if got := jsonI64(streak, "days"); got != 12 {
		t.Fatalf("streak.days = %d", got)
	}
	cards, _ := m["makeup_cards"].(map[string]any)
	if got := jsonI64(cards, "balance"); got != 2 {
		t.Fatalf("makeup_cards.balance = %d", got)
	}
	dates, _ := m["makeup_dates"].([]any)
	if len(dates) != 2 {
		t.Fatalf("makeup_dates len = %d", len(dates))
	}
}

// TestFetchGrowthHeatmap_ParsesFields 覆盖热力图字段解析。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func TestFetchGrowthHeatmap_ParsesFields(t *testing.T) {
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"today":{"date":"2026-10-15"},"cells":[{"date":"2026-10-14","score":3}]}}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	m, err := fetchGrowthHeatmap(sa)
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	today, _ := m["today"].(map[string]any)
	if got := jsonStr(today, "date"); got != "2026-10-15" {
		t.Fatalf("today.date = %q", got)
	}
	cells, _ := m["cells"].([]any)
	if len(cells) != 1 {
		t.Fatalf("cells len = %d", len(cells))
	}
}

// TestFetchGrowthReadonlyEndpoints 覆盖连登域三个只读端点的路径与字段解析。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func TestFetchGrowthReadonlyEndpoints(t *testing.T) {
	seen := map[string]bool{}
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = true
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case growthPathPrefix + growthPathEnergy:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"balance":42}}`))
		case growthPathPrefix + growthPathRedeemSummary:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"starter_status":"ready","advanced_status":"locked","legendary_status":"locked"}}`))
		case growthPathPrefix + growthPathLotteryChances:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"balance":3}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer cleanup()

	sa := &storedAuth{}
	energy, err := fetchGrowthEnergy(sa)
	if err != nil || jsonI64(energy, "balance") != 42 {
		t.Fatalf("energy: %v %v", energy, err)
	}
	summary, err := fetchGrowthRedeemSummary(sa)
	if err != nil || jsonStr(summary, "starter_status") != "ready" {
		t.Fatalf("redeem summary: %v %v", summary, err)
	}
	chances, err := fetchGrowthLotteryChances(sa)
	if err != nil || jsonI64(chances, "balance") != 3 {
		t.Fatalf("lottery chances: %v %v", chances, err)
	}
	for _, p := range []string{growthPathEnergy, growthPathRedeemSummary, growthPathLotteryChances} {
		if !seen[growthPathPrefix+p] {
			t.Fatalf("endpoint not hit: %s", p)
		}
	}
}

// TestUseMakeupCard_SendsTargetDate 覆盖补登卡写端点的 body 与单发语义。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func TestUseMakeupCard_SendsTargetDate(t *testing.T) {
	var calls int32
	var gotBody map[string]any
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"makeup_cards":{"balance":1}}}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	m, err := useMakeupCard(sa, "2026-10-03")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if gotBody["target_date"] != "2026-10-03" {
		t.Fatalf("target_date = %v", gotBody)
	}
	cards, _ := m["makeup_cards"].(map[string]any)
	if jsonI64(cards, "balance") != 1 {
		t.Fatalf("balance = %v", m)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// TestUseMakeupCard_RejectsEmptyDate 覆盖空日期参数在发请求前被拦截。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func TestUseMakeupCard_RejectsEmptyDate(t *testing.T) {
	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	})
	defer cleanup()

	if _, err := useMakeupCard(&storedAuth{}, "  "); err == nil {
		t.Fatal("expected error for empty target_date")
	}
	if calls != 0 {
		t.Fatalf("must not send request for invalid input, got %d", calls)
	}
}

// TestRedeemTier_RejectsInvalidTier 覆盖非法档位在发请求前被拦截。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func TestRedeemTier_RejectsInvalidTier(t *testing.T) {
	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	})
	defer cleanup()

	if _, err := redeemTier(&storedAuth{}, "99d"); err == nil {
		t.Fatal("expected error for invalid tier")
	}
	if calls != 0 {
		t.Fatalf("must not send request for invalid tier, got %d", calls)
	}
}

// TestRedeemTier_ValidTierSendsClientToken 覆盖合法档位的 body 结构与 token 形态。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func TestRedeemTier_ValidTierSendsClientToken(t *testing.T) {
	var gotBody map[string]any
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"credit_granted":100}}`))
	})
	defer cleanup()

	sa := &storedAuth{}
	m, err := redeemTier(sa, "7d")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if gotBody["tier"] != "7d" {
		t.Fatalf("tier = %v", gotBody)
	}
	tok, _ := gotBody["client_token"].(string)
	if !strings.HasPrefix(tok, "u-") {
		t.Fatalf("client_token = %q", tok)
	}
	if jsonI64(m, "credit_granted") != 100 {
		t.Fatalf("credit_granted = %v", m)
	}
}

// TestGrowthClientToken_Unique 覆盖连续两次生成的 token 互不相同。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func TestGrowthClientToken_Unique(t *testing.T) {
	a, b := growthClientToken(), growthClientToken()
	if a == b {
		t.Fatalf("expected unique tokens, got duplicate %q", a)
	}
	if !strings.HasPrefix(a, "u-") || !strings.HasPrefix(b, "u-") {
		t.Fatalf("expected u- prefix, got %q %q", a, b)
	}
}

// TestDrawLottery_NoRetryOn5xx 覆盖抽奖写端点 5xx 时只请求一次（保护抽奖次数）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func TestDrawLottery_NoRetryOn5xx(t *testing.T) {
	orig := billingRetryDelays
	billingRetryDelays = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { billingRetryDelays = orig }()

	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()

	if _, err := drawLottery(&storedAuth{}); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if calls != 1 {
		t.Fatalf("lottery draw must not retry: expected 1 call, got %d", calls)
	}
}

// TestFetchGrowthTasks_ParsesList 覆盖任务列表解析。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:20:00 新增长任务域端点（TASK-1.3）
func TestFetchGrowthTasks_ParsesList(t *testing.T) {
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"tasks":[
			{"task_code":"t1","title":"每日对话","locked":false,"accept_status":"not_accepted","reward_credit":10,"reward_energy":2},
			{"task_code":"t2","title":"邀请好友","locked":true,"accept_status":"not_accepted","reward_credit":50,"reward_energy":0}
		]}}`))
	})
	defer cleanup()

	tasks, err := fetchGrowthTasks(&storedAuth{})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks len = %d", len(tasks))
	}
	if tasks[0].TaskCode != "t1" || tasks[0].RewardCredit != 10 || tasks[0].Locked {
		t.Fatalf("task0 = %+v", tasks[0])
	}
	if !tasks[1].Locked {
		t.Fatalf("task1 should be locked: %+v", tasks[1])
	}
}

// TestAcceptGrowthTasks_BatchesAt20 覆盖 25 个任务码按 20+5 分批。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:20:00 新增长任务域端点（TASK-1.3）
func TestAcceptGrowthTasks_BatchesAt20(t *testing.T) {
	var calls int32
	var batchSizes []int
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		var body struct {
			TaskCodes []string `json:"task_codes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		batchSizes = append(batchSizes, len(body.TaskCodes))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"results":[]}}`))
	})
	defer cleanup()

	codes := make([]string, 25)
	for i := range codes {
		codes[i] = fmt.Sprintf("task-%02d", i)
	}
	if _, err := acceptGrowthTasks(&storedAuth{}, codes); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 batches, got %d", calls)
	}
	if len(batchSizes) != 2 || batchSizes[0] != 20 || batchSizes[1] != 5 {
		t.Fatalf("unexpected batch sizes: %v", batchSizes)
	}
}

// TestAcceptGrowthTasks_ReportsPerItemFailures 覆盖逐条 status 判定：上游在
// HTTP 200 + code=0 下用 data.results[i].status 表达单条失败，只看状态码会把
// 整批虚报为成功（生产账号 700fd592 连续 6 轮恒报 14 accepted 的根因，0.16.3）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-17 01:30:00 逐条读 results 判定接单结果（0.16.3）
func TestAcceptGrowthTasks_ReportsPerItemFailures(t *testing.T) {
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"results":[
			{"task_code":"t-ok","status":"ok"},
			{"task_code":"t-bad","status":"error","message":"prerequisite not met: first_buddy"}
		]}}`))
	})
	defer cleanup()

	out, err := acceptGrowthTasks(&storedAuth{}, []string{"t-ok", "t-bad"})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if out.Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1 (only t-ok)", out.Accepted)
	}
	if len(out.Errors) != 1 || !strings.Contains(out.Errors[0], "first_buddy") {
		t.Fatalf("per-item failure reason must surface, got %v", out.Errors)
	}
}

// TestAcceptGrowthTasks_EmptyResultsFallsBackToBatch 覆盖 results 缺失或为空时
// 回退整批成功：无逐条信息即不误报失败（避免把契约省略当成失败）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-17 01:30:00 逐条读 results 判定接单结果（0.16.3）
func TestAcceptGrowthTasks_EmptyResultsFallsBackToBatch(t *testing.T) {
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
	})
	defer cleanup()

	out, err := acceptGrowthTasks(&storedAuth{}, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if out.Accepted != 3 || len(out.Errors) != 0 {
		t.Fatalf("missing results must fall back to batch success, got %+v", out)
	}
}

// TestAcceptGrowthTasks_EmptyListNoRequest 覆盖空列表不发请求。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-17 01:30:00 返回值改为逐条汇总结构（0.16.3）
func TestAcceptGrowthTasks_EmptyListNoRequest(t *testing.T) {
	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	})
	defer cleanup()

	out, err := acceptGrowthTasks(&storedAuth{}, nil)
	if err != nil || out.Accepted != 0 {
		t.Fatalf("expected zero outcome, got %+v err=%v", out, err)
	}
	if calls != 0 {
		t.Fatalf("expected 0 calls, got %d", calls)
	}
}

// TestClaimGrowthTask_EscapesCodeAndHandlesAlreadyClaimed 覆盖任务码转义与重复领奖归一化。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:20:00 新增长任务域端点（TASK-1.3）
func TestClaimGrowthTask_EscapesCodeAndHandlesAlreadyClaimed(t *testing.T) {
	var gotPath string
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"already_claimed":true,"credit":0,"energy":0}}`))
	})
	defer cleanup()

	m, err := claimGrowthTask(&storedAuth{}, "task/with space")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	want := growthPathPrefix + growthPathTasks + "/task%2Fwith%20space/claim"
	if gotPath != want {
		t.Fatalf("escaped path = %q, want %q", gotPath, want)
	}
	if m["success"] != true {
		t.Fatalf("already_claimed should normalize to success: %v", m)
	}
}

// TestClaimGrowthTask_RejectsEmptyCode 覆盖空任务码在发请求前被拦截。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:20:00 新增长任务域端点（TASK-1.3）
func TestClaimGrowthTask_RejectsEmptyCode(t *testing.T) {
	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	})
	defer cleanup()

	if _, err := claimGrowthTask(&storedAuth{}, "   "); err == nil {
		t.Fatal("expected error for empty task_code")
	}
	if calls != 0 {
		t.Fatalf("expected 0 calls, got %d", calls)
	}
}

// TestFetchTravelStatus_ParsesThreeStates 覆盖旅行状态三态字段解析。
// record_id 同时覆盖上游真实数字形态与历史字符串形态：强类型 string 绑定会
// 在数字形态下整段解析失败（0.16.1 修复的线上缺陷）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:40:00 修复旅行域 record_id 类型不匹配（0.16.1）
func TestFetchTravelStatus_ParsesThreeStates(t *testing.T) {
	cases := []struct {
		state string
		idRaw string
	}{
		{"idle", "9001"},
		{"traveling", "9001"},
		{"arrived", "9001"},
		{"arrived", `"r-1"`},
	}
	for _, c := range cases {
		c := c
		cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"state":"` + c.state + `","record_id":` + c.idRaw + `,"daily_limit_reached":false}}`))
		})
		st, err := fetchTravelStatus(&storedAuth{})
		cleanup()
		if err != nil {
			t.Fatalf("state %s id %s: %v", c.state, c.idRaw, err)
		}
		if st.State != c.state || string(st.RecordID) != c.idRaw {
			t.Fatalf("state %s id %s: %+v", c.state, c.idRaw, st)
		}
	}
}

// TestFetchTravelConfig_ParsesLocations 覆盖目的地配置解析。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:40:00 修复旅行域 record_id 类型不匹配（0.16.1）
func TestFetchTravelConfig_ParsesLocations(t *testing.T) {
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"locations":[
			{"id":"loc-1","name":"西湖","duration_hours":4},
			{"id":"loc-2","name":"故宫","duration_hours":8}
		]}}`))
	})
	defer cleanup()

	locs, err := fetchTravelConfig(&storedAuth{})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if len(locs) != 2 || string(locs[0].ID) != `"loc-1"` || locs[0].DurationHours != 4 {
		t.Fatalf("locations = %+v", locs)
	}
}

// TestClaimTravelReward_SendsRecordID 覆盖收礼写端点 body 与单发语义。
// 用上游真实数字形态断言 body 内 record_id 仍为数字（不被强转成字符串）。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:40:00 修复旅行域 record_id 类型不匹配（0.16.1）
func TestClaimTravelReward_SendsRecordID(t *testing.T) {
	var calls int32
	var gotBody map[string]any
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"reward_credit":20}}`))
	})
	defer cleanup()

	m, err := claimTravelReward(&storedAuth{}, json.RawMessage("9001"))
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if gotBody["record_id"] != float64(9001) || jsonI64(m, "reward_credit") != 20 {
		t.Fatalf("body=%v resp=%v", gotBody, m)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// TestTravelWriteEndpoints_RejectEmptyIDs 覆盖空 ID 在发请求前被拦截。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:40:00 修复旅行域 record_id 类型不匹配（0.16.1）
func TestTravelWriteEndpoints_RejectEmptyIDs(t *testing.T) {
	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
	})
	defer cleanup()

	for _, empty := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage(`""`), json.RawMessage(`"  "`)} {
		if _, err := claimTravelReward(&storedAuth{}, empty); err == nil {
			t.Fatalf("expected error for empty record_id %q", string(empty))
		}
		if _, err := departBuddy(&storedAuth{}, empty); err == nil {
			t.Fatalf("expected error for empty location_id %q", string(empty))
		}
	}
	if calls != 0 {
		t.Fatalf("expected 0 calls, got %d", calls)
	}
}

// TestDepartBuddy_SendsLocationID 覆盖派出写端点 body 与单发语义。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:40:00 修复旅行域 record_id 类型不匹配（0.16.1）
func TestDepartBuddy_SendsLocationID(t *testing.T) {
	var calls int32
	var gotBody map[string]any
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"location":{"name":"西湖"},"duration_hours":4}}`))
	})
	defer cleanup()

	if _, err := departBuddy(&storedAuth{}, json.RawMessage(`"loc-1"`)); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if gotBody["location_id"] != "loc-1" {
		t.Fatalf("location_id = %v", gotBody)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// TestFetchBuddyQuota_AndOpenBox 覆盖盲盒额度读取与开盒写端点。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:30:00 新增 Buddy 旅行域端点（TASK-1.4）
func TestFetchBuddyQuota_AndOpenBox(t *testing.T) {
	var gotBody map[string]any
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case growthPathPrefix + growthPathBuddyQuota:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"affordable":true,"max_open_count":3}}`))
		case growthPathPrefix + growthPathBuddyOpen:
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"buddies":[{"name":"小Buddy"}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer cleanup()

	q, err := fetchBuddyQuota(&storedAuth{})
	if err != nil || !jsonBool(q, "affordable") || jsonI64(q, "max_open_count") != 3 {
		t.Fatalf("quota: %v %v", q, err)
	}
	if _, err := openBuddyBox(&storedAuth{}, 0); err != nil {
		t.Fatalf("open box: %v", err)
	}
	// count<=0 归一化为 1；client_token 必须存在。
	if gotBody["count"] != float64(1) {
		t.Fatalf("count = %v", gotBody["count"])
	}
	if tok, _ := gotBody["client_token"].(string); !strings.HasPrefix(tok, "u-") {
		t.Fatalf("client_token = %v", gotBody["client_token"])
	}
}

// TestOpenBuddyBox_NoRetryOn5xx 覆盖开盒写端点 5xx 时只请求一次。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 21:30:00 新增 Buddy 旅行域端点（TASK-1.4）
func TestOpenBuddyBox_NoRetryOn5xx(t *testing.T) {
	orig := billingRetryDelays
	billingRetryDelays = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { billingRetryDelays = orig }()

	var calls int32
	cleanup := newGrowthTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()

	if _, err := openBuddyBox(&storedAuth{}, 2); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if calls != 1 {
		t.Fatalf("open box must not retry: expected 1 call, got %d", calls)
	}
}
