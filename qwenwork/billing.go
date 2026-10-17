// billing.go owns the QwenWork account quota and plan snapshots.
//
// 上游两个端点（都需要宿主 Bearer，不是 MaaS key）：
//
//	GET /api/v1/adapter/user/account-context?include=user,plan,quota,page,data_sharing
//	GET /api/v1/adapter/user/wallets
//
// 实测形态：quota.total / quota.used 可能为 null，只有 quota.remaining 与
// 钱包三档余额可靠，所以额度聚合以 remaining + daily/monthly/longterm 为准。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// accountContextEnvelope 镜像 account-context 的响应外壳。
type accountContextEnvelope struct {
	Code string `json:"code"`
	Data struct {
		User struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
			Email    string `json:"email"`
			IsBiz    bool   `json:"is_biz"`
		} `json:"user"`
		Plan struct {
			Name              string `json:"name"`
			UserType          string `json:"user_type"`
			IsPersonalVersion bool   `json:"is_personal_version"`
			IsSubscribed      bool   `json:"is_subscribed"`
			SubscriptionStat  bool   `json:"subscription_status"`
			Period            string `json:"period"`
			NextRefreshDate   string `json:"next_refresh_date"`
		} `json:"plan"`
		Quota struct {
			Total     *float64 `json:"total"`
			Used      *float64 `json:"used"`
			Remaining *float64 `json:"remaining"`
			Exceeded  bool     `json:"exceeded"`
		} `json:"quota"`
	} `json:"data"`
}

// walletsEnvelope 镜像 wallets 的响应外壳。
type walletsEnvelope struct {
	Code string `json:"code"`
	Data struct {
		DailyCredits struct {
			TotalBalance float64 `json:"total_balance"`
		} `json:"daily_credits"`
		MonthlyCredits struct {
			TotalBalance float64 `json:"total_balance"`
		} `json:"monthly_credits"`
		LongtermCredits struct {
			TotalBalance float64 `json:"total_balance"`
		} `json:"longterm_credits"`
		ExpiringSoon struct {
			Count        int     `json:"count"`
			TotalBalance float64 `json:"total_balance"`
		} `json:"expiring_soon"`
	} `json:"data"`
}

// billingGet 用宿主 token 发一次 GET 并返回原始响应体。
//
// [参数] sa 账号凭据；url 完整地址。
// [返回] 响应体；网络错误或非 200 时返回错误。
// 最近修改时间：2026-10-18 00:02:00；改动原因：新建 QwenWork 额度层。
func billingGet(sa *storedAuth, url string) ([]byte, error) {
	hostToken := strings.TrimSpace(sa.Auth.AccessToken)
	if hostToken == "" {
		return nil, fmt.Errorf("billing: account has no host access token")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	commonHeaders(req)
	req.Header.Set("Authorization", "Bearer "+hostToken)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("billing: http %d body=%s", resp.StatusCode, truncateRedacted(string(resp.Body), 200))
	}
	return resp.Body, nil
}

// fetchPaymentType 返回账号的套餐展示名，例如「个人免费版」。
//
// [参数] sa 账号凭据。
// [返回] 套餐名；失败时返回空串（面板降级显示，不影响主链路）。
// 最近修改时间：2026-10-18 00:02:00；改动原因：新建 QwenWork 额度层。
func fetchPaymentType(sa *storedAuth) string {
	raw, err := billingGet(sa, endpointUserInfo)
	if err != nil {
		return ""
	}
	var env accountContextEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	name := strings.TrimSpace(env.Data.Plan.Name)
	if name != "" {
		return name
	}
	if env.Data.Plan.UserType != "" {
		return env.Data.Plan.UserType
	}
	return ""
}

// fetchUserResource 聚合账号可用额度。
//
// QwenWork 的 quota.total/used 常为 null，因此 TotalSize 由 remaining 与
// 钱包余额推导；Packages 拆成「日额度 / 月额度 / 长期额度」三档，便于面板
// 直接展示来源。
//
// [参数] sa 账号凭据。
// [返回] 聚合后的额度快照；任一请求失败时返回错误。
// 最近修改时间：2026-10-18 00:02:00；改动原因：新建 QwenWork 额度层。
func fetchUserResource(sa *storedAuth) (*creditsSummary, error) {
	ctxRaw, errCtx := billingGet(sa, endpointUserInfo)
	walRaw, errWal := billingGet(sa, endpointUserWallets)
	if errCtx != nil && errWal != nil {
		return nil, fmt.Errorf("credits: account-context=%v wallets=%v", errCtx, errWal)
	}

	var remaining float64
	exceeded := false
	if errCtx == nil {
		var env accountContextEnvelope
		if err := json.Unmarshal(ctxRaw, &env); err == nil {
			if env.Data.Quota.Remaining != nil {
				remaining = *env.Data.Quota.Remaining
			}
			exceeded = env.Data.Quota.Exceeded
		}
	}

	var daily, monthly, longterm, expiring float64
	var expiringCount int
	if errWal == nil {
		var env walletsEnvelope
		if err := json.Unmarshal(walRaw, &env); err == nil {
			daily = env.Data.DailyCredits.TotalBalance
			monthly = env.Data.MonthlyCredits.TotalBalance
			longterm = env.Data.LongtermCredits.TotalBalance
			expiring = env.Data.ExpiringSoon.TotalBalance
			expiringCount = env.Data.ExpiringSoon.Count
		}
	}

	packs := make([]packageSummary, 0, 3)
	addPack := func(name string, balance float64) {
		if balance <= 0 {
			return
		}
		packs = append(packs, packageSummary{
			Name:   name,
			Remain: int64(balance),
			Size:   int64(balance),
		})
	}
	addPack("日额度", daily)
	addPack("月额度", monthly)
	addPack("长期额度", longterm)

	total := daily + monthly + longterm
	// 钱包三项都取不到时退回 quota.remaining，避免面板显示 0。
	if total == 0 && remaining > 0 {
		total = remaining
	}

	summary := &creditsSummary{
		TotalRemain: int64(total),
		TotalSize:   int64(total),
		PackCount:   len(packs),
		Packages:    packs,
		FetchedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if exceeded {
		// 上游明确标记超额时把可用额度归零，供 lifecycle 判定禁用。
		summary.TotalRemain = 0
	}
	if expiringCount > 0 {
		summary.Packages = append(summary.Packages, packageSummary{
			Name:   fmt.Sprintf("即将过期(%d)", expiringCount),
			Remain: int64(expiring),
			Size:   int64(expiring),
		})
	}
	return summary, nil
}
