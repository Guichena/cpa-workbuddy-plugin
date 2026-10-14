package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// zcode-plan 额度（billing/current）查询与缓存。
//
// 数据源（2026-10-12 线上实测）：GET https://zcode.z.ai/api/v1/zcode-plan/billing/current
// 以账号 JWT 作 `Authorization: Bearer`，返回当前生效计划及其 entitlements：
// 每个 entitlement 描述一个模型在某个周期内的授予额度（grant_units，如每日 300 万 token）。
// 该端点是 zcode 面板额度可视化的唯一可靠来源——billing/balance（余额/已用）在免费
// start-plan 账号上固定返回 `400 code 3001 parameter error`，故不采用。
//
// 领取链路（2026-10-14 逆向 app.asar 3.15.1 + 生产实测）：
// 免费 Start Plan 需经 `POST /billing/claim` 领取，请求体 `{"plan_id":"<planId>"}`，
// 并强制携带阿里云人机验证头 `X-Aliyun-Captcha-Verify-Param`（无感验证产出）与设备指纹
// `X-Device-Mid`（UUID）。planId 由官方 `getManualClaimPlanPreviews()` 从 `/billing/preview`
// 动态解析，带批次后缀（实测为 `zcode-v3-start-plan-0817`），硬编码无后缀值会返回
// `code 1001 target not found`。
const (
	zcodeQuotaURL        = "https://zcode.z.ai/api/v1/zcode-plan/billing/current"
	zcodeClaimURL        = "https://zcode.z.ai/api/v1/zcode-plan/billing/claim"
	zcodeClaimPreviewURL = "https://zcode.z.ai/api/v1/zcode-plan/billing/preview"
	// defaultStartPlanID 是 preview 未返回可领取套餐时的回退 planId（实测有效值）。
	defaultStartPlanID   = "zcode-v3-start-plan-0817"
	defaultAliyunRegion  = "sgp"
	defaultAliyunSceneID = "11xygtvd"
	defaultAliyunPrefix  = "no8xfe"
)

var (
	deviceMidOnce sync.Once
	deviceMidVal  string
)

// zcodeDeviceMid 返回进程内稳定的设备指纹（UUID v4）。
//
// claim 与 preview 端点强制校验 `X-Device-Mid`，缺失时上游返回 `code 3001 parameter error`；
// 官方客户端把 deviceMid 持久化在本地 state 文件，插件端在进程生命周期内保持单例即可
// 满足上游契约，同时避免每次请求更换指纹。
// [参数] 无。
// [返回] UUID v4 字符串。
// 最近修改时间：2026-10-14；改动原因：新增，补齐 claim / preview 端点必需的设备指纹头。
func zcodeDeviceMid() string {
	deviceMidOnce.Do(func() {
		deviceMidVal = newUUIDv4()
	})
	return deviceMidVal
}

// newUUIDv4 生成随机 UUID v4 字符串。
// [参数] 无。
// [返回] 形如 xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx 的字符串。
// 最近修改时间：2026-10-14；改动原因：新增，为设备指纹提供 UUID 生成能力。
func newUUIDv4() string {
	var b [16]byte
	if _, errRead := rand.Read(b[:]); errRead != nil {
		// 退化路径：以纳秒时间戳填充，保证非空且格式合法（上游仅做 UUID 格式校验）。
		t := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			b[i] = byte(t >> (8 * i))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// fetchClaimablePlanID 从官方 preview 端点解析当前可领取的 Start Plan 套餐 ID。
//
// 官方客户端（`getManualClaimPlanPreviews`）即以此端点动态获取 planId，避免批次后缀
// 随上游漂移后失效。端点异常、无权限或未返回可领取套餐时返回空串，由调用方回退默认值。
// [参数] jwt: 账号 JWT。
// [返回] 可领取的 plan_id；无法解析时返回空串。
// 最近修改时间：2026-10-14；改动原因：新增，替代无后缀硬编码 planId 以修复 1001 target not found。
func fetchClaimablePlanID(jwt string) string {
	targetURL := zcodeClaimPreviewURL
	if strings.TrimSpace(claimPreviewURLForTest) != "" {
		targetURL = strings.TrimSpace(claimPreviewURLForTest)
	}
	req, errNew := http.NewRequest(http.MethodGet, targetURL, nil)
	if errNew != nil {
		return ""
	}
	query := req.URL.Query()
	query.Set("app_version", mimicAppVersion())
	query.Set("platform", "win32-x64")
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-ZCode-App-Version", mimicAppVersion())
	req.Header.Set("X-Platform", "win32-x64")
	req.Header.Set("X-Device-Mid", zcodeDeviceMid())
	req.Header.Set("HTTP-Referer", "https://zcode.z.ai")

	resp, errDo := modelHTTPClient.Do(req)
	if errDo != nil {
		return ""
	}
	defer resp.Body.Close()
	raw, errRead := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if errRead != nil {
		return ""
	}
	var parsed struct {
		Code int `json:"code"`
		Data struct {
			Plans []struct {
				PlanID string `json:"plan_id"`
			} `json:"plans"`
		} `json:"data"`
	}
	if errUnmarshal := json.Unmarshal(raw, &parsed); errUnmarshal != nil || parsed.Code != 0 {
		return ""
	}
	for _, plan := range parsed.Data.Plans {
		if id := strings.TrimSpace(plan.PlanID); id != "" {
			return id
		}
	}
	return ""
}

// quotaURLForTest 供单元测试注入 mock 端点；为空时使用 zcodeQuotaURL。
var (
	quotaURLForTest        string
	claimURLForTest        string
	claimPreviewURLForTest string
)

// zcodeQuotaTTL 是额度快照的进程内缓存时长，避免面板频繁打开时反复打上游。
const zcodeQuotaTTL = 5 * time.Minute

// zcodeQuotaEntitlement 是单个模型的额度授予项。
type zcodeQuotaEntitlement struct {
	EntitlementID string `json:"entitlement_id"`
	ShowName      string `json:"show_name"`   // 展示名，如 GLM-5.3
	Meter         string `json:"meter"`       // 计量方式，如 model_usage
	UnitType      string `json:"unit_type"`   // 单位，如 token
	Capability    string `json:"capability"`  // 归一化能力，如 model:glm-5.3
	GrantUnits    int64  `json:"grant_units"` // 该周期授予额度
	Period        string `json:"period"`      // 周期，如 daily
	Priority      int64  `json:"priority"`
}

// zcodeQuotaSnapshot 是单个账号的额度快照。
type zcodeQuotaSnapshot struct {
	PlanName     string                  `json:"plan_name"`
	PlanID       string                  `json:"plan_id"`
	PlanStatus   string                  `json:"plan_status"`
	Description  string                  `json:"description,omitempty"`
	StartsAt     int64                   `json:"starts_at,omitempty"`
	EndsAt       int64                   `json:"ends_at,omitempty"`
	Entitlements []zcodeQuotaEntitlement `json:"entitlements"`
	TotalGrant   int64                   `json:"total_grant"` // 全部 entitlement 授予额度合计
	FetchedAt    string                  `json:"fetched_at"`
}

// zcodeQuotaCacheEntry 是缓存条目（含过期时间）。
type zcodeQuotaCacheEntry struct {
	quota     *zcodeQuotaSnapshot
	fetchedAt time.Time
}

var (
	zcodeQuotaMu    sync.RWMutex
	zcodeQuotaCache = make(map[string]zcodeQuotaCacheEntry)
)

// zcodeQuotaResponse 是 billing/current 的响应体。
type zcodeQuotaResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		ServerTime int64 `json:"server_time"`
		Plans      []struct {
			UserPlanID   string `json:"user_plan_id"`
			PlanID       string `json:"plan_id"`
			Name         string `json:"name"`
			Description  string `json:"description"`
			Priority     int64  `json:"priority"`
			Status       string `json:"status"`
			StartsAt     int64  `json:"starts_at"`
			EndsAt       int64  `json:"ends_at"`
			Entitlements []struct {
				EntitlementID string   `json:"entitlement_id"`
				ShowName      string   `json:"show_name"`
				Meter         string   `json:"meter"`
				UnitType      string   `json:"unit_type"`
				Capabilities  []string `json:"capabilities"`
				GrantUnits    int64    `json:"grant_units"`
				Period        string   `json:"period"`
				Priority      int64    `json:"priority"`
			} `json:"entitlements"`
		} `json:"plans"`
	} `json:"data"`
}

// fetchAccountQuota 查询单个账号的额度快照（不走缓存）。
//
// [参数] sa: 账号凭据（需含 JWT）。
// [返回] 额度快照；账号无 JWT 或上游异常时返回错误。
func fetchAccountQuota(sa *StoredAuth) (*zcodeQuotaSnapshot, error) {
	if sa == nil {
		return nil, fmt.Errorf("nil account")
	}
	jwt := strings.TrimSpace(sa.Token)
	if jwt == "" {
		jwt = strings.TrimSpace(sa.APIKey)
	}
	if jwt == "" {
		return nil, fmt.Errorf("账号缺少 JWT，无法查询额度")
	}

	req, errNew := http.NewRequest(http.MethodGet, quotaEndpoint(), nil)
	if errNew != nil {
		return nil, fmt.Errorf("create quota request: %w", errNew)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ZCode/"+mimicAppVersion())
	req.Header.Set("X-ZCode-App-Version", mimicAppVersion())
	req.Header.Set("HTTP-Referer", "https://zcode.z.ai")
	req.Header.Set("X-Title", "Z Code@electron")

	resp, errDo := modelHTTPClient.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("request quota: %w", errDo)
	}
	defer resp.Body.Close()

	raw, errRead := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if errRead != nil {
		return nil, fmt.Errorf("read quota response: %w", errRead)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("quota upstream error %d: %s", resp.StatusCode, string(raw))
	}

	var parsed zcodeQuotaResponse
	if errUnmarshal := json.Unmarshal(raw, &parsed); errUnmarshal != nil {
		return nil, fmt.Errorf("parse quota response: %w", errUnmarshal)
	}
	if parsed.Code != 0 {
		msg := strings.TrimSpace(parsed.Msg)
		if msg == "" {
			msg = fmt.Sprintf("code=%d", parsed.Code)
		}
		return nil, fmt.Errorf("quota query failed: %s", msg)
	}
	if len(parsed.Data.Plans) == 0 {
		return nil, fmt.Errorf("账号无生效计划")
	}

	plan := parsed.Data.Plans[0]
	snap := &zcodeQuotaSnapshot{
		PlanName:    strings.TrimSpace(plan.Name),
		PlanID:      strings.TrimSpace(plan.PlanID),
		PlanStatus:  strings.TrimSpace(plan.Status),
		Description: strings.TrimSpace(plan.Description),
		StartsAt:    plan.StartsAt,
		EndsAt:      plan.EndsAt,
		FetchedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	for _, ent := range plan.Entitlements {
		capability := ""
		if len(ent.Capabilities) > 0 {
			capability = strings.TrimSpace(ent.Capabilities[0])
		}
		snap.Entitlements = append(snap.Entitlements, zcodeQuotaEntitlement{
			EntitlementID: strings.TrimSpace(ent.EntitlementID),
			ShowName:      strings.TrimSpace(ent.ShowName),
			Meter:         strings.TrimSpace(ent.Meter),
			UnitType:      strings.TrimSpace(ent.UnitType),
			Capability:    capability,
			GrantUnits:    ent.GrantUnits,
			Period:        strings.TrimSpace(ent.Period),
			Priority:      ent.Priority,
		})
		snap.TotalGrant += ent.GrantUnits
	}
	return snap, nil
}

// quotaForAccount 返回账号额度，优先命中进程内缓存。
//
// [参数] sa: 账号凭据；force: true 时跳过缓存强制刷新。
// [返回] 额度快照与错误。
func quotaForAccount(sa *StoredAuth, force bool) (*zcodeQuotaSnapshot, error) {
	if sa == nil || strings.TrimSpace(sa.AuthID) == "" {
		return fetchAccountQuota(sa)
	}
	key := sa.AuthID

	zcodeQuotaMu.RLock()
	oldEntry, hasOld := zcodeQuotaCache[key]
	zcodeQuotaMu.RUnlock()

	if !force && hasOld && oldEntry.quota != nil && time.Since(oldEntry.fetchedAt) < zcodeQuotaTTL {
		return oldEntry.quota, nil
	}

	snap, errFetch := fetchAccountQuota(sa)
	if errFetch != nil {
		// 若因新导入账号未开通计划导致，刷新额度时自动尝试一次静默激活
		if strings.Contains(errFetch.Error(), "无生效计划") {
			if claimedSnap, errClaim := claimAccountStartPlan(sa, "", ""); errClaim == nil && claimedSnap != nil {
				return claimedSnap, nil
			}
		}
		// 上游瞬时拉取失败：若存在历史有效快照，优雅回退旧缓存，避免网络抖动导致额度清空
		if hasOld && oldEntry.quota != nil {
			return oldEntry.quota, nil
		}
		return nil, errFetch
	}
	zcodeQuotaMu.Lock()
	zcodeQuotaCache[key] = zcodeQuotaCacheEntry{quota: snap, fetchedAt: time.Now()}
	zcodeQuotaMu.Unlock()
	return snap, nil
}

// claimAccountStartPlan 向官方端点发起套餐激活开通。
//
// [参数] sa: 待激活账号凭据; verifyParam: 阿里云智能验证签名(可为空时进行静默试探); region: 验证码区域。
// [返回] 激活成功后的最新额度快照；若上游要求验证码且入参为空，返回明确错误。
func claimAccountStartPlan(sa *StoredAuth, verifyParam, region string) (*zcodeQuotaSnapshot, error) {
	if sa == nil {
		return nil, fmt.Errorf("nil account")
	}
	jwt := strings.TrimSpace(sa.Token)
	if jwt == "" {
		jwt = strings.TrimSpace(sa.APIKey)
	}
	if jwt == "" {
		return nil, fmt.Errorf("账号缺少 JWT，无法激活套餐")
	}

	targetURL := zcodeClaimURL
	if strings.TrimSpace(claimURLForTest) != "" {
		targetURL = strings.TrimSpace(claimURLForTest)
	}

	// planId 优先由官方 preview 端点动态解析（带批次后缀），失败时回退默认值，
	// 避免上游批次变更后硬编码值返回 1001 target not found。
	planID := fetchClaimablePlanID(jwt)
	if planID == "" {
		planID = defaultStartPlanID
	}

	payload := map[string]string{
		"plan_id": planID,
	}
	reqBody, _ := json.Marshal(payload)

	req, errNew := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(reqBody))
	if errNew != nil {
		return nil, fmt.Errorf("create claim request: %w", errNew)
	}

	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ZCode/3.15.1")
	req.Header.Set("X-ZCode-App-Version", "3.15.1")
	req.Header.Set("X-Platform", "win32-x64")
	req.Header.Set("X-Device-Mid", zcodeDeviceMid())
	req.Header.Set("HTTP-Referer", "https://zcode.z.ai")

	if strings.TrimSpace(verifyParam) != "" {
		req.Header.Set("X-Aliyun-Captcha-Verify-Param", strings.TrimSpace(verifyParam))
		if strings.TrimSpace(region) == "" {
			region = defaultAliyunRegion
		}
		req.Header.Set("X-Aliyun-Captcha-Verify-Region", strings.TrimSpace(region))
	}

	resp, errDo := modelHTTPClient.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("request claim: %w", errDo)
	}
	defer resp.Body.Close()

	raw, errRead := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if errRead != nil {
		return nil, fmt.Errorf("read claim response: %w", errRead)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("claim error %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	_ = json.Unmarshal(raw, &parsed)
	if parsed.Code != 0 {
		msg := strings.TrimSpace(parsed.Msg)
		if msg == "" {
			msg = fmt.Sprintf("code=%d", parsed.Code)
		}
		return nil, fmt.Errorf("claim failed: %s", msg)
	}

	// 激活成功：清除旧缓存并立刻拉取最新生效额度
	invalidateQuota(sa.AuthID)
	return quotaForAccount(sa, true)
}

// cachedQuota 只读缓存中的额度（不触发上游请求），供 /accounts 列表附带展示。
//
// [参数] authID: 账号标识。
// [返回] 已缓存且未过期的额度；无缓存或已过期时返回 nil。
func cachedQuota(authID string) *zcodeQuotaSnapshot {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil
	}
	zcodeQuotaMu.RLock()
	defer zcodeQuotaMu.RUnlock()
	entry, ok := zcodeQuotaCache[authID]
	if !ok || entry.quota == nil || time.Since(entry.fetchedAt) >= zcodeQuotaTTL {
		return nil
	}
	return entry.quota
}

// invalidateQuota 清除某账号的额度缓存（删除账号或探活后调用）。
//
// [参数] authID: 账号标识。
// [返回] 无。
func invalidateQuota(authID string) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	zcodeQuotaMu.Lock()
	delete(zcodeQuotaCache, authID)
	zcodeQuotaMu.Unlock()
}

// quotaEndpoint 返回当前生效的额度查询端点（测试可注入覆盖）。
//
// [参数] 无。
// [返回] 端点 URL。
func quotaEndpoint() string {
	if strings.TrimSpace(quotaURLForTest) != "" {
		return quotaURLForTest
	}
	return zcodeQuotaURL
}

// mimicAppVersion 返回当前指纹使用的 app 版本（额度请求复用同一取值）。
//
// [参数] 无。
// [返回] app 版本字符串。
func mimicAppVersion() string {
	v := strings.TrimSpace(currentConfig().Mimic.AppVersion)
	if v == "" {
		return "unknown"
	}
	return v
}
