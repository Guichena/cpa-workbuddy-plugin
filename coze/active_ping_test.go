package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// 本文件覆盖面板「测试模型」入口：模型下拉数据源与认证层探活判定。
// 全部走 httptest，不依赖真实网络与账号。

// seedAccount 在临时账号目录写入一个账号文件，返回账号结构。
// [参数] t: 测试上下文；authID: 账号标识；secUserID: 账号句柄。
// [返回] 已落盘的账号。
// 最近修改时间：2026-10-18；改动原因：新增，探活用例的账号夹具。
func seedAccount(t *testing.T, authID, secUserID string) *StoredAuth {
	t.Helper()
	dir := t.TempDir()
	setAccountsDir(dir)
	t.Cleanup(func() { setAccountsDir("") })

	sa := &StoredAuth{
		Type:         providerName,
		Provider:     providerName,
		AuthID:       authID,
		CookieHeader: "sessionid=test-session; sid_tt=test-sid",
		SecUserID:    secUserID,
		Label:        "测试账号",
		LoginAt:      "2026-10-18T03:21:47+08:00",
		LastRenewAt:  "2026-10-18T03:21:47+08:00",
		CreatedAt:    "2026-10-18T03:21:47+08:00",
		UpdatedAt:    "2026-10-18T03:21:47+08:00",
	}
	path := filepath.Join(dir, authID+".json")
	if errWrite := writeAuthFileDirect(path, sa); errWrite != nil {
		t.Fatalf("写入账号夹具失败：%v", errWrite)
	}
	return sa
}

// setupModelStubs 启动模型清单与多模态目录桩并接管端点。
// [参数] t: 测试上下文；byType: 按类型预置的模型；catalog: 多模态目录。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，模型端点环境搭建。
func setupModelStubs(t *testing.T, byType map[string][]upstreamModel, catalog *multimodalCatalog) {
	t.Helper()
	listSrv := httptest.NewServer(&modelListStub{byType: byType})
	t.Cleanup(listSrv.Close)
	catalogStub := &multimodalStub{fail: true}
	if catalog != nil {
		catalogStub = &multimodalStub{catalog: *catalog}
	}
	catalogSrv := httptest.NewServer(catalogStub)
	t.Cleanup(catalogSrv.Close)
	t.Cleanup(swapModelEndpoints(listSrv.URL, catalogSrv.URL))
	resetModelCatalogCache()
	t.Cleanup(resetModelCatalogCache)
}

// setupProbeStubs 启动令牌与判据桩。
// [参数] t: 测试上下文；secUserID: 判据返回的账号句柄。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，探活链路环境搭建。
func setupProbeStubs(t *testing.T, secUserID string) {
	t.Helper()
	token := &tokenBeatStub{t: t, cookieNames: defaultCookieNames}
	info := &accountInfoStub{valid: true, secUserID: secUserID}
	tokenSrv := httptest.NewServer(token)
	infoSrv := httptest.NewServer(info)
	t.Cleanup(tokenSrv.Close)
	t.Cleanup(infoSrv.Close)
	t.Cleanup(swapEndpoints(tokenSrv.URL, infoSrv.URL))
}

// TestHandleModelsQuery_RequiresAuthID 验证缺少账号标识时如实报错。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，入参校验用例。
func TestHandleModelsQuery_RequiresAuthID(t *testing.T) {
	res := handleModelsQuery(pluginapi.ManagementRequest{})
	if res["error"] == nil {
		t.Fatal("缺少 auth_id 时必须返回错误")
	}
}

// TestHandleModelsQuery_ReturnsUpstreamModels 验证下拉数据来自动态目录。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，动态目录用例。
func TestHandleModelsQuery_ReturnsUpstreamModels(t *testing.T) {
	sa := seedAccount(t, "coze-models01", "sec-user-1")
	setupModelStubs(t, map[string][]upstreamModel{
		modelTypeLLM: {sampleLLM("chat-a")},
	}, nil)

	res := handleModelsQuery(pluginapi.ManagementRequest{
		Query: url.Values{"auth_id": []string{sa.AuthID}},
	})
	if res["error"] != nil {
		t.Fatalf("查询不应失败：%v", res["error"])
	}
	if res["source"] != "upstream" {
		t.Fatalf("数据源应为 upstream，实际 %v", res["source"])
	}
	models, _ := res["models"].([]map[string]any)
	if len(models) != 1 {
		t.Fatalf("应返回 1 个模型，实际 %d", len(models))
	}
	if models[0]["id"] != modelIDPrefix+"chat-a" {
		t.Fatalf("模型 id 不符：%v", models[0]["id"])
	}
}

// TestHandleModelsQuery_FallbackOnDiscoverError 验证拉取失败时回落并说明原因。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，回落提示用例。
func TestHandleModelsQuery_FallbackOnDiscoverError(t *testing.T) {
	sa := seedAccount(t, "coze-models02", "sec-user-1")
	failAll := map[string]bool{}
	for _, entry := range modelListTypes {
		failAll[entry.Upstream] = true
	}
	listSrv := httptest.NewServer(&modelListStub{byType: map[string][]upstreamModel{}, failTypes: failAll})
	t.Cleanup(listSrv.Close)
	catalogSrv := httptest.NewServer(&multimodalStub{fail: true})
	t.Cleanup(catalogSrv.Close)
	t.Cleanup(swapModelEndpoints(listSrv.URL, catalogSrv.URL))
	resetModelCatalogCache()
	t.Cleanup(resetModelCatalogCache)

	res := handleModelsQuery(pluginapi.ManagementRequest{
		Query: url.Values{"auth_id": []string{sa.AuthID}},
	})
	if res["source"] != "fallback" {
		t.Fatalf("拉取失败应回落 fallback，实际 %v", res["source"])
	}
	if res["notice"] == "" || res["notice"] == nil {
		t.Fatal("回落时必须说明原因，不能把内置清单伪装成账号真实模型")
	}
	models, _ := res["models"].([]map[string]any)
	if len(models) == 0 {
		t.Fatal("回落清单不能为空")
	}
}

// TestRunActiveTest_Success 验证会话有效且模型可见时判定通过。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，探活成功用例。
func TestRunActiveTest_Success(t *testing.T) {
	sa := seedAccount(t, "coze-probe01", "sec-user-1")
	setupProbeStubs(t, "sec-user-1")
	setupModelStubs(t, map[string][]upstreamModel{
		modelTypeLLM: {sampleLLM("chat-a")},
	}, nil)

	res := runActiveTest(sa, modelIDPrefix+"chat-a")
	if res["ok"] != true {
		t.Fatalf("探活应通过，实际 %v", res["error"])
	}
	if res["model_count"] != 1 {
		t.Fatalf("模型计数应为 1，实际 %v", res["model_count"])
	}
}

// TestRunActiveTest_SecUserIDMismatch 验证判据返回他人句柄时拒绝判定可用。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，账号归属校验用例。
func TestRunActiveTest_SecUserIDMismatch(t *testing.T) {
	sa := seedAccount(t, "coze-probe02", "sec-user-1")
	setupProbeStubs(t, "sec-user-other")
	setupModelStubs(t, map[string][]upstreamModel{
		modelTypeLLM: {sampleLLM("chat-a")},
	}, nil)

	res := runActiveTest(sa, "")
	if res["ok"] == true {
		t.Fatal("登录态与账号不匹配时必须判失败")
	}
	if res["error"] == nil {
		t.Fatal("必须给出不匹配原因")
	}
}

// TestRunActiveTest_ModelNotInCatalog 验证选中模型不在目录时报错。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，模型归属校验用例。
func TestRunActiveTest_ModelNotInCatalog(t *testing.T) {
	sa := seedAccount(t, "coze-probe03", "sec-user-1")
	setupProbeStubs(t, "sec-user-1")
	setupModelStubs(t, map[string][]upstreamModel{
		modelTypeLLM: {sampleLLM("chat-a")},
	}, nil)

	res := runActiveTest(sa, modelIDPrefix+"not-exist")
	if res["ok"] == true {
		t.Fatal("模型不在目录时必须判失败")
	}
	if res["model_count"] != 1 {
		t.Fatalf("失败时仍应回传可见模型数，实际 %v", res["model_count"])
	}
}

// TestRunActiveTest_ProbeFailure 验证判据失败时不做模型探测。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，会话失效用例。
func TestRunActiveTest_ProbeFailure(t *testing.T) {
	sa := seedAccount(t, "coze-probe04", "sec-user-1")
	info := &accountInfoStub{valid: false}
	infoSrv := httptest.NewServer(info)
	t.Cleanup(infoSrv.Close)
	t.Cleanup(swapEndpoints("", infoSrv.URL))

	res := runActiveTest(sa, "")
	if res["ok"] == true {
		t.Fatal("会话失效时必须判失败")
	}
	if res["error"] == nil {
		t.Fatal("必须给出失效原因")
	}
}

// TestModelInCatalog 验证模型名匹配忽略大小写与 :latest 后缀。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，匹配规则用例。
func TestModelInCatalog(t *testing.T) {
	models := []pluginapi.ModelInfo{{ID: "coze/Chat-A", Name: "chat-a"}}
	if !modelInCatalog(models, "coze/chat-a") {
		t.Fatal("模型匹配应忽略大小写")
	}
	if !modelInCatalog(models, "coze/chat-a:latest") {
		t.Fatal("模型匹配应忽略 :latest 后缀")
	}
	if modelInCatalog(models, "coze/other") {
		t.Fatal("不存在的模型不应命中")
	}
}

// TestHandleListAccounts_MasksSecUserID 验证账号列表脱敏且带北京时间原始值。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，面板数据契约用例。
func TestHandleListAccounts_MasksSecUserID(t *testing.T) {
	seedAccount(t, "coze-list01", "sec-user-abcdefghijklmn")

	raw, errList := handleListAccounts()
	if errList != nil {
		t.Fatalf("列表不应失败：%v", errList)
	}
	var env envelope
	if errUnmarshal := json.Unmarshal(raw, &env); errUnmarshal != nil || !env.OK {
		t.Fatalf("响应信封异常：%s", raw)
	}
	var resp struct {
		Body []byte `json:"Body"`
	}
	if errUnmarshal := json.Unmarshal(env.Result, &resp); errUnmarshal != nil {
		t.Fatalf("管理响应解析失败：%v", errUnmarshal)
	}
	var payload struct {
		Accounts []accountSummary `json:"accounts"`
	}
	if errUnmarshal := json.Unmarshal(resp.Body, &payload); errUnmarshal != nil {
		t.Fatalf("账号载荷解析失败：%v", errUnmarshal)
	}
	if len(payload.Accounts) != 1 {
		t.Fatalf("应返回 1 个账号，实际 %d", len(payload.Accounts))
	}
	got := payload.Accounts[0]
	if got.SecUserID == "sec-user-abcdefghijklmn" {
		t.Fatal("账号句柄必须脱敏后再下发")
	}
	if got.LoginAt == "" {
		t.Fatal("授权时间必须下发，供面板按北京时间展示")
	}
	if got.AuthState == "" {
		t.Fatal("登录态状态必须下发")
	}
}

// TestAuthStateOf 验证会话 Cookie 缺失时判定为待重新授权。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，状态判定用例。
func TestAuthStateOf(t *testing.T) {
	expired := &StoredAuth{CookieHeader: "foo=bar"}
	if got := authStateOf(expired); got != "expired" {
		t.Fatalf("无会话 Cookie 应判 expired，实际 %q", got)
	}
}

// TestSeedAccountFileExists 确认夹具确实落盘（防止后续用例因夹具缺失而假通过）。
// [参数] t: 测试上下文。
// [返回] 无。
// 最近修改时间：2026-10-18；改动原因：新增，夹具自检。
func TestSeedAccountFileExists(t *testing.T) {
	sa := seedAccount(t, "coze-fixture1", "sec-user-1")
	if _, errStat := os.Stat(filepath.Join(getAccountsDir(), sa.AuthID+".json")); errStat != nil {
		t.Fatalf("夹具应真实落盘：%v", errStat)
	}
}
