package main

import (
	"context"
	"encoding/json"
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

type upstreamModelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// fetchUpstreamModels 使用指定账号向上游 /models 拉取模型清单。
// [参数] ctx: 上下文；sa: 目标账号凭据。
// [返回] 模型 ID 列表与错误；失败时回退默认清单。
// 最近修改时间：2026-10-15；改动原因：新增，Copilot 模型动态发现。
func fetchUpstreamModels(ctx context.Context, sa *StoredAuth) ([]string, error) {
	if sa == nil {
		return append([]string(nil), defaultModels...), nil
	}
	cacheKey := sa.AuthID
	if strings.TrimSpace(cacheKey) == "" {
		cacheKey = authIDFor(sa.GitHubToken)
	}

	cfg := currentConfig()
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
		return append([]string(nil), defaultModels...), errToken
	}

	reqURL := resolveCopilotBaseURL(sa, cfg) + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return append([]string(nil), defaultModels...), err
	}
	applyCopilotHeaders(req.Header, sa, cfg, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return append([]string(nil), defaultModels...), err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return append([]string(nil), defaultModels...), nil
	}

	var parsed upstreamModelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return append([]string(nil), defaultModels...), err
	}

	var models []string
	seen := make(map[string]struct{})
	for _, item := range parsed.Data {
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
		models = append([]string(nil), defaultModels...)
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

// authModelsForIndex 获取指定账号（或默认）的可用模型列表。
func authModelsForIndex(authID string) []string {
	authID = strings.TrimSpace(authID)
	cleanID := strings.TrimSuffix(authID, ".json")
	accounts, err := listAllAuthFiles()
	if err == nil {
		for _, acc := range accounts {
			if acc.AuthID == cleanID || acc.AuthID == authID || authFileNameFor(acc.GitHubToken) == cleanID+".json" {
				if len(acc.Models) > 0 {
					return acc.Models
				}
				models, errFetch := fetchUpstreamModels(context.Background(), acc)
				if errFetch == nil && len(models) > 0 {
					return models
				}
			}
		}
	}
	return append([]string(nil), defaultModels...)
}

// allConfiguredModels 返回所有已配置/已发现的模型 ModelInfo。
func allConfiguredModels() []pluginapi.ModelInfo {
	cfg := currentConfig()
	modelsSet := make(map[string]struct{})
	for _, m := range defaultModels {
		modelsSet[m] = struct{}{}
	}
	for _, m := range cfg.Models {
		if strings.TrimSpace(m) != "" {
			modelsSet[strings.TrimSpace(m)] = struct{}{}
		}
	}

	// 汇总所有账号已发现的模型
	if accounts, err := listAllAuthFiles(); err == nil {
		for _, acc := range accounts {
			for _, m := range acc.Models {
				if strings.TrimSpace(m) != "" {
					modelsSet[strings.TrimSpace(m)] = struct{}{}
				}
			}
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
