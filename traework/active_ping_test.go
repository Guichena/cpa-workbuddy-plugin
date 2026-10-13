package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestPickRandomTraeModel(t *testing.T) {
	storeTraeDynamicModels(nil)
	saEmpty := &traeAuth{}
	mDefault := pickRandomTraeModel(saEmpty)
	if mDefault != "claude-3-5-sonnet" {
		t.Fatalf("expected fallback model claude-3-5-sonnet, got %s", mDefault)
	}

	fakeModels := []pluginapi.ModelInfo{
		{ID: "trae-fake-1"},
		{ID: "trae-fake-2"},
	}
	storeTraeDynamicModels(fakeModels)
	defer storeTraeDynamicModels(nil)

	picked := pickRandomTraeModel(saEmpty)
	if picked != "trae-fake-1" && picked != "trae-fake-2" {
		t.Fatalf("expected picked model from fakeModels, got %s", picked)
	}
}

func TestShouldActivePingThrottle(t *testing.T) {
	resetActivePingTimes()
	defer resetActivePingTimes()

	authKey := "trae-user-123"

	// 1. 首次应该允许
	if !shouldActivePing(authKey) {
		t.Fatalf("first ping should be allowed")
	}

	// 记录活跃时间
	recordActivePing(authKey)

	// 2. 立即再次检查应该被节流拦截
	if shouldActivePing(authKey) {
		t.Fatalf("immediate next ping should be throttled")
	}

	// 3. 模拟时间超过 30 分钟后应该再次允许
	activePingMu.Lock()
	lastActivePingTimes[authKey] = time.Now().Add(-31 * time.Minute)
	activePingMu.Unlock()
	if !shouldActivePing(authKey) {
		t.Fatalf("ping after 31 minutes should be allowed")
	}
}

func TestDoFetchOneWithActivePing(t *testing.T) {
	origPingFn := triggerActivePingFn
	defer func() { triggerActivePingFn = origPingFn }()

	pingCount := 0
	var lastAuthID string
	triggerActivePingFn = func(authIndex, authID string, sa *traeAuth) error {
		pingCount++
		lastAuthID = authID
		return errors.New("upstream transient ping error")
	}

	sa := &traeAuth{
		UserID: "test-uid",
	}
	err := triggerActivePing("idx-1", "test-auth", sa)
	if err == nil || err.Error() != "upstream transient ping error" {
		t.Fatalf("expected mock ping error, got %v", err)
	}
	if pingCount != 1 || lastAuthID != "test-auth" {
		t.Fatalf("expected ping recorded for test-auth, count=%d", pingCount)
	}
}

func TestHandleTestActiveTrae(t *testing.T) {
	origPingFn := sendActivePingTraeFn
	defer func() { sendActivePingTraeFn = origPingFn }()

	// 1. 缺少 auth_index 校验
	resMissing := handleTestActive(pluginapi.ManagementRequest{
		Body: []byte(`{}`),
	})
	if resMissing["error"] != "auth_index is required" {
		t.Fatalf("expected error for missing auth_index, got %v", resMissing)
	}

	// 2. host API 缺省时的凭据获取错误
	resNoHost := handleTestActive(pluginapi.ManagementRequest{
		Body: []byte(`{"auth_index": "test-index"}`),
	})
	if resNoHost["error"] == nil {
		t.Fatalf("expected error for hostAuthGet under test shim, got %v", resNoHost)
	}

	// 3. 模拟凭据成功调用 handleTestActiveWithAuth
	sa := &traeAuth{
		UserID: "trae-user-001",
	}
	var calledModel string
	sendActivePingTraeFn = func(sa *traeAuth, chosenModel string) error {
		calledModel = chosenModel
		return nil
	}

	resOk := handleTestActiveWithAuth(sa, "test-index", "")
	if resOk["ok"] != true || calledModel == "" {
		t.Fatalf("expected active ping test ok, got %v", resOk)
	}

	// 4. 模拟上游报错
	sendActivePingTraeFn = func(sa *traeAuth, chosenModel string) error {
		return errors.New("upstream rate limited")
	}
	resErr := handleTestActiveWithAuth(sa, "test-index", "")
	if resErr["error"] == nil || !strings.Contains(fmt.Sprint(resErr["error"]), "upstream rate limited") {
		t.Fatalf("expected rate limit error, got %v", resErr)
	}
}

// TestHandleTestActiveWithAuthSpecifiedModel 验证「测试」弹窗点选模型后的
// 指定模型透传，以及未指定时回退随机模型的向后兼容行为。
func TestHandleTestActiveWithAuthSpecifiedModel(t *testing.T) {
	origPingFn := sendActivePingTraeFn
	defer func() { sendActivePingTraeFn = origPingFn }()

	sa := &traeAuth{UserID: "trae-user-spec"}
	var calledModel string
	sendActivePingTraeFn = func(sa *traeAuth, chosenModel string) error {
		calledModel = chosenModel
		return nil
	}

	// 1. 指定模型必须原样透传给上游发送函数并回显在结果里
	resSpec := handleTestActiveWithAuth(sa, "test-index", "gemini-2.5-pro")
	if resSpec["ok"] != true || calledModel != "gemini-2.5-pro" {
		t.Fatalf("expected specified model passthrough, got res=%v called=%q", resSpec, calledModel)
	}
	if resSpec["model"] != "gemini-2.5-pro" {
		t.Fatalf("expected echoed model gemini-2.5-pro, got %v", resSpec["model"])
	}

	// 2. 模型为空时回退随机模型（缓存清空 → 兜底默认值），兼容旧面板
	storeTraeDynamicModels(nil)
	resAuto := handleTestActiveWithAuth(sa, "test-index", "")
	if resAuto["ok"] != true || calledModel != "claude-3-5-sonnet" {
		t.Fatalf("expected fallback random model, got res=%v called=%q", resAuto, calledModel)
	}
}

// TestPickRandomTraeModels 验证多模型随机挑选的上限、去重与 fallback 行为。
func TestPickRandomTraeModels(t *testing.T) {
	storeTraeDynamicModels(nil)
	saEmpty := &traeAuth{}
	resFallback := pickRandomTraeModels(saEmpty, 5)
	if len(resFallback) != 1 || resFallback[0] != "claude-3-5-sonnet" {
		t.Fatalf("expected fallback model, got %v", resFallback)
	}

	fakeModels := []pluginapi.ModelInfo{
		{ID: "m1"}, {ID: "m2"}, {ID: "m3"}, {ID: "m4"}, {ID: "m5"}, {ID: "m6"}, {ID: "m1"},
	}
	storeTraeDynamicModels(fakeModels)
	defer storeTraeDynamicModels(nil)

	res5 := pickRandomTraeModels(saEmpty, 5)
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

	res2 := pickRandomTraeModels(saEmpty, 2)
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
	storeTraeDynamicModels(fakeModels)
	defer storeTraeDynamicModels(nil)

	origPingFn := sendActivePingTraeFn
	defer func() { sendActivePingTraeFn = origPingFn }()

	var attempted []string
	sendActivePingTraeFn = func(sa *traeAuth, chosenModel string) error {
		attempted = append(attempted, chosenModel)
		if chosenModel == "succ-3" {
			return nil
		}
		return errors.New("upstream failed for " + chosenModel)
	}

	sa := &traeAuth{UserID: "trae-user-any-succ"}
	err := doActivePing("idx-succ", "trae-user-any-succ", sa)
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
	storeTraeDynamicModels(fakeModels)
	defer storeTraeDynamicModels(nil)

	origPingFn := sendActivePingTraeFn
	defer func() { sendActivePingTraeFn = origPingFn }()

	attemptCount := 0
	sendActivePingTraeFn = func(sa *traeAuth, chosenModel string) error {
		attemptCount++
		return errors.New("all models down")
	}

	sa := &traeAuth{UserID: "trae-user-all-fail"}
	err := doActivePing("idx-fail", "trae-user-all-fail", sa)
	if err == nil {
		t.Fatalf("expected error when all models fail")
	}
	if attemptCount != 2 {
		t.Fatalf("expected 2 attempts for 2 available models, got %d", attemptCount)
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
	storeTraeDynamicModels(fakeModels)
	defer storeTraeDynamicModels(nil)

	origPingFn := sendActivePingTraeFn
	defer func() { sendActivePingTraeFn = origPingFn }()

	var attempted []string
	sendActivePingTraeFn = func(sa *traeAuth, chosenModel string) error {
		attempted = append(attempted, chosenModel)
		if chosenModel == "m7" {
			return nil
		}
		return errors.New("down")
	}

	sa := &traeAuth{UserID: "trae-user-beyond-cap"}
	if err := doActivePing("idx-beyond", "trae-user-beyond-cap", sa); err != nil {
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

// TestSendActivePingTrae_ExitsImmediatelyOnFirstOutput 验证探活流收到首个有效 output（含思考片段）时立即成功退出，
// 不再阻塞等待后续数百个 reasoning token 或连接 EOF。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-11 21:50:00
func TestSendActivePingTrae_ExitsImmediatelyOnFirstOutput(t *testing.T) {
	origDirect := hostStreamDirectFn
	defer func() { hostStreamDirectFn = origDirect }()

	// 1. 构造模拟流：发送首个思考 output 后无限阻塞（若未首包即停则会超时）
	firstChunk := "event: metadata\ndata: {\"model\":\"\"}\n\nevent: output\ndata: {\"response\":\"\",\"reasoning_content\":\"Let\"}\n\n"
	hostStreamDirectFn = func(req *http.Request, bodyBytes []byte) (*hostHTTPStream, int, http.Header, error) {
		r := &hangingReader{
			r: strings.NewReader(firstChunk),
		}
		return &hostHTTPStream{liveBody: io.NopCloser(r)}, 200, http.Header{}, nil
	}

	// 2. 断言 sendActivePingTrae 在 500ms 内快速返回 nil
	start := time.Now()
	err := sendActivePingTrae(&traeAuth{UserID: "u1", Token: "t1"}, "glm-5.3-flash")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("sendActivePingTrae unexpected error: %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("sendActivePingTrae took %v, expected immediate return (< 500ms) on first output", elapsed)
	}
}

// TestSendActivePingTrae_DetectsEventErrorOnHTTP200 验证上游返回 HTTP 200 但携带 event:error（如 4008 额度耗尽）时如实报错。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-11 21:50:00
func TestSendActivePingTrae_DetectsEventErrorOnHTTP200(t *testing.T) {
	origDirect := hostStreamDirectFn
	defer func() { hostStreamDirectFn = origDirect }()

	errSSE := "event: error\ndata: {\"code\":4008,\"message\":\"Your requests have exceeded the quota.\"}\n\nevent: done\ndata: {}\n\n"
	hostStreamDirectFn = func(req *http.Request, bodyBytes []byte) (*hostHTTPStream, int, http.Header, error) {
		return &hostHTTPStream{liveBody: io.NopCloser(strings.NewReader(errSSE))}, 200, http.Header{}, nil
	}

	err := sendActivePingTrae(&traeAuth{UserID: "u1", Token: "t1"}, "glm-5.3-flash")
	if err == nil || !strings.Contains(err.Error(), "exceeded the quota") {
		t.Fatalf("expected quota exceeded error on HTTP 200 event:error, got: %v", err)
	}
}

