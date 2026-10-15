package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// pickForTest 调用 handleSchedulerPick 并解出响应体，供各用例复用。
// [参数] t: 测试句柄；candidates: 宿主下发的候选集。
// [返回] 调度响应。
// 最近修改时间：2026-10-15 21:56:00；改动原因：新增，调度器候选集语义的测试入口。
func pickForTest(t *testing.T, candidates []pluginapi.SchedulerAuthCandidate) pluginapi.SchedulerPickResponse {
	t.Helper()

	// 1. 序列化宿主调度请求并调用插件入口。
	raw, err := json.Marshal(pluginapi.SchedulerPickRequest{Candidates: candidates})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	out, err := handleSchedulerPick(raw)
	if err != nil {
		t.Fatalf("handleSchedulerPick: %v", err)
	}

	// 2. 解出响应信封并确认成功标记。
	var env envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("envelope not ok: %s", string(out))
	}

	// 3. 解出调度响应供各用例断言。
	var resp pluginapi.SchedulerPickResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

// TestHandleSchedulerPickReturnsCandidateIDVerbatim 校验返回的 AuthID 逐字等于宿主候选 ID（含 .json）。
// 判据：宿主以 strings.TrimSpace(candidate.ID) == authID 精确比较，裁剪扩展名或改用自有账号池 ID 会重现 unknown auth id。无副作用。
func TestHandleSchedulerPickReturnsCandidateIDVerbatim(t *testing.T) {
	resp := pickForTest(t, []pluginapi.SchedulerAuthCandidate{
		{ID: "copilot-f13aadb583ef01ba.json", Provider: providerName, Status: "active"},
	})
	if !resp.Handled {
		t.Fatal("expected handled response for in-scope candidate")
	}
	if resp.AuthID != "copilot-f13aadb583ef01ba.json" {
		t.Fatalf("AuthID=%q, want verbatim candidate ID %q", resp.AuthID, "copilot-f13aadb583ef01ba.json")
	}
}

// TestHandleSchedulerPickSkipsForeignProvider 校验候选全为其它 provider 时让位内建调度。
func TestHandleSchedulerPickSkipsForeignProvider(t *testing.T) {
	resp := pickForTest(t, []pluginapi.SchedulerAuthCandidate{
		{ID: "workbuddy-137aba20-ab91-410a-8009-6d97830868ff.json", Provider: "workbuddy-provider", Status: "active"},
		{ID: "antigravity-luode0320@gmail.com.json", Provider: "antigravity", Status: "active"},
	})
	if resp.Handled {
		t.Fatalf("must defer to built-in scheduler for foreign providers, got AuthID=%q", resp.AuthID)
	}
}

// TestHandleSchedulerPickSkipsDisabled 校验禁用候选被跳过，全禁用时让位内建调度。
func TestHandleSchedulerPickSkipsDisabled(t *testing.T) {
	// 1. 禁用与可用混合：只应选中可用项。
	resp := pickForTest(t, []pluginapi.SchedulerAuthCandidate{
		{ID: "copilot-disabled.json", Provider: providerName, Status: "disabled"},
		{ID: "copilot-healthy.json", Provider: providerName, Status: "active"},
	})
	if !resp.Handled || resp.AuthID != "copilot-healthy.json" {
		t.Fatalf("expected healthy candidate, got handled=%v AuthID=%q", resp.Handled, resp.AuthID)
	}

	// 2. Metadata.disabled=true 同样视为禁用。
	resp = pickForTest(t, []pluginapi.SchedulerAuthCandidate{
		{ID: "copilot-md.json", Provider: providerName, Status: "active", Metadata: map[string]any{"disabled": true}},
		{ID: "copilot-ok.json", Provider: providerName, Status: "active"},
	})
	if !resp.Handled || resp.AuthID != "copilot-ok.json" {
		t.Fatalf("expected metadata-disabled candidate skipped, got handled=%v AuthID=%q", resp.Handled, resp.AuthID)
	}

	// 3. 全部禁用：让位内建调度。
	resp = pickForTest(t, []pluginapi.SchedulerAuthCandidate{
		{ID: "copilot-disabled.json", Provider: providerName, Status: "disabled"},
	})
	if resp.Handled {
		t.Fatalf("must defer when every candidate is disabled, got AuthID=%q", resp.AuthID)
	}
}

// TestHandleSchedulerPickSkipsCoolingDown 校验冷却候选被跳过，全冷却时让位内建调度。
func TestHandleSchedulerPickSkipsCoolingDown(t *testing.T) {
	noteAccountFailure("copilot-cooling", 429, "rate limit")
	defer resetAccountFailover("copilot-cooling")

	// 1. 冷却项跳过，可用项仍可选中；宿主候选 ID 带 .json，冷却 key 不带。
	resp := pickForTest(t, []pluginapi.SchedulerAuthCandidate{
		{ID: "copilot-cooling.json", Provider: providerName, Status: "active"},
		{ID: "copilot-fresh.json", Provider: providerName, Status: "active"},
	})
	if !resp.Handled || resp.AuthID != "copilot-fresh.json" {
		t.Fatalf("expected cooling candidate skipped, got handled=%v AuthID=%q", resp.Handled, resp.AuthID)
	}

	// 2. 全部冷却：让位内建调度。
	resp = pickForTest(t, []pluginapi.SchedulerAuthCandidate{
		{ID: "copilot-cooling.json", Provider: providerName, Status: "active"},
	})
	if resp.Handled {
		t.Fatalf("must defer when every candidate is cooling down, got AuthID=%q", resp.AuthID)
	}
}

// TestHandleSchedulerPickNoCandidates 校验空候选集让位内建调度。
func TestHandleSchedulerPickNoCandidates(t *testing.T) {
	if resp := pickForTest(t, nil); resp.Handled {
		t.Fatalf("must defer on empty candidates, got AuthID=%q", resp.AuthID)
	}
}

// TestHandleSchedulerPickRoundRobinWithinCandidates 校验多候选轮转且结果始终落在候选集内。
func TestHandleSchedulerPickRoundRobinWithinCandidates(t *testing.T) {
	candidates := []pluginapi.SchedulerAuthCandidate{
		{ID: "copilot-a.json", Provider: providerName, Status: "active"},
		{ID: "copilot-b.json", Provider: providerName, Status: "active"},
	}
	inScope := map[string]bool{"copilot-a.json": true, "copilot-b.json": true}
	seen := map[string]int{}
	for i := 0; i < 4; i++ {
		resp := pickForTest(t, candidates)
		if !resp.Handled {
			t.Fatal("expected handled response")
		}
		if !inScope[resp.AuthID] {
			t.Fatalf("AuthID=%q escaped candidate set", resp.AuthID)
		}
		seen[resp.AuthID]++
	}
	if len(seen) != 2 {
		t.Fatalf("expected round-robin across both candidates, got %v", seen)
	}
}
