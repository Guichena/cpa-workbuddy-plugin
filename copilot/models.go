package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// defaultModels 是 Copilot 上游不可达时的兜底模型清单。
//
// Copilot 的真实可用模型随订阅档位变化，运行时优先走 /models 动态发现；
// 该清单只保证首次请求前模型出口非空，不承诺上游一定可用。
var defaultModels = []string{
	"gpt-4.1",
	"gpt-4o",
	"claude-sonnet-4",
	"o4-mini",
	"gemini-2.5-pro",
}

var (
	// modelHTTPClient 用于模型列表、令牌换取等元数据短请求，15 秒整体超时即快速失败边界。
	modelHTTPClient = &http.Client{Timeout: 15 * time.Second}

	// inferenceHTTPClient 专用于推理请求（流式 / 非流式）：不设整体超时，避免长思考输出
	// 在读响应体时被掐断；建连挂死改由按操作的 deadline 兜底。
	inferenceHTTPClient = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        20,
			IdleConnTimeout:     90 * time.Second,
			MaxIdleConnsPerHost: 5,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 90 * time.Second,
		},
	}

	modelCache struct {
		sync.RWMutex
		byKey     map[string][]string
		expiresAt map[string]time.Time
	}
)

func init() {
	modelCache.byKey = make(map[string][]string)
	modelCache.expiresAt = make(map[string]time.Time)
}

// upstreamModelItem 是上游 /models 的单个条目（只保留过滤所需字段）。
type upstreamModelItem struct {
	ID           string `json:"id"`
	Vendor       string `json:"vendor"`
	Capabilities struct {
		Type   string `json:"type"`
		Family string `json:"family"`
	} `json:"capabilities"`
	Policy struct {
		State string `json:"state"`
	} `json:"policy"`
}

type upstreamModelListResponse struct {
	Data []upstreamModelItem `json:"data"`
}

// internalModelFamilies 是 Copilot 上游暴露的内部工具项，调用对话接口必报错。
//
// 这些条目与真实对话模型一样带 capabilities.type=chat，无法靠 type 字段区分；
// 实测（2026-10-15）它们同时满足 vendor=Fireworks 且 family 为 *-agent /
// trajectory-compaction，故按 family 前缀剔除。
var internalModelFamilies = []string{
	"search-agent",
	"exec-agent",
	"trajectory-compaction",
}

// isInternalToolModel 判断上游条目是否为不可对话的内部工具项。
// [参数] family: capabilities.family 取值；vendor: 条目 vendor 取值。
// [返回] true 表示应剔除。
// 最近修改时间：2026-10-15 23:20:00；改动原因：新增，剔除内部子代理工具项。
func isInternalToolModel(family, vendor string) bool {
	if !strings.EqualFold(strings.TrimSpace(vendor), "fireworks") {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(family))
	for _, prefix := range internalModelFamilies {
		if normalized == prefix || strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

// isChatCapableModel 判断上游条目是否为该账号可用的对话模型。
//
// 判据（2026-10-15 生产实测 57 条目）：
//  1. capabilities.type 必须为 chat（排除 embeddings / completion）；
//  2. policy.state 为 disabled 表示当前账号无权使用，必须剔除（21/57 命中）；
//  3. model_picker_enabled 全为 false，不可用作判据；
//  4. 内部子代理工具项按 family 剔除。
//
// [参数] item: 上游模型条目。
// [返回] true 表示可用对话模型。
// 最近修改时间：2026-10-15 23:20:00；改动原因：新增，按上游契约过滤可用对话模型。
func isChatCapableModel(item upstreamModelItem) bool {
	if !strings.EqualFold(strings.TrimSpace(item.Capabilities.Type), "chat") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(item.Policy.State), "disabled") {
		return false
	}
	return !isInternalToolModel(item.Capabilities.Family, item.Vendor)
}

// fetchUpstreamModels 使用指定账号向上游 /models 拉取可用对话模型清单。
//
// 动态发现成功时返回上游权威清单（已按 capabilities / policy 过滤）；
// 未启用动态发现、无凭据或上游失败时返回 nil，由调用方按
// 「配置 models > 静态兜底」优先级链回退。
// [参数] ctx: 上下文；sa: 目标账号凭据。
// [返回] 可用模型 ID 列表与错误；动态不可用时列表为 nil。
// 最近修改时间：2026-10-15 23:20:00；改动原因：改为返回 nil 交由调用方兜底，并落地 dynamic_models 开关。
func fetchUpstreamModels(ctx context.Context, sa *StoredAuth) ([]string, error) {
	if sa == nil {
		return nil, fmt.Errorf("nil account")
	}
	cfg := currentConfig()
	if cfg.DynamicModels != nil && !*cfg.DynamicModels {
		return nil, fmt.Errorf("dynamic model discovery disabled by config")
	}

	cacheKey := sa.AuthID
	if strings.TrimSpace(cacheKey) == "" {
		cacheKey = authIDFor(sa.GitHubToken)
	}

	modelCache.RLock()
	if exp, ok := modelCache.expiresAt[cacheKey]; ok && time.Now().Before(exp) {
		if list, found := modelCache.byKey[cacheKey]; found && len(list) > 0 {
			defer modelCache.RUnlock()
			return append([]string(nil), list...), nil
		}
	}
	modelCache.RUnlock()

	token, errToken := ensureCopilotToken(sa)
	if errToken != nil {
		return nil, errToken
	}

	reqURL := resolveCopilotBaseURL(sa, cfg) + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	applyCopilotHeaders(req.Header, sa, cfg, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("models upstream status %d", resp.StatusCode)
	}

	var parsed upstreamModelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var models []string
	seen := make(map[string]struct{})
	for _, item := range parsed.Data {
		if !isChatCapableModel(item) {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			models = append(models, id)
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("models upstream returned no chat-capable model")
	}

	ttl := time.Duration(cfg.ModelCacheSecs) * time.Second
	if ttl <= 0 {
		ttl = 300 * time.Second
	}
	modelCache.Lock()
	modelCache.byKey[cacheKey] = models
	modelCache.expiresAt[cacheKey] = time.Now().Add(ttl)
	modelCache.Unlock()

	return models, nil
}

// cachedModelsForIndex 获取指定账号的模型列表，**只读缓存，不发起上游请求**。
//
// 供宿主高频调用路径使用（`model.static` 的全局模型注册、面板 `/accounts` 渲染）：
// 这些路径在单次调用中会遍历全部账号，若每账号都触发一次上游请求，上游不可达时
// 每账号阻塞至 15 秒超时，会拖垮宿主模型注册与面板加载。动态拉取只在
// `authModelsForIndex`（面板 /models、model.for_auth、测试探活）中发生。
// [参数] authID: 账号标识（可带 .json 后缀）。
// [返回] 模型 ID 列表；无缓存时按落盘清单 → 配置 → 静态兜底降级。
// 最近修改时间：2026-10-16 00:20:00；改动原因：新增，隔离高频路径的上游请求。
func cachedModelsForIndex(authID string) []string {
	authID = strings.TrimSpace(authID)
	cleanID := strings.TrimSuffix(authID, ".json")

	accounts, err := listAllAuthFiles()
	if err != nil {
		return append([]string(nil), defaultModels...)
	}
	for _, acc := range accounts {
		if acc.AuthID != cleanID && acc.AuthID != authID && authFileNameFor(acc.GitHubToken) != cleanID+".json" {
			continue
		}
		cacheKey := acc.AuthID
		if strings.TrimSpace(cacheKey) == "" {
			cacheKey = authIDFor(acc.GitHubToken)
		}
		modelCache.RLock()
		if exp, ok := modelCache.expiresAt[cacheKey]; ok && time.Now().Before(exp) {
			if list, found := modelCache.byKey[cacheKey]; found && len(list) > 0 {
				cached := append([]string(nil), list...)
				modelCache.RUnlock()
				return cached
			}
		}
		modelCache.RUnlock()

		if len(acc.Models) > 0 {
			return append([]string(nil), acc.Models...)
		}
		break
	}
	if cfg := currentConfig(); len(cfg.Models) > 0 {
		return append([]string(nil), cfg.Models...)
	}
	return append([]string(nil), defaultModels...)
}

// authModelsForIndex 获取指定账号的可用模型列表（动态优先）。
//
// 优先级链（对齐既有插件成熟策略）：上游动态发现 > 账号落盘缓存 > 配置 models > 静态兜底。
// 之所以动态优先：上游新增模型只能从 /models 拿到，若账号文件里的旧清单优先，
// 新模型会被永久遮蔽（本 Bug 的成因）。账号落盘的 models 降级为**离线缓存**，
// 仅在上游不可达时保证出口非空。
//
// 本函数会发起上游请求，只应在用户主动触发或按账号注册的路径调用；
// 宿主高频遍历路径请用 cachedModelsForIndex。
// [参数] authID: 账号标识（可带 .json 后缀）。
// [返回] 模型 ID 列表；账号不存在时返回静态兜底清单。
// 最近修改时间：2026-10-15 23:20:00；改动原因：反转为动态优先，修复落盘兜底清单遮蔽上游模型。
func authModelsForIndex(authID string) []string {
	authID = strings.TrimSpace(authID)
	cleanID := strings.TrimSuffix(authID, ".json")
	accounts, err := listAllAuthFiles()
	if err == nil {
		for _, acc := range accounts {
			if acc.AuthID != cleanID && acc.AuthID != authID && authFileNameFor(acc.GitHubToken) != cleanID+".json" {
				continue
			}
			if models, errFetch := fetchUpstreamModels(context.Background(), acc); errFetch == nil && len(models) > 0 {
				return models
			}
			if len(acc.Models) > 0 {
				return acc.Models
			}
			if cfg := currentConfig(); len(cfg.Models) > 0 {
				return append([]string(nil), cfg.Models...)
			}
			break
		}
	}
	return append([]string(nil), defaultModels...)
}

// allConfiguredModels 返回本 provider 对外暴露的全部模型 ModelInfo。
//
// 来源优先级：各账号缓存清单 > 配置 models > 静态兜底清单。
// 只读缓存不触发上游请求：本函数被宿主 model.static / model.register 高频调用，
// 逐账号发请求会让模型注册被上游超时拖垮（详见 cachedModelsForIndex）。
// [参数] 无。
// [返回] 模型元数据列表。
// 最近修改时间：2026-10-16 00:20:00；改动原因：改用只读缓存路径，避免模型注册阻塞。
func allConfiguredModels() []pluginapi.ModelInfo {
	cfg := currentConfig()
	modelsSet := make(map[string]struct{})

	if accounts, err := listAllAuthFiles(); err == nil {
		for _, acc := range accounts {
			for _, m := range cachedModelsForIndex(acc.AuthID) {
				if strings.TrimSpace(m) != "" {
					modelsSet[strings.TrimSpace(m)] = struct{}{}
				}
			}
		}
	}
	if len(modelsSet) == 0 {
		for _, m := range cfg.Models {
			if strings.TrimSpace(m) != "" {
				modelsSet[strings.TrimSpace(m)] = struct{}{}
			}
		}
	}
	if len(modelsSet) == 0 {
		for _, m := range defaultModels {
			modelsSet[m] = struct{}{}
		}
	}

	var result []pluginapi.ModelInfo
	for id := range modelsSet {
		result = append(result, pluginapi.ModelInfo{
			ID:                         id,
			Object:                     "model",
			OwnedBy:                    copilotProviderID,
			DisplayName:                id,
			Name:                       mappedModel(id, cfg),
			SupportedGenerationMethods: []string{"chat"},
			ContextLength:              128000,
			MaxCompletionTokens:        defaultMaxCompletionTokens,
			UserDefined:                true,
		})
	}
	return result
}

// defaultMaxCompletionTokens 是模型元数据的输出上限保底值。
//
// Copilot 上游按请求声明的 max_tokens 计费与限流，过小会截断长回答，
// 过大易触发上游 413；取一个通用的中位值作为元数据展示口径。
const defaultMaxCompletionTokens = 32768

// filterExcludedModels 剔除宿主 oauth-excluded-models 中为 copilot-provider 列出的模型。
//
// 宿主只在 model.for_auth 的按账号注册路径上替插件过滤（sdk/cliproxy/service_executors.go
// 的 applyExcludedModels），model.static 返回的列表会被原样登记进全局模型目录并参与
// 路由选模（internal/pluginhost/adapters.go 的 RegisterModels 无排除调用），因此插件必须
// 在出口自行过滤；同名裸模型被多个 OAuth 插件同时认领时，这是唯一能把模型从某个服务商
// 摘掉的入口。
// [参数] models: 插件已解析完成的模型列表；host: 宿主随请求传入的配置摘要。
// [返回] 过滤后的模型列表；宿主未配置排除项时原样返回入参。
// 最近修改时间：2026-10-15；改动原因：新增，与既有 5 个 OAuth 插件保持一致的排除语义。
func filterExcludedModels(models []pluginapi.ModelInfo, host pluginapi.HostConfigSummary) []pluginapi.ModelInfo {
	if len(host.ExcludedModels) == 0 {
		return models
	}
	excluded := host.ExcludedModels[providerName]
	if len(excluded) == 0 {
		for channel, list := range host.ExcludedModels {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				excluded = list
				break
			}
		}
	}
	if len(excluded) == 0 {
		return models
	}
	excludeSet := make(map[string]struct{}, len(excluded))
	for _, m := range excluded {
		excludeSet[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
	}
	// 返回新切片而非 models[:0] 就地压缩：入参可能来自账号模型缓存或
	// allConfiguredModels 的底层数组，就地写回会让缓存永久变成"已过滤列表"。
	out := make([]pluginapi.ModelInfo, 0, len(models))
	for _, m := range models {
		if _, skip := excludeSet[strings.ToLower(m.ID)]; skip {
			continue
		}
		out = append(out, m)
	}
	return out
}

var modelAliasCache struct {
	sync.RWMutex
	byAlias map[string]string
}

func cacheModelAliases(host pluginapi.HostConfigSummary) {
	entries := host.OAuthModelAlias[providerName]
	if len(entries) == 0 {
		for channel, list := range host.OAuthModelAlias {
			if strings.EqualFold(strings.TrimSpace(channel), providerName) {
				entries = list
				break
			}
		}
	}
	byAlias := make(map[string]string, len(entries))
	for _, e := range entries {
		name := strings.TrimSpace(e.Name)
		alias := strings.TrimSpace(e.Alias)
		if name == "" || alias == "" || strings.EqualFold(name, alias) {
			continue
		}
		byAlias[strings.ToLower(alias)] = name
	}
	modelAliasCache.Lock()
	modelAliasCache.byAlias = byAlias
	modelAliasCache.Unlock()
}

// resolveUpstreamModel 将客户端请求的别名或模型标识映射为上游真实模型 ID。
// [参数] model: 客户端请求的模型名；attributes: 账号属性（可携带别名覆盖）。
// [返回] 上游模型 ID。
func resolveUpstreamModel(model string, attributes map[string]string) string {
	m := strings.TrimSpace(model)
	if m == "" {
		return model
	}
	key := strings.ToLower(m)
	if name, ok := parseModelAliasAttribute(attributes)[key]; ok {
		return name
	}
	modelAliasCache.RLock()
	name, ok := modelAliasCache.byAlias[key]
	modelAliasCache.RUnlock()
	if ok {
		return name
	}
	return m
}

// parseModelAliasAttribute 解析账号属性中携带的模型别名表。
// [参数] attributes: 账号属性键值对。
// [返回] 别名为键、真实模型名为值的映射；无别名时返回 nil。
func parseModelAliasAttribute(attributes map[string]string) map[string]string {
	if len(attributes) == 0 {
		return nil
	}
	raw := ""
	for _, k := range []string{"model_alias", "model-alias", "oauth-model-alias"} {
		if v := strings.TrimSpace(attributes[k]); v != "" {
			raw = v
			break
		}
	}
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	add := func(name, alias string) {
		name, alias = strings.TrimSpace(name), strings.TrimSpace(alias)
		if name != "" && alias != "" && !strings.EqualFold(name, alias) {
			out[strings.ToLower(alias)] = name
		}
	}
	if strings.HasPrefix(raw, "[") {
		var list []struct {
			Name  string `json:"name"`
			Alias string `json:"alias"`
		}
		if json.Unmarshal([]byte(raw), &list) == nil {
			for _, e := range list {
				add(e.Name, e.Alias)
			}
			return out
		}
	}
	if strings.HasPrefix(raw, "{") {
		var m map[string]string
		if json.Unmarshal([]byte(raw), &m) == nil {
			for alias, name := range m {
				add(name, alias)
			}
			return out
		}
	}
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			add(kv[1], kv[0])
		}
	}
	return out
}

// mappedModel 按配置的 model_map 与别名表解析上游模型名。
// [参数] model: 客户端请求模型名；cfg: 当前插件配置。
// [返回] 上游模型名。
func mappedModel(model string, cfg Config) string {
	if upstream, exists := cfg.ModelMap[model]; exists && strings.TrimSpace(upstream) != "" {
		return strings.TrimSpace(upstream)
	}
	return resolveUpstreamModel(model, nil)
}
