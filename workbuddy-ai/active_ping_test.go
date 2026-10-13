package main

import (
	"errors"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// TestHandleTestActiveWithAuthSpecifiedModel 验证「测试」弹窗点选模型后的
// 指定模型透传，以及未指定时回退随机模型的向后兼容行为。
func TestHandleTestActiveWithAuthSpecifiedModel(t *testing.T) {
	origPingFn := sendActivePingWorkbuddyFn
	defer func() { sendActivePingWorkbuddyFn = origPingFn }()

	sa := &storedAuth{Account: storedAccount{UID: "wbai-user-spec"}}
	var calledModel string
	sendActivePingWorkbuddyFn = func(sa *storedAuth, chosenModel string) error {
		calledModel = chosenModel
		return nil
	}

	// 1. 指定模型必须原样透传给上游发送函数并回显在结果里
	resSpec := handleTestActiveWithAuth(sa, "test-index", "gpt-6-astra")
	if resSpec["ok"] != true || calledModel != "gpt-6-astra" {
		t.Fatalf("expected specified model passthrough, got res=%v called=%q", resSpec, calledModel)
	}
	if resSpec["model"] != "gpt-6-astra" {
		t.Fatalf("expected echoed model gpt-6-astra, got %v", resSpec["model"])
	}

	// 2. 模型为空时回退随机模型（缓存清空 + 无 token → 兜底默认值），兼容旧面板
	storeDynamicModels(nil)
	resAuto := handleTestActiveWithAuth(sa, "test-index", "")
	if resAuto["ok"] != true || calledModel != "deepseek-v4.1-flash" {
		t.Fatalf("expected fallback random model, got res=%v called=%q", resAuto, calledModel)
	}
}

// TestPickRandomWorkbuddyModels 验证多模型随机挑选的上限、去重与 fallback 行为。
func TestPickRandomWorkbuddyModels(t *testing.T) {
	storeDynamicModels(nil)
	saEmpty := &storedAuth{}
	resFallback := pickRandomWorkbuddyModels(saEmpty, 5)
	if len(resFallback) != 1 || resFallback[0] != "deepseek-v4.1-flash" {
		t.Fatalf("expected fallback model, got %v", resFallback)
	}

	fakeModels := []pluginapi.ModelInfo{
		{ID: "m1"}, {ID: "m2"}, {ID: "m3"}, {ID: "m4"}, {ID: "m5"}, {ID: "m6"}, {ID: "m1"},
	}
	storeDynamicModels(fakeModels)
	defer storeDynamicModels(nil)

	res5 := pickRandomWorkbuddyModels(saEmpty, 5)
	if len(res5) != 5 {
		t.Fatalf("expected 5 models, got %d (%v)", len(res5), res5)
	}
	seen := make(map[string]bool)
	for _, m := range res5 {
		if seen[m] {
			t.Fatalf("unexpected duplicate model %s in %v", m, res5)
		}
		seen[m] = true
	}

	res2 := pickRandomWorkbuddyModels(saEmpty, 2)
	if len(res2) != 2 {
		t.Fatalf("expected 2 models, got %d (%v)", len(res2), res2)
	}
}

// TestDoActivePingMultiModelAnySuccess 验证多模型轮测中任意一个成功即判定健康且立即终止后续测试。
func TestDoActivePingMultiModelAnySuccess(t *testing.T) {
	resetActivePingTimes()
	defer resetActivePingTimes()

	fakeModels := []pluginapi.ModelInfo{
		{ID: "fail-1"}, {ID: "fail-2"}, {ID: "succ-3"}, {ID: "fail-4"}, {ID: "fail-5"},
	}
	storeDynamicModels(fakeModels)
	defer storeDynamicModels(nil)

	origPingFn := sendActivePingWorkbuddyFn
	defer func() { sendActivePingWorkbuddyFn = origPingFn }()

	var attempted []string
	sendActivePingWorkbuddyFn = func(sa *storedAuth, chosenModel string) error {
		attempted = append(attempted, chosenModel)
		if chosenModel == "succ-3" {
			return nil
		}
		return errors.New("upstream failed for " + chosenModel)
	}

	sa := &storedAuth{Account: storedAccount{UID: "wbai-user-any-succ"}}
	err := doActivePing("idx-succ", "wbai-user-any-succ", sa)
	if err != nil {
		t.Fatalf("expected any-success to return nil, got %v", err)
	}

	foundSucc := false
	for _, m := range attempted {
		if m == "succ-3" {
			foundSucc = true
			break
		}
	}
	if !foundSucc {
		t.Fatalf("expected succ-3 to be attempted, got attempts: %v", attempted)
	}
}

// TestDoActivePingMultiModelAllFail 验证所有候选模型均失败时返回错误。
func TestDoActivePingMultiModelAllFail(t *testing.T) {
	resetActivePingTimes()
	defer resetActivePingTimes()

	fakeModels := []pluginapi.ModelInfo{
		{ID: "fail-1"}, {ID: "fail-2"},
	}
	storeDynamicModels(fakeModels)
	defer storeDynamicModels(nil)

	origPingFn := sendActivePingWorkbuddyFn
	defer func() { sendActivePingWorkbuddyFn = origPingFn }()

	attemptCount := 0
	sendActivePingWorkbuddyFn = func(sa *storedAuth, chosenModel string) error {
		attemptCount++
		return errors.New("all models down")
	}

	sa := &storedAuth{Account: storedAccount{UID: "wbai-user-all-fail"}}
	err := doActivePing("idx-fail", "wbai-user-all-fail", sa)
	if err == nil {
		t.Fatalf("expected error when all models fail")
	}
	if attemptCount != 2 {
		t.Fatalf("expected 2 attempts for 2 available models, got %d", attemptCount)
	}
}

// TestDoActivePingFailureStillThrottles 钉住节流契约：探活失败也必须刷新节流
// 时间戳，否则坏账号会在每个 watchdog tick（10 分钟）被反复全量重测，反而比
// 健康账号更频繁地消耗账号配额。
func TestDoActivePingFailureStillThrottles(t *testing.T) {
	resetActivePingTimes()
	defer resetActivePingTimes()

	fakeModels := []pluginapi.ModelInfo{{ID: "fail-1"}, {ID: "fail-2"}}
	storeDynamicModels(fakeModels)
	defer storeDynamicModels(nil)

	origPingFn := sendActivePingWorkbuddyFn
	defer func() { sendActivePingWorkbuddyFn = origPingFn }()

	attempts := 0
	sendActivePingWorkbuddyFn = func(sa *storedAuth, chosenModel string) error {
		attempts++
		return errors.New("all models down")
	}

	sa := &storedAuth{Account: storedAccount{UID: "wbai-user-throttle-fail"}}
	if err := doActivePing("idx-tf", "wbai-user-throttle-fail", sa); err == nil {
		t.Fatalf("expected error when all models fail")
	}
	firstRun := attempts
	if firstRun != 2 {
		t.Fatalf("expected 2 attempts on first run, got %d", firstRun)
	}
	// 立即再次调用应被节流拦截，不再发起任何模型请求。
	if err := doActivePing("idx-tf", "wbai-user-throttle-fail", sa); err != nil {
		t.Fatalf("throttled second run should return nil, got %v", err)
	}
	if attempts != firstRun {
		t.Fatalf("failed ping must stamp the throttle clock; got %d attempts after second call", attempts)
	}
}

// TestDoActivePingAllModelsBeyondLegacyCap 验证超过旧上限（5 个）的模型全部按序轮测，
// 且顺序与候选发现顺序一致，最后一个模型成功时前面的模型均已尝试。
func TestDoActivePingAllModelsBeyondLegacyCap(t *testing.T) {
	resetActivePingTimes()
	defer resetActivePingTimes()

	fakeModels := []pluginapi.ModelInfo{
		{ID: "m1"}, {ID: "m2"}, {ID: "m3"}, {ID: "m4"}, {ID: "m5"}, {ID: "m6"}, {ID: "m7"},
	}
	storeDynamicModels(fakeModels)
	defer storeDynamicModels(nil)

	origPingFn := sendActivePingWorkbuddyFn
	defer func() { sendActivePingWorkbuddyFn = origPingFn }()

	var attempted []string
	sendActivePingWorkbuddyFn = func(sa *storedAuth, chosenModel string) error {
		attempted = append(attempted, chosenModel)
		if chosenModel == "m7" {
			return nil
		}
		return errors.New("down")
	}

	sa := &storedAuth{Account: storedAccount{UID: "wbai-user-beyond-cap"}}
	if err := doActivePing("idx-beyond", "wbai-user-beyond-cap", sa); err != nil {
		t.Fatalf("expected success when last model succeeds, got %v", err)
	}
	if len(attempted) != 7 {
		t.Fatalf("expected all 7 models attempted (no legacy 5 cap), got %d: %v", len(attempted), attempted)
	}
	for i, want := range []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7"} {
		if attempted[i] != want {
			t.Fatalf("attempt order mismatch at %d: got %v, want %s", i, attempted, want)
		}
	}
}
