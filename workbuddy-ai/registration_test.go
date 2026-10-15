// registration_test.go 覆盖 WorkBuddy AI 国际版补注册链路：二次 JSON 解包、
// 出口 IP 地区推导与 CN→HK 映射、国家条目匹配、注册地提交体、云账号注册、
// 幂等编排（已注册跳过 / 未注册补齐 / 各步失败分支）。全部通过 httptest 本地桩
// 完成，不触碰真实上游与宿主。
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// registrationStub 汇总一次补注册链路中桩服务收到的请求，供断言调用面。
type registrationStub struct {
	paths      []string
	submitBody map[string]any
}

// newRegistrationServer 构造覆盖补注册四端点的桩服务。region 为地区端点返回的 IOS2；
// registered 决定 /console/accounts 返回的 areaInfoComplete。
// [参数] registered 初始注册态；region 地区端点 IOS2；failPath 需返回业务错误的路径（空表示不失败）
// [返回] 桩服务与请求记录
// 最近修改时间：2026-10-15 18:40:00；改动原因：补注册链路新建
func newRegistrationServer(registered bool, region, failPath string) (*httptest.Server, *registrationStub) {
	stub := &registrationStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.paths = append(stub.paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if failPath != "" && r.URL.Path == failPath {
			_, _ = w.Write([]byte(`{"code":500,"msg":"stub failure"}`))
			return
		}
		switch r.URL.Path {
		case endpointAccounts:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":{"accounts":[{"uid":"u1","type":"personal","areaInfoComplete":` + boolStr(registered) + `}]}}`))
		case endpointUserAreaInfo:
			// 上游把 data 序列化成 JSON 字符串返回，需二次解码。
			inner := `{"code":0,"msg":"ok","data":{"IOS2":"` + region + `","name":"stub"}}`
			encoded, _ := json.Marshal(inner)
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":` + string(encoded) + `}`))
		case endpointCountryList:
			inner := `{"code":0,"data":{"list":[{"IOS2":"SG","IOS3":"SGP","EnName":"Singapore","Name":"新加坡","Code":"65"},{"IOS2":"HK","IOS3":"HKG","EnName":"Hong Kong, China","Name":"中国香港","Code":"852"}]}}`
			encoded, _ := json.Marshal(inner)
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK","data":` + string(encoded) + `}`))
		case endpointSubmitRegion:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			stub.submitBody = body
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK"}`))
		case endpointRegisterCloud:
			_, _ = w.Write([]byte(`{"code":200,"msg":"register success"}`))
		case "/billing/ide/trial":
			_, _ = w.Write([]byte(`{"code":14051,"msg":"has applied trial"}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"msg":"OK"}`))
		}
	}))
	return srv, stub
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestDecodeNestedData 验证二次 JSON 解包与已是对象时的原样返回。
func TestDecodeNestedData(t *testing.T) {
	// 双重编码：data 是字符串化的 JSON。
	double, _ := json.Marshal(`{"data":{"IOS2":"SG"}}`)
	got := decodeNestedData(double)
	if string(got) != `{"data":{"IOS2":"SG"}}` {
		t.Fatalf("double-encoded decode = %s", got)
	}
	// 已是对象：保持原样。
	plain := json.RawMessage(`{"IOS2":"JP"}`)
	if string(decodeNestedData(plain)) != `{"IOS2":"JP"}` {
		t.Fatalf("plain decode changed value: %s", decodeNestedData(plain))
	}
}

// TestResolveRegistrationRegion_MapsCNToHK 验证 CN 按官方前端口径映射为 HK。
func TestResolveRegistrationRegion_MapsCNToHK(t *testing.T) {
	srv, _ := newRegistrationServer(false, "CN", "")
	defer srv.Close()
	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{Auth: storedTokens{Domain: "www.workbuddy.ai"}}
	region, err := resolveRegistrationRegion(sa)
	if err != nil {
		t.Fatalf("resolveRegistrationRegion: %v", err)
	}
	if region != "HK" {
		t.Fatalf("CN should map to HK, got %s", region)
	}
}

// TestEnsureAccountRegistered_SkipsWhenAlreadyRegistered 验证已注册账号不产生写操作。
func TestEnsureAccountRegistered_SkipsWhenAlreadyRegistered(t *testing.T) {
	srv, stub := newRegistrationServer(true, "SG", "")
	defer srv.Close()
	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "tok", Domain: "www.workbuddy.ai"},
		Account: storedAccount{UID: "u1", Nickname: "n"},
	}
	res, err := ensureAccountRegistered(sa)
	if err != nil {
		t.Fatalf("ensureAccountRegistered: %v", err)
	}
	if ok, _ := res["registered"].(bool); !ok {
		t.Fatalf("expected registered=true, got %v", res)
	}
	for _, p := range stub.paths {
		if p == endpointSubmitRegion || p == endpointRegisterCloud {
			t.Fatalf("already-registered account must not write; called %s", p)
		}
	}
}

// TestEnsureAccountRegistered_CompletesFullFlow 验证未注册账号走完完整链路，
// 且提交体的 countryCode 取电话区号（与官方网页口径一致）。
func TestEnsureAccountRegistered_CompletesFullFlow(t *testing.T) {
	srv, stub := newRegistrationServer(false, "SG", "")
	defer srv.Close()
	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "tok", Domain: "www.workbuddy.ai"},
		Account: storedAccount{UID: "u1", Nickname: "n"},
	}
	res, err := ensureAccountRegistered(sa)
	if err != nil {
		t.Fatalf("ensureAccountRegistered: %v", err)
	}
	if ok, _ := res["registered"].(bool); !ok {
		t.Fatalf("expected registered=true, got %v", res)
	}
	if region, _ := res["region"].(string); region != "SG" {
		t.Fatalf("expected region SG, got %v", res["region"])
	}
	attrs, _ := stub.submitBody["attributes"].(map[string]any)
	if attrs == nil {
		t.Fatalf("submit body missing attributes: %v", stub.submitBody)
	}
	codes, _ := attrs["countryCode"].([]any)
	if len(codes) != 1 || codes[0] != "65" {
		t.Fatalf("countryCode must be dialing code 65, got %v", attrs["countryCode"])
	}
	if names, _ := attrs["countryName"].([]any); len(names) != 1 || names[0] != "SG" {
		t.Fatalf("countryName must be IOS2 SG, got %v", attrs["countryName"])
	}
}

// TestEnsureAccountRegistered_RegionSubmitFailure 验证提交注册地失败时返回错误且不继续注册云账号。
func TestEnsureAccountRegistered_RegionSubmitFailure(t *testing.T) {
	srv, stub := newRegistrationServer(false, "SG", endpointSubmitRegion)
	defer srv.Close()
	restore := setBillingBase(srv.URL)
	defer restore()

	sa := &storedAuth{
		Auth:    storedTokens{AccessToken: "tok", Domain: "www.workbuddy.ai"},
		Account: storedAccount{UID: "u1", Nickname: "n"},
	}
	if _, err := ensureAccountRegistered(sa); err == nil {
		t.Fatal("expected error when region submit fails")
	}
	for _, p := range stub.paths {
		if p == endpointRegisterCloud {
			t.Fatal("must not register cloud account after region submit failure")
		}
	}
}

// TestRegisterCloudAccount_RequiresUID 验证缺少 uid 时拒绝发起云账号注册。
func TestRegisterCloudAccount_RequiresUID(t *testing.T) {
	sa := &storedAuth{Auth: storedTokens{AccessToken: "tok", Domain: "www.workbuddy.ai"}}
	if err := registerCloudAccount(sa); err == nil {
		t.Fatal("expected error when uid is empty")
	}
}
