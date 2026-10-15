package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// Copilot 配额查询端点。
//
// 关键事实（来源：ericc-ch/copilot-api master 分支
// src/services/github/get-copilot-usage.ts，2026-10-15 调研）：
// `/copilot_internal/user` 返回账号的订阅档位与三类配额快照
// （chat / completions / premium_interactions），每项含 entitlement 与 remaining。
// 该端点同样要求 GitHub API 版本头与编辑器指纹头。
const copilotUserURL = "https://api.github.com/copilot_internal/user"

// copilotQuotaTTL 是额度快照的进程内缓存时长。
//
// 面板 /accounts 只读缓存以免频繁打开时反复打上游；超过该时长视为过期，
// 返回空值而非陈旧额度——展示旧额度比不展示更容易误导用户。
const copilotQuotaTTL = 5 * time.Minute

// copilotQuotaSnapshot 是单个账号的配额快照。
type copilotQuotaSnapshot struct {
	Plan          string          `json:"plan,omitempty"`
	AccessTypeSKU string          `json:"access_type_sku,omitempty"`
	ChatEnabled   bool            `json:"chat_enabled"`
	ResetDate     string          `json:"quota_reset_date,omitempty"`
	Chat          *copilotQuota   `json:"chat,omitempty"`
	Completions   *copilotQuota   `json:"completions,omitempty"`
	Premium       *copilotQuota   `json:"premium_interactions,omitempty"`
	FetchedAt     string          `json:"fetched_at"`
	Error         string          `json:"error,omitempty"`
	Raw           json.RawMessage `json:"-"`
}

// copilotQuota 是单类配额的用量明细。
type copilotQuota struct {
	Entitlement   int64   `json:"entitlement"`
	Remaining     int64   `json:"remaining"`
	PercentRemain float64 `json:"percent_remaining"`
	Unlimited     bool    `json:"unlimited"`
	OverageCount  int64   `json:"overage_count"`
	OveragePermit bool    `json:"overage_permitted"`
	QuotaID       string  `json:"quota_id,omitempty"`
}

var quotaCache struct {
	sync.RWMutex
	byAuthID map[string]*copilotQuotaSnapshot
}

func init() {
	quotaCache.byAuthID = make(map[string]*copilotQuotaSnapshot)
}

// invalidateQuota 使指定账号的配额缓存失效。
func invalidateQuota(authID string) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	quotaCache.Lock()
	delete(quotaCache.byAuthID, authID)
	quotaCache.Unlock()
}

// cachedQuota 读取指定账号的配额快照缓存（不触发上游请求）。
// [参数] authID: 账号标识。
// [返回] 已缓存且未过期的配额快照；无缓存或已过期时返回 nil。
// 最近修改时间：2026-10-15；改动原因：补 TTL 过期判定，避免面板长期展示陈旧额度。
func cachedQuota(authID string) *copilotQuotaSnapshot {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return nil
	}
	quotaCache.RLock()
	snapshot := quotaCache.byAuthID[authID]
	quotaCache.RUnlock()
	if snapshot == nil || snapshot.FetchedAt == "" {
		return nil
	}
	if fetchedAt, err := time.Parse(time.RFC3339, snapshot.FetchedAt); err == nil {
		if time.Since(fetchedAt) >= copilotQuotaTTL {
			return nil
		}
	}
	return snapshot
}

// fetchAccountQuota 查询指定账号的 Copilot 配额。
// [参数] sa: 目标账号凭据。
// [返回] 配额快照与错误。
// 最近修改时间：2026-10-15；改动原因：新增，Copilot 额度查询。
func fetchAccountQuota(sa *StoredAuth) (*copilotQuotaSnapshot, error) {
	if sa == nil {
		return nil, fmt.Errorf("nil account")
	}
	githubToken := strings.TrimSpace(sa.GitHubToken)
	if githubToken == "" {
		githubToken = strings.TrimSpace(sa.APIKey)
	}
	if githubToken == "" {
		return nil, fmt.Errorf("account %s has no usable credential", sa.AuthID)
	}

	req, err := http.NewRequest(http.MethodGet, copilotUserURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create quota request: %w", err)
	}
	applyGitHubHeaders(req.Header, githubToken, currentConfig())

	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request quota: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read quota response: %w", err)
	}

	snapshot := &copilotQuotaSnapshot{
		FetchedAt: time.Now().Format(time.RFC3339),
		Raw:       raw,
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snapshot.Error = fmt.Sprintf("upstream %d", resp.StatusCode)
		storeQuota(sa.AuthID, snapshot)
		return snapshot, fmt.Errorf("quota upstream error %d: %s", resp.StatusCode, string(raw))
	}

	parsed := parseQuotaSnapshot(raw)
	parsed.FetchedAt = snapshot.FetchedAt
	parsed.Raw = raw
	storeQuota(sa.AuthID, parsed)
	return parsed, nil
}

// parseQuotaSnapshot 解析 /copilot_internal/user 响应为配额快照。
//
// 1. 读取订阅档位、SKU、chat 开关与重置日期；
// 2. 逐项解析 chat / completions / premium_interactions 三类配额；
// 3. 未知字段不报错，保证上游响应扩展时插件不崩。
// [参数] raw: 上游响应体。
// [返回] 解析后的配额快照（永不返回 nil）。
// 最近修改时间：2026-10-15；改动原因：新增，抽出纯解析便于单测。
func parseQuotaSnapshot(raw []byte) *copilotQuotaSnapshot {
	snapshot := &copilotQuotaSnapshot{
		Plan:          strings.TrimSpace(gjson.GetBytes(raw, "copilot_plan").String()),
		AccessTypeSKU: strings.TrimSpace(gjson.GetBytes(raw, "access_type_sku").String()),
		ChatEnabled:   gjson.GetBytes(raw, "chat_enabled").Bool(),
		ResetDate:     strings.TrimSpace(gjson.GetBytes(raw, "quota_reset_date").String()),
	}
	snapshot.Chat = parseQuotaDetail(gjson.GetBytes(raw, "quota_snapshots.chat"))
	snapshot.Completions = parseQuotaDetail(gjson.GetBytes(raw, "quota_snapshots.completions"))
	snapshot.Premium = parseQuotaDetail(gjson.GetBytes(raw, "quota_snapshots.premium_interactions"))
	return snapshot
}

// parseQuotaDetail 解析单类配额明细。
// [参数] node: quota_snapshots 下的单类节点。
// [返回] 配额明细；节点不存在时返回 nil。
// 最近修改时间：2026-10-15；改动原因：新增，配额明细统一解析。
func parseQuotaDetail(node gjson.Result) *copilotQuota {
	if !node.Exists() {
		return nil
	}
	return &copilotQuota{
		Entitlement:   node.Get("entitlement").Int(),
		Remaining:     node.Get("remaining").Int(),
		PercentRemain: node.Get("percent_remaining").Float(),
		Unlimited:     node.Get("unlimited").Bool(),
		OverageCount:  node.Get("overage_count").Int(),
		OveragePermit: node.Get("overage_permitted").Bool(),
		QuotaID:       strings.TrimSpace(node.Get("quota_id").String()),
	}
}

// storeQuota 写入配额缓存。
func storeQuota(authID string, snapshot *copilotQuotaSnapshot) {
	authID = strings.TrimSpace(authID)
	if authID == "" || snapshot == nil {
		return
	}
	quotaCache.Lock()
	quotaCache.byAuthID[authID] = snapshot
	quotaCache.Unlock()
}
