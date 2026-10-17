// daily_cooldown.go 实现 QwenWork 账号遭遇 110 (Billing daily count exceeded) 时的
// 24 小时持久化冷却引擎。
//
// Qoder 账号存在每日请求调用次数上限，遭遇 110 限制时意味着当日配额耗尽。
// 该机制将冷却截止时间持久化写入物理 auth JSON 文件的 daily_cooling_until 字段，
// 并由内存镜像保障调度器与换号逻辑高效排除该账号，持续整整 24 小时。
// 严格遵循「仅到期自然解除」策略，同时支持面板管理接口人工手动解除。
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// dailyCooldownDuration 定义 Qoder 账号遭遇日请求次数上限后的固定冷却时长（24 小时）。
const dailyCooldownDuration = 24 * time.Hour

var (
	dailyCoolingMu  sync.RWMutex
	dailyCoolingMap = make(map[string]time.Time) // authID / normalizedKey -> 冷却截止时间
)

// parseDailyCoolingUntilFromAuthJSON 从物理 auth 文件 JSON 载荷中解析 daily_cooling_until。
// [参数] raw: 物理认证文件原始 JSON 字节流。
// [返回] 解析成功的截止时间；缺失或格式错误时返回 time.Time{} 零值。
func parseDailyCoolingUntilFromAuthJSON(raw []byte) time.Time {
	var doc struct {
		DailyCoolingUntil string `json:"daily_cooling_until"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return time.Time{}
	}
	rawStr := strings.TrimSpace(doc.DailyCoolingUntil)
	if rawStr == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, rawStr)
	if err != nil {
		return time.Time{}
	}
	return t
}

// isAccountDailyCooling 判定指定账号当前是否仍处于 24 小时日限额冷却窗口内。
// 支持原始 auth_id、文件名以及 normalizeFailoverKey 双向模糊归一化匹配。
// [参数] authID: 账号唯一标识。
// [返回] 处于冷却窗口内返回 true；未冷却或已自然过期返回 false。
func isAccountDailyCooling(authID string) bool {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return false
	}
	dailyCoolingMu.RLock()
	defer dailyCoolingMu.RUnlock()

	now := time.Now()
	if until, ok := dailyCoolingMap[authID]; ok && now.Before(until) {
		return true
	}
	target := normalizeFailoverKey(authID)
	if target == "" {
		return false
	}
	if until, ok := dailyCoolingMap[target]; ok && now.Before(until) {
		return true
	}
	for k, until := range dailyCoolingMap {
		if !now.Before(until) {
			continue
		}
		normK := normalizeFailoverKey(k)
		if normK == target || strings.Contains(normK, target) || strings.Contains(target, normK) {
			return true
		}
	}
	return false
}

// getAccountDailyCoolingUntil 获取指定账号的冷却截止时间。
// [参数] authID: 账号唯一标识。
// [返回] 仍在冷却中时返回截止时间；否则返回零值。
func getAccountDailyCoolingUntil(authID string) time.Time {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return time.Time{}
	}
	dailyCoolingMu.RLock()
	defer dailyCoolingMu.RUnlock()

	now := time.Now()
	if until, ok := dailyCoolingMap[authID]; ok && now.Before(until) {
		return until
	}
	target := normalizeFailoverKey(authID)
	if target == "" {
		return time.Time{}
	}
	if until, ok := dailyCoolingMap[target]; ok && now.Before(until) {
		return until
	}
	for k, until := range dailyCoolingMap {
		if !now.Before(until) {
			continue
		}
		normK := normalizeFailoverKey(k)
		if normK == target || strings.Contains(normK, target) || strings.Contains(target, normK) {
			return until
		}
	}
	return time.Time{}
}

// triggerDailyCooldown 将指定账号置入 24 小时每日调用上限冷却状态，并持久化到物理文件。
// [参数] authIndex: 宿主 RPC 索引（可空，为空时自动反查）；authID: 账号唯一标识。
// [返回] 写入与持久化成功返回 nil；内存冷却已生效但落盘失败时返回包装错误。
// 最近修改时间 2026-10-17 21:30:00 落盘失败保留内存冷却并返回可定位错误。
func triggerDailyCooldown(authIndex, authID string) error {
	authIndex = strings.TrimSpace(authIndex)
	authID = strings.TrimSpace(authID)

	until := time.Now().Add(dailyCooldownDuration)

	// 1. 先写入内存镜像
	dailyCoolingMu.Lock()
	dailyCoolingMap[authID] = until
	if norm := normalizeFailoverKey(authID); norm != "" {
		dailyCoolingMap[norm] = until
	}
	dailyCoolingMu.Unlock()

	if authIndex == "" && authID != "" {
		if idx, err := hostAuthIndexForPhys(authID); err == nil {
			authIndex = idx
		}
	}
	if authIndex == "" {
		return fmt.Errorf("daily cooling persist: resolve auth index: %w", errAuthIndexRequired())
	}

	// 2. 直写物理 auth 文件完成持久化落盘
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return fmt.Errorf("daily cooling persist: resolve physical auth: %w", err)
	}
	if phys == nil || len(phys.JSON) == 0 {
		return fmt.Errorf("daily cooling persist: %w", errAuthMissing())
	}

	var doc map[string]any
	if err := json.Unmarshal(phys.JSON, &doc); err != nil {
		doc = make(map[string]any)
	}
	doc["daily_cooling_until"] = until.UTC().Format(time.RFC3339)

	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("daily cooling persist: marshal auth doc: %w", err)
	}
	if err := persistAuthDirect(phys.Name, phys.Path, "", raw); err != nil {
		return fmt.Errorf("daily cooling persist: write auth file: %w", err)
	}

	log.Printf("[qwenwork] account %s (auth_index=%s) triggered 24h daily cooling until %s", authID, authIndex, until.Format(time.RFC3339))
	return nil
}

// dailyCooldownPersistFailureReason 把落盘错误收敛为一行可读原因，供调用点记录日志。
// [参数] err：triggerDailyCooldown 返回的错误；nil 表示落盘成功。
// [返回] 失败原因文本；err 为 nil 时返回空串。
// 最近修改时间 2026-10-17 21:30:00 冷却落盘失败补日志。
func dailyCooldownPersistFailureReason(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "未知落盘错误"
	}
	if strings.Contains(msg, "auth file missing or empty") {
		return "物理路径缺失：" + msg
	}
	return "落盘失败：" + msg
}

// logDailyCooldownPersistFailure 在落盘失败时记录一行中文日志。
// [参数] authID：账号标识；err：triggerDailyCooldown 返回的错误。
// [返回] 无。
// 最近修改时间 2026-10-17 21:30:00 冷却落盘失败补日志。
func logDailyCooldownPersistFailure(authID string, err error) {
	reason := dailyCooldownPersistFailureReason(err)
	if reason == "" {
		return
	}
	log.Printf("[qwenwork] account %s daily cooling persist failed, memory cooling still active: %s", authID, reason)
}

// clearDailyCooldown 手动解除指定账号的每日限额冷却状态，并从物理文件中移除字段。
// [参数] authIndex: 宿主 RPC 索引（可空，为空时自动反查）；authID: 账号唯一标识。
// [返回] 清除与落盘成功返回 nil。
func clearDailyCooldown(authIndex, authID string) error {
	authIndex = strings.TrimSpace(authIndex)
	authID = strings.TrimSpace(authID)
	if authIndex == "" && authID != "" {
		if idx, err := hostAuthIndexForPhys(authID); err == nil {
			authIndex = idx
		}
	}
	if authIndex == "" {
		return errAuthIndexRequired()
	}

	// 1. 清理内存状态
	dailyCoolingMu.Lock()
	if authID != "" {
		delete(dailyCoolingMap, authID)
		if norm := normalizeFailoverKey(authID); norm != "" {
			delete(dailyCoolingMap, norm)
		}
	}
	dailyCoolingMu.Unlock()

	// 2. 物理文件持久化剥离字段
	phys, err := hostAuthGetPhysical(authIndex)
	if err != nil {
		return err
	}
	if phys == nil || len(phys.JSON) == 0 {
		return errAuthMissing()
	}

	var doc map[string]any
	if err := json.Unmarshal(phys.JSON, &doc); err != nil {
		return nil
	}
	if _, ok := doc["daily_cooling_until"]; !ok {
		return nil
	}
	delete(doc, "daily_cooling_until")

	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := persistAuthDirect(phys.Name, phys.Path, "", raw); err != nil {
		return err
	}

	log.Printf("[qwenwork] account %s (auth_index=%s) daily cooling cleared manually", authID, authIndex)
	return nil
}

// mergeActiveDailyCooling 把仍有效但磁盘上不存在的内存冷却条目并入扫描结果。
// [参数] fromDisk：磁盘扫描得到的冷却字典；inMemory：当前内存镜像；now：判定时刻。
// [返回] 合并后的新字典，不修改入参。
// 最近修改时间 2026-10-17 21:30:00 落盘失败时内存冷却跨 watchdog 刷新保留。
func mergeActiveDailyCooling(fromDisk, inMemory map[string]time.Time, now time.Time) map[string]time.Time {
	merged := make(map[string]time.Time, len(fromDisk)+len(inMemory))
	for key, until := range fromDisk {
		merged[key] = until
	}
	for key, until := range inMemory {
		if !now.Before(until) {
			continue
		}
		if _, exists := merged[key]; exists {
			continue
		}
		merged[key] = until
	}
	return merged
}

// refreshDailyCoolingSetFromDisk 从磁盘物理文件中重新扫描并同步内存冷却时间字典。
// [返回] 当前处于冷却窗口内的账号数。
func refreshDailyCoolingSetFromDisk() int {
	files, err := hostAuthList()
	if err != nil {
		dailyCoolingMu.RLock()
		defer dailyCoolingMu.RUnlock()
		return len(dailyCoolingMap)
	}

	now := time.Now()
	nextMap := make(map[string]time.Time)

	for _, f := range files {
		phys, physErr := hostAuthGetPhysical(f.AuthIndex)
		if physErr != nil || phys == nil || len(phys.JSON) == 0 {
			continue
		}
		until := parseDailyCoolingUntilFromAuthJSON(phys.JSON)
		if !until.IsZero() && now.Before(until) {
			nextMap[f.ID] = until
			if norm := normalizeFailoverKey(f.ID); norm != "" {
				nextMap[norm] = until
			}
		}
	}

	dailyCoolingMu.Lock()
	dailyCoolingMap = mergeActiveDailyCooling(nextMap, dailyCoolingMap, now)
	count := len(dailyCoolingMap)
	dailyCoolingMu.Unlock()
	return count
}

// hostAuthIndexForPhys 根据账号标识反查对应的宿主 auth_index。
//
// 账号标识可能来自三条路径：宿主 auth.ID（qwenwork-<uid>.json）、裸 UID、
// 或已归一化的文件名。这里按「ID → Name → UID」逐级匹配，避免因形态差异
// 反查失败而让冷却只停在内存、无法落盘。
//
// [参数] authID: 账号标识。
// [返回] 匹配的 auth_index；未找到返回 errAuthMissing()。
// 最近修改时间：2026-10-18 01:55:00；改动原因：补 Name/UID 级匹配，兼容不同标识形态。
func hostAuthIndexForPhys(authID string) (string, error) {
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return "", errAuthMissing()
	}
	files, err := hostAuthList()
	if err != nil {
		return "", err
	}
	// 1. 直接命中宿主 auth.ID。
	for _, f := range files {
		if f.ID == authID {
			return f.AuthIndex, nil
		}
	}
	// 2. 命中文件名（调用方可能传 qwenwork-<uid>.json）。
	for _, f := range files {
		if strings.EqualFold(f.Name, authID) {
			return f.AuthIndex, nil
		}
	}
	// 3. 命中裸 UID：与 authFileNameFor 生成的规范名比较。
	wantName := authFilePrefix + sanitizeUIDForFileName(authID) + ".json"
	for _, f := range files {
		if strings.EqualFold(f.Name, wantName) {
			return f.AuthIndex, nil
		}
	}
	return "", errAuthMissing()
}
