package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// autoModelID 是 Copilot 的自动选模入口。
//
// 免费 / 学生档在上游 UI 只开放 auto；接口层对 "model":"auto" 返回
// 400 model_not_supported，必须省略 model 字段让上游按账号档位自选
// （依据 GitHub 2026-06-24 免费档模型选择变更公告与社区探针实测）。
const autoModelID = "auto"

// defaultModels 是上游不可达且无任何落盘清单时的最小兜底。
//
// Copilot 的真实可用模型随订阅档位变化，运行时必须走 /models 动态发现；
// 这里只保留 auto——它不依赖具体档位授权，是上游不可达时仍能保证模型
// 出口非空的唯一安全项。具体模型 ID 严禁写死：写死会遮蔽上游新增模型，
// 并把已下线或当前档位无权调用的模型暴露给宿主。
var defaultModels = []string{autoModelID}

// isAutoModelID 判断模型标识是否为自动选模入口。
// [参数] model: 模型标识。
// [返回] true 表示自动选模入口（大小写与空白不敏感）。
// 最近修改时间：2026-10-17 15:30:00；改动原因：新增，区分 auto 与真实上游模型 ID。
func isAutoModelID(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), autoModelID)
}

// ensureAutoModel 在模型清单首位补入 auto 并去重。
//
// 上游 /models 不返回 auto（它不是具体模型），但它对免费档是唯一可用入口，
// 必须在所有模型出口（全局注册、按账号注册、面板、测试弹窗）稳定可见。
// [参数] models: 原始模型清单。
// [返回] 首位为 auto 的去重清单。
// 最近修改时间：2026-10-17 15:30:00；改动原因：新增，统一模型出口的 auto 合成口径。
func ensureAutoModel(models []string) []string {
	out := make([]string, 0, len(models)+1)
	out = append(out, autoModelID)
	for _, m := range models {
		trimmed := strings.TrimSpace(m)
		if trimmed == "" || isAutoModelID(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return out
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

	// 可用性探测：上游 /models 只返回「账号可见」而非「账号可调用」的模型全集，
	// 且响应中无任何静态字段能区分可调用性（实测 2026-10-16：25 条候选仅 10 条
	// 真正可走 /chat/completions，其余报 400 model_not_supported）。故必须逐模型
	// 探测，否则面板与宿主会暴露一堆点开即 400 的模型。
	//
	// 判定口径分两类，不可混为一谈：
	//  1. 探测全部给出确定结论 → 以探测结果为准（免费档会在此收敛到零个具体模型）；
	//  2. 存在不确定结果（传输错误 / 5xx / 限流）→ 保留过滤清单，不因上游抖动清空出口。
	// 早先实现只要探测结果为空就回退候选，导致免费档无权模型被原样暴露。
	usable, unknownCount := probeUsableModels(ctx, sa, cfg, models)
	if unknownCount == 0 {
		models = usable
	}

	// 出口清单始终包含 auto：探测确定零可用时这里只剩 auto，
	// 若返回空列表，调用方会误判为「动态发现失败」并回退到落盘旧清单，
	// 让免费档上无权调用的具体模型继续残留。
	//
	// 缓存与落盘只写具体模型（不含 auto）：auto 由出口层统一合成，
	// 写进去会让离线兜底把它当成上游真实模型，掩盖上游可达性变化。
	ttl := time.Duration(cfg.ModelCacheSecs) * time.Second
	if ttl <= 0 {
		ttl = 300 * time.Second
	}
	modelCache.Lock()
	modelCache.byKey[cacheKey] = models
	modelCache.expiresAt[cacheKey] = time.Now().Add(ttl)
	modelCache.Unlock()

	// 同步落盘账号模型清单：modelCache 过期后 cachedModelsForIndex 会回落到
	// 账号文件字段，若不刷新，面板卡片会退回历史兜底清单，与测试弹窗不一致。
	if path := strings.TrimSpace(sa.PhysicalPath); path != "" && !stringSliceEqual(sa.Models, models) {
		sa.Models = append([]string(nil), models...)
		_ = writeAuthFileDirect(path, sa)
	}

	return ensureAutoModel(models), nil
}

// stringSliceEqual 判断两个字符串切片是否逐元素相等。
// [参数] a, b: 待比较切片。
// [返回] 完全相等返回 true。
// 最近修改时间：2026-10-16 01:45:00；改动原因：新增，避免模型清单未变化时重复落盘。
func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// probeUsableModels 逐模型探测账号真实可调用性，只保留可走 /chat/completions 的模型。
//
// 探测手法（实测 2026-10-16 生产账号确认）：向 /chat/completions 发送仅含
// {"model": id} 的请求（**故意省略 messages**）——
//   - 账号可调用 → 400 "messages must be non-empty"（请求已过模型鉴权，进入参数校验）；
//   - 账号无权调用 → 400 "The requested model is not supported."（模型鉴权阶段即拒）。
//
// 该请求在模型鉴权阶段被拒时不计费，实测 quota_snapshots 前后完全一致，属零配额消耗。
//
// 并发上限 6：实测 25 个模型耗时 3.38s，兼顾速度与上游压力。
// 单个探测失败（网络错误 / 5xx）按「保留」处理，避免上游抖动误删可用模型；
// 仅当明确收到「模型不受支持」类错误时才剔除。
// 整批另设总预算：上游在探测中途变为不可达时，未完成探测的模型按「保留」处理，
// 避免 25 个模型 × 15s 客户端超时把面板请求拖到分钟级。
//
// 返回值把「明确无权」与「无法判定」分开：调用方据此决定是否可以信任探测结果，
// 避免把整批不确定误当成「账号零可用模型」。
// [参数] ctx: 上下文；sa: 目标账号；cfg: 当前配置；candidates: 待探测模型 ID。
// [返回] 可调用模型 ID 列表与无法判定的模型数量；探测完全不可用时列表为 nil。
// 最近修改时间：2026-10-17 15:30:00；改动原因：返回不确定计数，供调用方区分「无可用」与「探测失效」。
func probeUsableModels(ctx context.Context, sa *StoredAuth, cfg Config, candidates []string) ([]string, int) {
	if len(candidates) == 0 {
		return nil, 0
	}
	token, errToken := ensureCopilotToken(sa)
	if errToken != nil {
		return nil, len(candidates)
	}
	baseURL := resolveCopilotBaseURL(sa, cfg)

	batchCtx, cancelBatch := context.WithTimeout(ctx, probeBatchBudget)
	defer cancelBatch()

	const maxConcurrency = 6
	sem := make(chan struct{}, maxConcurrency)
	verdicts := make([]modelProbeVerdict, len(candidates))
	var wg sync.WaitGroup

	for i, id := range candidates {
		wg.Add(1)
		go func(idx int, modelID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			verdicts[idx] = probeModelCallable(batchCtx, baseURL, token, modelID, cfg)
		}(i, id)
	}
	wg.Wait()

	var usable []string
	unknownCount := 0
	for i, id := range candidates {
		switch verdicts[i] {
		case modelProbeUsable:
			usable = append(usable, id)
		case modelProbeUnknown:
			unknownCount++
		}
	}
	return usable, unknownCount
}

// modelProbeVerdict 是单个模型的探测结论。
//
// 三态而非布尔：上游抖动时「不确定」既不能当成可用（会暴露点开即 400 的模型），
// 也不能当成无权（会误删真实可用模型），必须交由调用方按整批情况决策。
type modelProbeVerdict int

const (
	// modelProbeUnusable 表示上游明确拒绝该模型（无权 / 不受支持）。
	modelProbeUnusable modelProbeVerdict = iota
	// modelProbeUsable 表示请求已通过模型鉴权。
	modelProbeUsable
	// modelProbeUnknown 表示探测未得到确定结论（传输错误 / 5xx / 限流）。
	modelProbeUnknown
)

// probeBatchBudget 是整批可用性探测的总预算上限。
const probeBatchBudget = 25 * time.Second

// probeModelCallable 判定单个模型在当前账号下是否可调用。
//
// 返回可用：收到「非模型不可用」类 400（典型为 messages must be non-empty），或任何 2xx；
// 返回不可用：明确命中 model_not_supported / not accessible 类错误；
// 返回不确定：传输错误、5xx、429 / 402 等限流或配额类响应，由调用方决定保留策略。
// [参数] ctx: 上下文；baseURL: 上游基础 URL；token: Copilot 内部令牌；modelID: 待探测模型；cfg: 当前配置。
// [返回] 三态探测结论。
// 最近修改时间：2026-10-17 15:30:00；改动原因：布尔改为三态，把限流/配额类响应标为不确定。
func probeModelCallable(ctx context.Context, baseURL, token, modelID string, cfg Config) modelProbeVerdict {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	payload, errMarshal := json.Marshal(map[string]any{"model": modelID})
	if errMarshal != nil {
		return modelProbeUnknown
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, baseURL+"/chat/completions", strings.NewReader(string(payload)))
	if err != nil {
		return modelProbeUnknown
	}
	applyCopilotHeaders(req.Header, nil, cfg, payload)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, errDo := modelHTTPClient.Do(req)
	if errDo != nil {
		return modelProbeUnknown
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return modelProbeUsable
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	text := strings.ToLower(string(body))
	if strings.Contains(text, "model_not_supported") ||
		strings.Contains(text, "not supported") ||
		strings.Contains(text, "not accessible") {
		return modelProbeUnusable
	}
	// 400 / 422 且未命中模型拒绝文案：请求已通过模型鉴权、落到参数校验
	// （典型为 messages must be non-empty），属可调用。
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity {
		return modelProbeUsable
	}
	return modelProbeUnknown
}

// cachedModelsForIndex 获取指定账号的模型列表，**只读缓存，不发起上游请求**。
//
// 供宿主高频调用路径使用（`model.static` 的全局模型注册、面板 `/accounts` 渲染）：
// 这些路径在单次调用中会遍历全部账号，若每账号都触发一次上游请求，上游不可达时
// 每账号阻塞至 15 秒超时，会拖垮宿主模型注册与面板加载。动态拉取只在
// `authModelsForIndex`（面板 /models、model.for_auth、测试探活）中发生。
//
// 所有返回分支都经 ensureAutoModel 补入 auto：上游 /models 不返回该入口，
// 但它是免费档唯一可用的选模方式，必须在缓存、落盘、配置、兜底四条路径上稳定可见。
// [参数] authID: 账号标识（可带 .json 后缀）。
// [返回] 模型 ID 列表（首位为 auto）；无缓存时按落盘清单 → 配置 → 静态兜底降级。
// 最近修改时间：2026-10-17 15:30:00；改动原因：统一出口补入 auto，保证免费档可用入口不被遮蔽。
func cachedModelsForIndex(authID string) []string {
	authID = strings.TrimSpace(authID)
	cleanID := strings.TrimSuffix(authID, ".json")

	accounts, err := listAllAuthFiles()
	if err != nil {
		return ensureAutoModel(defaultModels)
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
				cached := ensureAutoModel(list)
				modelCache.RUnlock()
				return cached
			}
		}
		modelCache.RUnlock()

		if len(acc.Models) > 0 {
			return ensureAutoModel(acc.Models)
		}
		break
	}
	if cfg := currentConfig(); len(cfg.Models) > 0 {
		return ensureAutoModel(cfg.Models)
	}
	return ensureAutoModel(defaultModels)
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
// [返回] 模型 ID 列表（首位为 auto）；账号不存在时返回静态兜底清单。
// 最近修改时间：2026-10-17 15:30:00；改动原因：所有分支补入 auto，保证免费档唯一入口可见。
func authModelsForIndex(authID string) []string {
	authID = strings.TrimSpace(authID)
	cleanID := strings.TrimSuffix(authID, ".json")
	accounts, err := listAllAuthFiles()
	if err == nil {
		for _, acc := range accounts {
			if acc.AuthID != cleanID && acc.AuthID != authID && authFileNameFor(acc.GitHubToken) != cleanID+".json" {
				continue
			}
			// fetchUpstreamModels 成功时清单已含 auto，无需再次合成。
			if models, errFetch := fetchUpstreamModels(context.Background(), acc); errFetch == nil && len(models) > 0 {
				return models
			}
			if len(acc.Models) > 0 {
				return ensureAutoModel(acc.Models)
			}
			if cfg := currentConfig(); len(cfg.Models) > 0 {
				return ensureAutoModel(cfg.Models)
			}
			break
		}
	}
	return ensureAutoModel(defaultModels)
}

// allConfiguredModels 返回本 provider 对外暴露的全部模型 ModelInfo。
//
// 来源优先级：各账号缓存清单 > 配置 models > 静态兜底清单。
// 只读缓存不触发上游请求：本函数被宿主 model.static / model.register 高频调用，
// 逐账号发请求会让模型注册被上游超时拖垮（详见 cachedModelsForIndex）。
// 结果统一经 ensureAutoModel 补入 auto：上游 /models 不返回该入口，而免费档
// 只能走自动选模，缺了它等于把免费账号整体从路由中排除。
// [参数] 无。
// [返回] 模型元数据列表（含 auto）。
// 最近修改时间：2026-10-17 15:30:00；改动原因：全局模型出口补入 auto。
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
	for _, id := range ensureAutoModel(mapKeys(modelsSet)) {
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

// mapKeys 取出集合中的键并按字典序排序。
//
// 排序只为让模型注册输出稳定可复现：宿主按列表顺序登记模型，
// 顺序随 map 迭代随机变化会让注册结果与日志每次不同。
// [参数] set: 字符串集合。
// [返回] 排序后的键切片。
// 最近修改时间：2026-10-17 15:30:00；改动原因：新增，替代直接遍历 map 的随机顺序。
func mapKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
