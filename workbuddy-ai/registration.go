// registration.go implements the WorkBuddy AI (International) registration
// completion flow. The browser login page skips the country/region step when
// platform=CLI, so a freshly authorized account keeps areaInfoComplete=false
// and never receives credits until the region is submitted and the cloud
// account is registered upstream.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// 补注册链路涉及的三个端点分组：console（账号与注册地）、billing（地区与试用包）、
// auth（云账号注册）。与 billingBase 同域，但路径不同，故单独声明。
const (
	endpointAccounts        = "/console/accounts"
	endpointSubmitRegion    = "/console/login/account"
	endpointUserAreaInfo    = "/billing/area/get-user-area-info"
	endpointCountryList     = "/billing/area/get-country-code"
	endpointRegisterCloud   = "/auth/realms/copilot/overseas/user/register"
	registrationSuccessCode = 200
)

// countryEntry 是 /billing/area/get-country-code 返回的国家条目。
// countryCode 取 Code（电话区号，如 "65"），countryName 取 IOS2，与官方网页提交口径一致。
type countryEntry struct {
	IOS2   string `json:"IOS2"`
	IOS3   string `json:"IOS3"`
	EnName string `json:"EnName"`
	Name   string `json:"Name"`
	Code   string `json:"Code"`
}

// wbAPICall 以账号 Bearer 凭证调用 WorkBuddy AI 的 console/billing/auth 端点。
// [参数] sa 账号凭证（提供 AccessToken / UID / EnterpriseID）；method HTTP 方法；
// path 以 / 开头的路径；body 请求体，nil 表示发送空对象 {}
// [返回] 原始 {code,msg,data} 信封；网络或解析失败时返回错误
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路所需的通用调用入口
func wbAPICall(sa *storedAuth, method, path string, body any) (*apiEnvelope, error) {
	if sa == nil {
		return nil, fmt.Errorf("nil storedAuth")
	}
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader([]byte("{}"))
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, billingBase+path, reader)
	if err != nil {
		return nil, err
	}
	billingHeaders(req, sa)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	var env apiEnvelope
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, fmt.Errorf("parse failed: %w (body: %s)", err, truncateRedacted(string(resp.Body), 120))
	}
	return &env, nil
}

// envelopeOK 判定上游业务是否成功。多数端点用 code=0，云账号注册端点用 code=200，
// 两者都视为成功；其余非零码交给调用方按业务语义处理。
// [参数] env 上游信封
// [返回] true 表示成功码
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路
func envelopeOK(env *apiEnvelope) bool {
	if env == nil {
		return false
	}
	return env.Code == 0 || env.Code == registrationSuccessCode
}

// decodeNestedData 逐层解包 data 字段。部分端点（地区信息、国家列表）把 data
// 序列化成 JSON 字符串返回，需要二次解码；已是对象时原样返回。
// [参数] raw 上游信封的 data 字段
// [返回] 解包后的 JSON；无法解包时返回原值
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路
func decodeNestedData(raw json.RawMessage) json.RawMessage {
	cur := raw
	for i := 0; i < 2; i++ {
		trimmed := bytes.TrimSpace(cur)
		if len(trimmed) == 0 || trimmed[0] != '"' {
			return cur
		}
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return cur
		}
		next := json.RawMessage(strings.TrimSpace(s))
		if len(next) == 0 {
			return cur
		}
		cur = next
	}
	return cur
}

// fetchRegistrationStatus 查询账号是否已完成注册地填写。
// [参数] sa 账号凭证
// [返回] areaInfoComplete 布尔值；查询失败时返回错误
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路
func fetchRegistrationStatus(sa *storedAuth) (bool, error) {
	env, err := wbAPICall(sa, http.MethodGet, endpointAccounts, nil)
	if err != nil {
		return false, err
	}
	if !envelopeOK(env) {
		return false, fmt.Errorf("accounts: code=%d msg=%s", env.Code, truncateRedacted(env.Msg, 120))
	}
	var payload struct {
		Accounts []struct {
			UID              string `json:"uid"`
			Type             string `json:"type"`
			AreaInfoComplete bool   `json:"areaInfoComplete"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(decodeNestedData(env.Data), &payload); err != nil {
		return false, fmt.Errorf("accounts parse: %w", err)
	}
	for _, a := range payload.Accounts {
		if a.Type == "personal" {
			return a.AreaInfoComplete, nil
		}
	}
	if len(payload.Accounts) > 0 {
		return payload.Accounts[0].AreaInfoComplete, nil
	}
	return false, fmt.Errorf("accounts: empty list")
}

// resolveRegistrationRegion 按请求出口 IP 推导注册地 IOS2 码，与官方网页的
// 「detected region」预选行为一致；CN 按官方前端口径映射为 HK。
// [参数] sa 账号凭证
// [返回] IOS2 国家码（如 SG / HK）；推导失败时返回错误
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路
func resolveRegistrationRegion(sa *storedAuth) (string, error) {
	env, err := wbAPICall(sa, http.MethodPost, endpointUserAreaInfo, map[string]any{"action": "getUserAreaInfo"})
	if err != nil {
		return "", err
	}
	if !envelopeOK(env) {
		return "", fmt.Errorf("user-area: code=%d msg=%s", env.Code, truncateRedacted(env.Msg, 120))
	}
	var payload struct {
		Data struct {
			IOS2 string `json:"IOS2"`
		} `json:"data"`
	}
	if err := json.Unmarshal(decodeNestedData(env.Data), &payload); err != nil {
		return "", fmt.Errorf("user-area parse: %w", err)
	}
	region := strings.ToUpper(strings.TrimSpace(payload.Data.IOS2))
	if region == "" {
		return "", fmt.Errorf("user-area: empty IOS2")
	}
	if region == "CN" {
		region = "HK"
	}
	return region, nil
}

// lookupCountryByISO2 从上游国家列表取指定 IOS2 的完整条目，用于构造注册地提交体。
// [参数] sa 账号凭证；iso2 目标国家 IOS2 码
// [返回] 匹配的国家条目；未找到时返回错误
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路
func lookupCountryByISO2(sa *storedAuth, iso2 string) (*countryEntry, error) {
	env, err := wbAPICall(sa, http.MethodPost, endpointCountryList, map[string]any{"filterForbidden": 1})
	if err != nil {
		return nil, err
	}
	if !envelopeOK(env) {
		return nil, fmt.Errorf("country-list: code=%d msg=%s", env.Code, truncateRedacted(env.Msg, 120))
	}
	var payload struct {
		Data struct {
			List []countryEntry `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(decodeNestedData(env.Data), &payload); err != nil {
		return nil, fmt.Errorf("country-list parse: %w", err)
	}
	for i := range payload.Data.List {
		if strings.EqualFold(payload.Data.List[i].IOS2, iso2) {
			return &payload.Data.List[i], nil
		}
	}
	return nil, fmt.Errorf("country %s not found in upstream list", iso2)
}

// submitRegistrationRegion 提交注册地，翻转服务端 areaInfoComplete。
// [参数] sa 账号凭证；entry 目标国家条目（提供电话区号与英文名）
// [返回] 提交成功返回 nil，否则返回错误
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路
func submitRegistrationRegion(sa *storedAuth, entry *countryEntry) error {
	if entry == nil {
		return fmt.Errorf("nil country entry")
	}
	body := map[string]any{
		"attributes": map[string]any{
			"countryCode":     []string{entry.Code},
			"countryFullName": []string{entry.EnName},
			"countryName":     []string{entry.IOS2},
		},
	}
	env, err := wbAPICall(sa, http.MethodPost, endpointSubmitRegion, body)
	if err != nil {
		return err
	}
	if !envelopeOK(env) {
		return fmt.Errorf("submit region: code=%d msg=%s", env.Code, truncateRedacted(env.Msg, 120))
	}
	return nil
}

// registerCloudAccount 注册上游云账号。必须先提交注册地，否则上游返回
// 「register failed: register region required」。
// [参数] sa 账号凭证（须含 UID）
// [返回] 注册成功返回 nil，否则返回错误
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路
func registerCloudAccount(sa *storedAuth) error {
	uid := strings.TrimSpace(sa.Account.UID)
	if uid == "" {
		return fmt.Errorf("register cloud: missing uid")
	}
	path := endpointRegisterCloud + "?userId=" + url.QueryEscape(uid)
	env, err := wbAPICall(sa, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if !envelopeOK(env) {
		return fmt.Errorf("register cloud: code=%d msg=%s", env.Code, truncateRedacted(env.Msg, 120))
	}
	return nil
}

// ensureAccountRegistered 幂等补齐账号注册链路：查询注册态 → 推导并提交注册地 →
// 注册云账号 → 尽力领取试用包。任一步失败都返回错误，但不影响调用方的授权落盘。
// [参数] sa 账号凭证
// [返回] 状态明细（含 registered / region / steps）；失败时返回错误
// 最近修改时间：2026-10-15 18:20:00；改动原因：新增补注册链路
func ensureAccountRegistered(sa *storedAuth) (map[string]any, error) {
	out := map[string]any{"registered": false}
	if sa == nil {
		return out, fmt.Errorf("nil storedAuth")
	}
	if strings.TrimSpace(sa.Account.UID) == "" {
		return out, fmt.Errorf("missing uid; cannot complete registration")
	}

	complete, err := fetchRegistrationStatus(sa)
	if err != nil {
		return out, fmt.Errorf("check status: %w", err)
	}
	if complete {
		out["registered"] = true
		out["skipped"] = "already registered"
		return out, nil
	}

	region, err := resolveRegistrationRegion(sa)
	if err != nil {
		return out, fmt.Errorf("resolve region: %w", err)
	}
	entry, err := lookupCountryByISO2(sa, region)
	if err != nil {
		return out, fmt.Errorf("lookup country: %w", err)
	}
	if err := submitRegistrationRegion(sa, entry); err != nil {
		return out, fmt.Errorf("submit region: %w", err)
	}
	if err := registerCloudAccount(sa); err != nil {
		return out, fmt.Errorf("register cloud: %w", err)
	}

	out["registered"] = true
	out["region"] = region
	out["country"] = entry.EnName

	// 试用包为尽力而为：官方流程在注册后调用，已领取时上游返回 14051，不算失败。
	if _, err := performTrialCall(sa); err != nil {
		log.Printf("[workbuddy-ai] trial claim after registration failed for %s: %v", sa.Account.UID, err)
	}
	return out, nil
}
