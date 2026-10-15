// growth_handler_test.go 覆盖成长中心管理入口 POST /growth 的四种分支：
// 单账号成功、批量扇出、无匹配账号、Global 账号跳过（零上游请求）。
// 全部通过内存桩 host 接缝 + httptest 本地桩完成，不触碰真实上游与宿主。
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// growthHandlerServer 起一个同时提供签到与成长中心端点的本地桩，并统计成长
// 路径请求数。CN / Global 两个 billing base 都指向它，确保任何误发请求可观测。
// [参数] t：测试上下文。
// [返回] 清理函数与成长端点请求计数。
// 最近修改时间：2026-10-15 22:30:00 新增成长中心管理路由（TASK-3.1）
func growthHandlerServer(t *testing.T) (func(), *int32) {
	t.Helper()
	var growthCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, growthPathPrefix) {
			atomic.AddInt32(&growthCalls, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/billing/meter/checkin-activity-status":
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"active":false,"today_checked_in":true}}`))
		case growthPathPrefix + growthPathTravelStatus:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"state":"arrived","record_id":"r-1"}}`))
		case growthPathPrefix + growthPathTravelClaim:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"reward_credit":20}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{}}`))
		}
	}))
	restoreCN := setBillingBase(srv.URL)
	restoreGlobal := setBillingBaseGlobal(srv.URL)
	return func() {
		restoreGlobal()
		restoreCN()
		srv.Close()
	}, &growthCalls
}

// growthHandlerRequest 构造带可选 auth_index 的管理请求体。
// [参数] authIndex：目标账号索引（空串表示批量）。
// [返回] 可直接交给 handleGrowth 的请求。
// 最近修改时间：2026-10-15 22:30:00 新增成长中心管理路由（TASK-3.1）
func growthHandlerRequest(authIndex string) pluginapi.ManagementRequest {
	body, _ := json.Marshal(map[string]any{"auth_index": authIndex})
	return pluginapi.ManagementRequest{Body: body}
}

// TestHandleGrowth_SingleCN 覆盖单账号模式成功返回收益摘要。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:30:00 新增成长中心管理路由（TASK-3.1）
func TestHandleGrowth_SingleCN(t *testing.T) {
	cleanup, _ := growthHandlerServer(t)
	defer cleanup()
	setGrowthAutoForTest(t, true)

	const authIndex = "idx-single"
	entry := pluginapi.HostAuthFileEntry{ID: "acct-single", AuthIndex: authIndex, Name: "workbuddy-single.json"}
	withGrowthFakeHost(t, []pluginapi.HostAuthFileEntry{entry},
		map[string][]byte{authIndex: growthAuthDoc("uid-single", "www.codebuddy.cn")})
	t.Cleanup(func() {
		accountCache.Delete(entry.ID)
		growthLocks.Delete(authIndex)
	})

	out := handleGrowth(growthHandlerRequest(authIndex))
	results, _ := out["results"].([]map[string]any)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %v", out)
	}
	if results[0]["success"] != true {
		t.Fatalf("expected success, got %v", results[0])
	}
	if got := jsonI64(results[0], "credits_gained"); got != 20 {
		t.Fatalf("credits_gained = %d, want 20", got)
	}
	summary, _ := out["summary"].(map[string]any)
	if jsonI64(summary, "credits_gained") != 20 || summary["success"] != 1 {
		t.Fatalf("unexpected summary: %v", summary)
	}
}

// TestHandleGrowth_NoMatchingAccount 覆盖 auth_index 无匹配时直接返回错误。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:30:00 新增成长中心管理路由（TASK-3.1）
func TestHandleGrowth_NoMatchingAccount(t *testing.T) {
	cleanup, calls := growthHandlerServer(t)
	defer cleanup()
	withGrowthFakeHost(t, nil, nil)

	out := handleGrowth(growthHandlerRequest("missing"))
	if out["error"] == nil {
		t.Fatalf("expected error for unmatched auth_index, got %v", out)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("must not touch upstream when no account matched, got %d", atomic.LoadInt32(calls))
	}
}

// TestHandleGrowth_BatchSkipsGlobal 覆盖批量模式：CN 执行、Global 计入 skipped_global 且零上游请求。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:30:00 新增成长中心管理路由（TASK-3.1）
func TestHandleGrowth_BatchSkipsGlobal(t *testing.T) {
	cleanup, calls := growthHandlerServer(t)
	defer cleanup()
	setGrowthAutoForTest(t, true)

	entries := []pluginapi.HostAuthFileEntry{
		{ID: "acct-cn", AuthIndex: "idx-cn", Name: "workbuddy-cn.json"},
		{ID: "acct-gl", AuthIndex: "idx-gl", Name: "workbuddy-gl.json"},
	}
	docs := map[string][]byte{
		"idx-cn": growthAuthDoc("uid-cn", "www.codebuddy.cn"),
		"idx-gl": growthAuthDoc("uid-gl", "www.workbuddy.ai"),
	}
	withGrowthFakeHost(t, entries, docs)
	t.Cleanup(func() {
		for _, e := range entries {
			accountCache.Delete(e.ID)
			growthLocks.Delete(e.AuthIndex)
		}
	})

	out := handleGrowth(growthHandlerRequest(""))
	results, _ := out["results"].([]map[string]any)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %v", out)
	}
	var cn, global map[string]any
	for _, r := range results {
		switch r["auth_index"] {
		case "idx-cn":
			cn = r
		case "idx-gl":
			global = r
		}
	}
	if cn == nil || cn["success"] != true {
		t.Fatalf("CN account should succeed, got %v", cn)
	}
	if global == nil || global["skipped_global"] != true {
		t.Fatalf("Global account should be skipped_global, got %v", global)
	}
	summary, _ := out["summary"].(map[string]any)
	if summary["skipped_global"] != 1 || summary["success"] != 1 {
		t.Fatalf("unexpected summary: %v", summary)
	}
	// 仅 CN 账号触达成长端点；Global 分支必须提前返回。
	if atomic.LoadInt32(calls) == 0 {
		t.Fatal("CN account should have touched growth endpoints")
	}
}

// TestHandleGrowth_GlobalOnlyBatchZeroUpstream 覆盖全 Global 批量时零上游请求。
// [参数] t：测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-15 22:30:00 新增成长中心管理路由（TASK-3.1）
func TestHandleGrowth_GlobalOnlyBatchZeroUpstream(t *testing.T) {
	cleanup, calls := growthHandlerServer(t)
	defer cleanup()
	setGrowthAutoForTest(t, true)

	entry := pluginapi.HostAuthFileEntry{ID: "acct-gl-only", AuthIndex: "idx-gl-only", Name: "workbuddy-gl-only.json"}
	withGrowthFakeHost(t, []pluginapi.HostAuthFileEntry{entry},
		map[string][]byte{"idx-gl-only": growthAuthDoc("uid-gl-only", "www.workbuddy.ai")})
	t.Cleanup(func() {
		accountCache.Delete(entry.ID)
		growthLocks.Delete(entry.AuthIndex)
	})

	out := handleGrowth(growthHandlerRequest(""))
	summary, _ := out["summary"].(map[string]any)
	if summary["skipped_global"] != 1 {
		t.Fatalf("expected skipped_global=1, got %v", summary)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("Global-only batch must not touch upstream, got %d", atomic.LoadInt32(calls))
	}
}
