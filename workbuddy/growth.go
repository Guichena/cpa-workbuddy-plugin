// growth.go owns the WorkBuddy CN growth-center（成长中心）API client: the
// endpoint path constants, the shared GET/POST caller, and the read-only
// status fetchers consumed by the growth orchestrator.
//
// 契约来源：EXT-1（88lin/workbuddy-auto-signin 的 signin.py，2026-10 活动版本）。
// 成长中心路由挂在 {billingBase}/v2/activity/growth 下；每日签到状态仍走
// {billingBase}/v2/billing/meter/checkin-activity-status，由 billing.go 的
// fetchCheckinStatus 承担，本文件不再包一层同义函数。
//
// 写语义：GET 遇上游 5xx 重试；POST 一律不重试——这些端点会真实发放积分、
// 消耗补登卡与抽奖次数，盲重试可能重复扣减。
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// growthPathPrefix 是成长中心全部端点的公共前缀，拼接在账号所属 billing base 之后。
const growthPathPrefix = "/v2/activity/growth"

// 成长中心端点相对路径。GET 为只读查询，POST 为会改变账号状态的写操作。
const (
	growthPathStreak         = "/streak"
	growthPathHeatmap        = "/heatmap"
	growthPathEnergy         = "/energy"
	growthPathRedeemSummary  = "/redeem/summary"
	growthPathRedeem         = "/redeem"
	growthPathLotteryChances = "/lottery/chances"
	growthPathLotteryDraw    = "/lottery/draw"
	growthPathMakeupCardsUse = "/makeup-cards/use"
	growthPathTasks          = "/tasks"
	growthPathTasksAccept    = "/tasks/accept"
	growthPathTravelStatus   = "/buddy/travel/status"
	growthPathTravelConfig   = "/buddy/travel/config"
	growthPathTravelClaim    = "/buddy/travel/claim"
	growthPathTravelDepart   = "/buddy/travel/depart"
	growthPathBuddyQuota     = "/buddy/quota"
	growthPathBuddyOpen      = "/buddy/open"
)

// growthRedeemTiers 是连登阶梯兑换的合法档位（对齐 EXT-1 的 tier 枚举）。
var growthRedeemTiers = map[string]bool{"7d": true, "14d": true, "28d": true}

// growthBaseFor 返回账号所属区域的成长中心 API base（CN 与 Global 同族域）。
// [参数] sa：账号凭据；nil 时回落 CN base。
// [返回] {billingBase}/v2/activity/growth 形式的完整前缀。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func growthBaseFor(sa *storedAuth) string {
	return billingBaseFor(sa) + growthPathPrefix
}

// growthCall 发起一次成长中心 API 调用，按方法区分重试语义。
// [参数] sa：账号凭据；method：HTTP 方法；path：growthPathPrefix 下的相对路径；body：POST 请求体（GET 传 nil）。
// [返回] envelope 解包后的 data 原始 JSON；失败时返回分类错误。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func growthCall(sa *storedAuth, method, path string, body any) (json.RawMessage, error) {
	// 1. 首次调用
	data, err := growthCallOnce(sa, method, path, body)

	// 2. 仅 GET 重试：写操作幂等性未经证实（计划 A3），一律单发
	if method != http.MethodGet {
		if err != nil {
			log.Printf("[workbuddy] growth %s %s failed for %s: %v", method, path, sa.Account.UID, err)
		}
		return data, err
	}
	for _, d := range billingRetryDelays {
		if err == nil || !isTransientBillingErr(err) {
			break
		}
		time.Sleep(d)
		data, err = growthCallOnce(sa, method, path, body)
	}
	if err != nil {
		log.Printf("[workbuddy] growth %s %s failed for %s: %v", method, path, sa.Account.UID, err)
	}
	return data, err
}

// growthCallOnce 执行单次成长中心请求并解包 {code,msg,data} envelope。
// 与 billingCallOnce 同族，但不复用其函数体：billing 链路只发 POST 且始终带重试，
// 成长中心需要 GET 无 body 与写操作不重试两种语义。
// [参数] sa：账号凭据；method：HTTP 方法；path：相对路径；body：请求体（nil 时 GET 不带体、POST 发空对象）。
// [返回] data 原始 JSON；5xx / 解析失败 / 业务 code != 0 时返回带脱敏片段的错误。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func growthCallOnce(sa *storedAuth, method, path string, body any) (json.RawMessage, error) {
	// 1. 组装请求体：显式 body 优先；POST 无 body 时补空对象，GET 不带体
	var reqBody io.Reader
	switch {
	case body != nil:
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(raw)
	case method == http.MethodPost:
		reqBody = bytes.NewReader([]byte("{}"))
	}

	// 2. 复用 billing 鉴权头发起请求；走 host.http.do 让宿主请求日志可观测，
	//    Windows 与单测环境由 hostHTTPDo 内部回落直连
	req, err := http.NewRequest(method, growthBaseFor(sa)+path, reqBody)
	if err != nil {
		return nil, err
	}
	billingHeaders(req, sa)
	resp, err := hostHTTPDo(req)
	if err != nil {
		return nil, err
	}
	raw := resp.Body
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("http %d from %s: %s", resp.StatusCode, path, growthSnippet(raw))
	}

	// 3. 解 apiEnvelope：code != 0 视为业务失败，错误片段脱敏截断
	var env apiEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse failed: %w (body: %s)", err, growthSnippet(raw))
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("code=%d msg=%s", env.Code, truncateRedacted(env.Msg, 120))
	}
	return env.Data, nil
}

// growthSnippet 返回脱敏并截断到 120 字节的响应片段，供错误信息定位上游问题。
// [参数] raw：上游原始响应体。
// [返回] 脱敏截断后的字符串。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func growthSnippet(raw []byte) string {
	// 1. 先去敏再截断，保证错误信息既不泄漏凭据也不会过长
	s := strings.TrimSpace(redactSecrets(string(raw)))
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// fetchGrowthStreak 读取连登域状态：连登天数、补登卡余额、已补登日期与阶梯兑换状态。
// [参数] sa：账号凭据。
// [返回] 响应顶层字段 map；失败时返回错误。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func fetchGrowthStreak(sa *storedAuth) (map[string]any, error) {
	return growthGetMap(sa, growthPathStreak)
}

// fetchGrowthHeatmap 读取签到热力图：今日日期与逐日活跃分值。
// [参数] sa：账号凭据。
// [返回] 响应顶层字段 map；失败时返回错误。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func fetchGrowthHeatmap(sa *storedAuth) (map[string]any, error) {
	return growthGetMap(sa, growthPathHeatmap)
}

// growthGetMap 执行一次只读 GET 并把 data 解成顶层 map，供各 fetchGrowth* 复用。
// [参数] sa：账号凭据；path：growth 相对路径。
// [返回] 顶层字段 map；失败时返回错误。
// 最近修改时间：2026-10-15 20:50:00 新增成长中心客户端骨架（TASK-1.1）
func growthGetMap(sa *storedAuth, path string) (map[string]any, error) {
	// 1. 发起只读 GET 并解 data 为顶层 map
	data, err := growthCall(sa, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// -----------------------------------------------------------------------------
// 连登域只读端点（TASK-1.2）
// -----------------------------------------------------------------------------

// fetchGrowthEnergy 读取 Buddy 能量余额。
// [参数] sa：账号凭据。
// [返回] 响应顶层字段 map；失败时返回错误。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func fetchGrowthEnergy(sa *storedAuth) (map[string]any, error) {
	return growthGetMap(sa, growthPathEnergy)
}

// fetchGrowthRedeemSummary 读取连登阶梯兑换状态（入门 / 进阶 / 巅峰三档）。
// [参数] sa：账号凭据。
// [返回] 响应顶层字段 map；失败时返回错误。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func fetchGrowthRedeemSummary(sa *storedAuth) (map[string]any, error) {
	return growthGetMap(sa, growthPathRedeemSummary)
}

// fetchGrowthLotteryChances 读取盲盒抽奖剩余次数。
// [参数] sa：账号凭据。
// [返回] 响应顶层字段 map；失败时返回错误。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func fetchGrowthLotteryChances(sa *storedAuth) (map[string]any, error) {
	return growthGetMap(sa, growthPathLotteryChances)
}

// -----------------------------------------------------------------------------
// 连登域写端点（TASK-1.2）—— 一律单发不重试，client_token 去重
// -----------------------------------------------------------------------------

// useMakeupCard 对指定日期使用一张补登卡，补齐断登日。
// [参数] sa：账号凭据；targetDate：目标日期（YYYY-MM-DD）。
// [返回] 响应顶层字段 map（含 makeup_cards.balance）；失败时返回错误。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func useMakeupCard(sa *storedAuth, targetDate string) (map[string]any, error) {
	// 1. 空日期直接拒绝，避免对无意义目标消耗补登卡
	if strings.TrimSpace(targetDate) == "" {
		return nil, fmt.Errorf("empty target_date")
	}
	return growthPostMap(sa, growthPathMakeupCardsUse, map[string]any{"target_date": targetDate})
}

// redeemTier 领取指定档位的连登阶梯奖励。
// [参数] sa：账号凭据；tier：档位（7d / 14d / 28d）。
// [返回] 响应顶层字段 map（含 credit_granted 等）；档位非法时直接返回错误且不发请求。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func redeemTier(sa *storedAuth, tier string) (map[string]any, error) {
	// 1. 档位白名单校验：非法档位直接拒绝，不发写请求
	if !growthRedeemTiers[tier] {
		return nil, fmt.Errorf("invalid redeem tier: %q", tier)
	}
	// 2. 携带 client_token 兑换，供上游去重
	return growthPostMap(sa, growthPathRedeem, map[string]any{
		"tier":         tier,
		"client_token": growthClientToken(),
	})
}

// drawLottery 消耗一次抽奖机会抽取盲盒。
// [参数] sa：账号凭据。
// [返回] 响应顶层字段 map（含 prize_name 等）；失败时返回错误。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func drawLottery(sa *storedAuth) (map[string]any, error) {
	return growthPostMap(sa, growthPathLotteryDraw, map[string]any{"client_token": growthClientToken()})
}

// growthPostMap 执行一次写操作 POST 并把 data 解成顶层 map。
// [参数] sa：账号凭据；path：growth 相对路径；body：请求体。
// [返回] 顶层字段 map；失败时返回错误。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func growthPostMap(sa *storedAuth, path string, body any) (map[string]any, error) {
	// 1. 发起单发写操作 POST（不重试）并解 data 为顶层 map
	data, err := growthCall(sa, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// growthClientToken 生成 "u-<uuid4>" 形式的幂等 token，供上游做重复请求去重。
// 用 crypto/rand 直接构造 UUID v4，避免为单个 token 引入第三方 uuid 依赖。
// [参数] 无。
// [返回] "u-" 前缀的 UUID v4 字符串；随机源失败时退化为时间戳纳秒串。
// 最近修改时间：2026-10-15 21:10:00 新增连登域端点（TASK-1.2）
func growthClientToken() string {
	// 1. 取 16 字节随机源；失败时退化时间戳串，保证仍能返回可去重 token
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("u-%d", time.Now().UnixNano())
	}
	// 2. 按 RFC 4122 置版本位与变体位，渲染成 uuid v4 形态
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("u-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// -----------------------------------------------------------------------------
// 成长任务域（TASK-1.3）
// -----------------------------------------------------------------------------

// growthAcceptBatchSize 是单次接单请求携带的最大任务码数量（对齐 EXT-1 的 20 上限）。
const growthAcceptBatchSize = 20

// growthTask 是成长任务列表中的一条任务。
type growthTask struct {
	TaskCode     string `json:"task_code"`
	Title        string `json:"title"`
	Locked       bool   `json:"locked"`
	AcceptStatus string `json:"accept_status"`
	RewardCredit int64  `json:"reward_credit"`
	RewardEnergy int64  `json:"reward_energy"`
}

// fetchGrowthTasks 读取成长任务列表。
// [参数] sa：账号凭据。
// [返回] 任务列表；data 无 tasks 字段时返回空切片；失败时返回错误。
// 最近修改时间：2026-10-15 21:20:00 新增长任务域端点（TASK-1.3）
func fetchGrowthTasks(sa *storedAuth) ([]growthTask, error) {
	// 1. 拉取任务列表并按上游 data.tasks 结构解包
	data, err := growthCall(sa, http.MethodGet, growthPathTasks, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tasks []growthTask `json:"tasks"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Tasks, nil
}

// acceptGrowthTasks 批量接单，按 growthAcceptBatchSize 自动分批。
// [参数] sa：账号凭据；codes：任务码列表（空列表直接返回 nil 不发请求）。
// [返回] 逐批结果合并后的响应 map 列表；任一批失败即中止并返回错误。
// 最近修改时间：2026-10-15 21:20:00 新增长任务域端点（TASK-1.3）
func acceptGrowthTasks(sa *storedAuth, codes []string) ([]map[string]any, error) {
	// 1. 空列表直接返回，避免发无意义的写请求
	if len(codes) == 0 {
		return nil, nil
	}
	// 2. 按 growthAcceptBatchSize 切批，逐批接单；任一批失败即中止并回传已得结果
	var out []map[string]any
	for start := 0; start < len(codes); start += growthAcceptBatchSize {
		end := start + growthAcceptBatchSize
		if end > len(codes) {
			end = len(codes)
		}
		m, err := growthPostMap(sa, growthPathTasksAccept, map[string]any{"task_codes": codes[start:end]})
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

// claimGrowthTask 领取单个已完成任务的奖励。
// [参数] sa：账号凭据；code：任务码。
// [返回] 响应顶层字段 map；code 为空时直接返回错误且不发请求。
// 最近修改时间：2026-10-15 21:20:00 新增长任务域端点（TASK-1.3）
func claimGrowthTask(sa *storedAuth, code string) (map[string]any, error) {
	// 1. 空任务码直接拒绝，避免拼出 /tasks//claim 这种无效路径
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("empty task_code")
	}
	// 2. 领奖：任务码需转义后拼进路径
	path := growthPathTasks + "/" + url.PathEscape(code) + "/claim"
	m, err := growthPostMap(sa, path, map[string]any{})
	if err != nil {
		return nil, err
	}
	// 3. 归一化成功语义：上游对重复领奖返回 already_claimed=true，同属业务成功
	if jsonBool(m, "already_claimed") {
		m["success"] = true
		m["already_claimed"] = true
		return m, nil
	}
	m["success"] = true
	return m, nil
}

// -----------------------------------------------------------------------------
// Buddy 旅行域（TASK-1.4）
// -----------------------------------------------------------------------------

// growthTravelStatus 是 Buddy 旅行的当前状态。
// State 三态：idle（待派出）/ traveling（旅行中）/ arrived（已到达可收礼）。
type growthTravelStatus struct {
	State             string `json:"state"`
	RecordID          string `json:"record_id"`
	ArriveAt          string `json:"arrive_at"`
	ServerNow         string `json:"server_now"`
	DailyLimitReached bool   `json:"daily_limit_reached"`
}

// growthTravelLocation 是旅行配置中的一个目的地。
type growthTravelLocation struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DurationHours int64  `json:"duration_hours"`
}

// fetchTravelStatus 读取 Buddy 旅行状态。
// [参数] sa：账号凭据。
// [返回] 旅行状态；失败时返回错误。
// 最近修改时间：2026-10-15 21:30:00 新增 Buddy 旅行域端点（TASK-1.4）
func fetchTravelStatus(sa *storedAuth) (*growthTravelStatus, error) {
	// 1. 拉取旅行状态并按 State 三态（idle / traveling / arrived）解包
	data, err := growthCall(sa, http.MethodGet, growthPathTravelStatus, nil)
	if err != nil {
		return nil, err
	}
	var st growthTravelStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// fetchTravelConfig 读取 Buddy 旅行可选目的地配置。
// [参数] sa：账号凭据。
// [返回] 目的地列表；失败时返回错误。
// 最近修改时间：2026-10-15 21:30:00 新增 Buddy 旅行域端点（TASK-1.4）
func fetchTravelConfig(sa *storedAuth) ([]growthTravelLocation, error) {
	// 1. 拉取可选目的地列表，供派出时选取合法 location_id
	data, err := growthCall(sa, http.MethodGet, growthPathTravelConfig, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Locations []growthTravelLocation `json:"locations"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Locations, nil
}

// claimTravelReward 领取已到达旅行的奖励。
// [参数] sa：账号凭据；recordID：旅行记录 ID。
// [返回] 响应顶层字段 map（含 reward_credit）；recordID 为空时直接返回错误且不发请求。
// 最近修改时间：2026-10-15 21:30:00 新增 Buddy 旅行域端点（TASK-1.4）
func claimTravelReward(sa *storedAuth, recordID string) (map[string]any, error) {
	// 1. 空记录 ID 直接拒绝，避免对无效记录发收礼请求
	recordID = strings.TrimSpace(recordID)
	if recordID == "" {
		return nil, fmt.Errorf("empty record_id")
	}
	return growthPostMap(sa, growthPathTravelClaim, map[string]any{"record_id": recordID})
}

// departBuddy 派出 Buddy 前往指定目的地。
// [参数] sa：账号凭据；locationID：目的地 ID（来自 fetchTravelConfig）。
// [返回] 响应顶层字段 map（含 location.name / duration_hours）；locationID 为空时直接返回错误且不发请求。
// 最近修改时间：2026-10-15 21:30:00 新增 Buddy 旅行域端点（TASK-1.4）
func departBuddy(sa *storedAuth, locationID string) (map[string]any, error) {
	// 1. 空目的地 ID 直接拒绝，避免派出到无效目的地
	locationID = strings.TrimSpace(locationID)
	if locationID == "" {
		return nil, fmt.Errorf("empty location_id")
	}
	return growthPostMap(sa, growthPathTravelDepart, map[string]any{"location_id": locationID})
}

// fetchBuddyQuota 读取 Buddy 盲盒可开次数与最大开盒数。
// [参数] sa：账号凭据。
// [返回] 响应顶层字段 map（含 affordable / max_open_count）；失败时返回错误。
// 最近修改时间：2026-10-15 21:30:00 新增 Buddy 旅行域端点（TASK-1.4）
func fetchBuddyQuota(sa *storedAuth) (map[string]any, error) {
	return growthGetMap(sa, growthPathBuddyQuota)
}

// openBuddyBox 开启指定数量的 Buddy 能量盲盒。
// [参数] sa：账号凭据；count：开盒数量（<=0 时按 1 处理）。
// [返回] 响应顶层字段 map（含 buddies 等）；失败时返回错误。
// 最近修改时间：2026-10-15 21:30:00 新增 Buddy 旅行域端点（TASK-1.4）
func openBuddyBox(sa *storedAuth, count int) (map[string]any, error) {
	// 1. 开盒数兜底为 1，避免 0 或负数被上游判为非法
	if count <= 0 {
		count = 1
	}
	// 2. 携带 client_token 开盒，供上游去重
	return growthPostMap(sa, growthPathBuddyOpen, map[string]any{
		"count":        count,
		"client_token": growthClientToken(),
	})
}

// -----------------------------------------------------------------------------
// 成长中心编排（TASK-2.1）
// -----------------------------------------------------------------------------

// growthBudget 是一次 runGrowthForAccount 的总时长预算，超时后主动放弃后续段。
// 是 var（非 const）以便测试注入极短预算验证超时保护。
var growthBudget = 180 * time.Second

// growthEnabled 返回 growth_auto 配置开关的当前值。
// [参数] 无。
// [返回] true 表示每 4 小时 tick 内自动执行成长中心领奖。
// 最近修改时间：2026-10-15 21:50:00 新增 growth_auto 配置接入（TASK-2.2）
func growthEnabled() bool {
	growthAutoMu.RLock()
	defer growthAutoMu.RUnlock()
	return growthAuto
}

// growthInterAccountDelayFn 是账号间延迟的包级测试接缝：生产指向随机延迟实现，
// 单测替换为零延迟，避免 tick 用例为每次账号处理等待 1~3s。
var growthInterAccountDelayFn = growthInterAccountDelay

// growthInterAccountDelay 返回账号间随机延迟（1~3s），用于摊平上游请求避免风控。
// [参数] 无。
// [返回] 1s 到 3s 之间的随机时长；随机源失败时返回 2s。
// 最近修改时间：2026-10-15 21:50:00 新增 growth_auto 配置接入（TASK-2.2）
func growthInterAccountDelay() time.Duration {
	// 1. 取 4 字节随机源；失败时回落 2s 固定延迟
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 2 * time.Second
	}
	// 2. 映射到 [1000ms, 3000ms] 区间，摊平账号间请求
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return time.Duration(1000+n%2001) * time.Millisecond
}

// growthLocks 按 auth_index 串行化同一账号的成长中心执行，避免面板点击与定时 tick
// 并发触发导致写端点重复调用。与 checkinLocks 分开，二者互不阻塞。
var growthLocks sync.Map // auth_index -> *sync.Mutex

// growthResult 汇总一次成长中心执行的收益与分段结果。
type growthResult struct {
	CreditsGained int64    `json:"credits_gained"`
	EnergyGained  int64    `json:"energy_gained"`
	TasksAccepted int      `json:"tasks_accepted"`
	TasksClaimed  int      `json:"tasks_claimed"`
	Segments      []string `json:"segments"`
	Errors        []string `json:"errors"`
}

// growthLockFor 返回账号级互斥锁，保证同一账号成长中心执行串行。
// [参数] authIndex：宿主 RPC 索引。
// [返回] 该账号对应的互斥锁。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthLockFor(authIndex string) *sync.Mutex {
	v, _ := growthLocks.LoadOrStore(authIndex, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// pruneGrowthLocks 清理已不存在账号的锁条目，避免 sync.Map 无界增长。
// 与 pruneCheckinLocks 同形：锁键是 auth_index，故 live 集合需同时收 ID 与 auth_index。
// [参数] 无。
// [返回] 无。
// 最近修改时间：2026-10-15 22:30:00 新增成长中心管理路由与面板入口（TASK-3.1）
func pruneGrowthLocks() {
	// 1. 读取在册账号，同时收集 ID 与 auth_index（锁键是 auth_index）
	files, err := hostAuthList()
	if err != nil {
		return
	}
	live := make(map[string]struct{}, len(files))
	for _, f := range files {
		live[f.ID] = struct{}{}
		live[f.AuthIndex] = struct{}{}
	}
	// 2. 删除已不在册账号的锁条目，避免 sync.Map 无界增长
	growthLocks.Range(func(key, _ any) bool {
		idx, _ := key.(string)
		if _, ok := live[idx]; !ok {
			growthLocks.Delete(key)
		}
		return true
	})
}

// runGrowthForAccount 按外部项目顺序分段执行成长中心全部玩法。
// 每段独立执行：任一段失败只记入 Errors，不影响其余段；总时长超过 growthBudget
// 时放弃剩余段并返回已得结果。写操作一律不重试（由 growthCall 保证）。
// [参数] sa：账号凭据；authIndex：账号索引（用于账号级互斥）。
// [返回] 本轮收益与分段结果。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func runGrowthForAccount(sa *storedAuth, authIndex string) *growthResult {
	mu := growthLockFor(authIndex)
	mu.Lock()
	defer mu.Unlock()

	res := &growthResult{}
	deadline := time.Now().Add(growthBudget)

	// 1. 签到：复用既有 billing 链路，未签则补签
	if !time.Now().After(deadline) {
		if ci, err := fetchCheckinStatus(sa); err != nil {
			res.Errors = append(res.Errors, "checkin_status: "+err.Error())
		} else if ci.Active && !ci.TodayCheckedIn {
			if m, callErr := performCheckinCall(sa); callErr != nil {
				res.Errors = append(res.Errors, "checkin: "+callErr.Error())
			} else {
				res.CreditsGained += jsonI64(m, "credit")
				res.Segments = append(res.Segments, "checkin")
			}
		}
	}

	// 2. 旅行：到达则收礼，随后按配置再派出
	if !time.Now().After(deadline) {
		growthRunTravel(sa, res)
	}

	// 3. 任务：未接单的接单，已完成的领奖
	if !time.Now().After(deadline) {
		growthRunTasks(sa, res)
	}

	// 4. 补登：当月断登且仍有补登卡时最多补 1 张
	if !time.Now().After(deadline) {
		growthRunMakeup(sa, res, time.Now())
	}

	// 5. 兑换：达成档位则兑换最高档
	if !time.Now().After(deadline) {
		growthRunRedeem(sa, res)
	}

	// 6. 抽奖：有次数则最多抽 1 次
	if !time.Now().After(deadline) {
		growthRunLottery(sa, res)
	}

	// 7. 盲盒：可开则开
	if !time.Now().After(deadline) {
		growthRunBuddyBox(sa, res)
	}

	// 8. 汇总：能量余额与连登天数只读读取
	if !time.Now().After(deadline) {
		if m, err := fetchGrowthEnergy(sa); err == nil {
			res.EnergyGained += jsonI64(m, "balance")
		}
		if m, err := fetchGrowthStreak(sa); err == nil {
			res.Segments = append(res.Segments, fmt.Sprintf("streak=%d", jsonI64(mapOrEmpty(m, "streak"), "days")))
		}
	}
	return res
}

// growthRunTravel 执行 Buddy 旅行分段：到达收礼 → 读取配置 → 再派出。
// [参数] sa：账号凭据；res：结果累加器。
// [返回] 无（结果写入 res）。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthRunTravel(sa *storedAuth, res *growthResult) {
	// 1. 读旅行状态；失败即记录并退出本段
	st, err := fetchTravelStatus(sa)
	if err != nil {
		res.Errors = append(res.Errors, "travel_status: "+err.Error())
		return
	}
	// 2. 仅在 arrived 状态收礼；traveling 直接返回，idle 继续走派出
	if st.State == "arrived" {
		if m, claimErr := claimTravelReward(sa, st.RecordID); claimErr != nil {
			res.Errors = append(res.Errors, "travel_claim: "+claimErr.Error())
		} else {
			res.CreditsGained += jsonI64(m, "reward_credit")
			res.Segments = append(res.Segments, "travel_claim")
		}
	} else if st.State != "idle" && st.State != "" {
		return // traveling：无需派出
	}
	// 3. 已达当日上限则不再派出（避免无谓写请求）
	if st.DailyLimitReached {
		return
	}
	// 4. 读配置取合法目的地后派出；无合法目的地则记录并退出
	locs, err := fetchTravelConfig(sa)
	if err != nil || len(locs) == 0 {
		if err != nil {
			res.Errors = append(res.Errors, "travel_config: "+err.Error())
		}
		return
	}
	if _, departErr := departBuddy(sa, locs[0].ID); departErr != nil {
		res.Errors = append(res.Errors, "travel_depart: "+departErr.Error())
		return
	}
	res.Segments = append(res.Segments, "travel_depart")
}

// growthRunTasks 执行成长任务分段：未接单批量接单 → 重新拉取 → 已完成逐个领奖。
// [参数] sa：账号凭据；res：结果累加器。
// [返回] 无（结果写入 res）。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthRunTasks(sa *storedAuth, res *growthResult) {
	// 1. 拉任务列表；失败即记录并退出本段
	tasks, err := fetchGrowthTasks(sa)
	if err != nil {
		res.Errors = append(res.Errors, "tasks: "+err.Error())
		return
	}
	// 2. 收集「未锁定且未接单」的任务码，批量接单
	var pending []string
	for _, t := range tasks {
		if !t.Locked && t.AcceptStatus == "not_accepted" && strings.TrimSpace(t.TaskCode) != "" {
			pending = append(pending, t.TaskCode)
		}
	}
	if len(pending) > 0 {
		if _, acceptErr := acceptGrowthTasks(sa, pending); acceptErr != nil {
			res.Errors = append(res.Errors, "tasks_accept: "+acceptErr.Error())
		} else {
			res.TasksAccepted += len(pending)
			res.Segments = append(res.Segments, "tasks_accept")
		}
	}
	// 3. 接单可能使任务立即完成，重新拉取后逐个领取已完成项
	refreshed, err := fetchGrowthTasks(sa)
	if err != nil {
		res.Errors = append(res.Errors, "tasks_refresh: "+err.Error())
		return
	}
	for _, t := range refreshed {
		if t.AcceptStatus != "completed" || strings.TrimSpace(t.TaskCode) == "" {
			continue
		}
		m, claimErr := claimGrowthTask(sa, t.TaskCode)
		if claimErr != nil {
			res.Errors = append(res.Errors, "task_claim "+t.TaskCode+": "+claimErr.Error())
			continue
		}
		res.CreditsGained += jsonI64(m, "credit")
		res.EnergyGained += jsonI64(m, "energy")
		res.TasksClaimed++
	}
	if res.TasksClaimed > 0 {
		res.Segments = append(res.Segments, "tasks_claim")
	}
}

// growthRunMakeup 执行补登分段：定位当月首个断登日并使用补登卡（每轮最多 1 张）。
// [参数] sa：账号凭据；res：结果累加器；now：当前时间（便于测试注入）。
// [返回] 无（结果写入 res）。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthRunMakeup(sa *storedAuth, res *growthResult, now time.Time) {
	// 1. 读连登与热力图，二者缺一不可（补登判据同时依赖）
	streak, err := fetchGrowthStreak(sa)
	if err != nil {
		res.Errors = append(res.Errors, "streak: "+err.Error())
		return
	}
	heatmap, err := fetchGrowthHeatmap(sa)
	if err != nil {
		res.Errors = append(res.Errors, "heatmap: "+err.Error())
		return
	}
	// 2. 定位当月首个断登日；无缺口则本段无操作（每轮最多补 1 张）
	target := growthMakeupTarget(streak, heatmap, now)
	if target == "" {
		return
	}
	if _, useErr := useMakeupCard(sa, target); useErr != nil {
		res.Errors = append(res.Errors, "makeup_use: "+useErr.Error())
		return
	}
	res.Segments = append(res.Segments, "makeup:"+target)
}

// growthMakeupTarget 计算当月首个可补登日期。
// 判据：补登卡余额 > 0；从 max(当月 1 日, 上线日) 起逐日检查到昨日，返回第一个
// 既不在热力图有效日内、也不在已补登列表内的日期；无缺口返回空串。
// [参数] streak：/streak 响应；heatmap：/heatmap 响应；now：当前时间。
// [返回] YYYY-MM-DD 目标日期；无可补日期返回空串。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthMakeupTarget(streak, heatmap map[string]any, now time.Time) string {
	// 1. 补登卡余额为 0 则无可补，直接返回空串
	if jsonI64(mapOrEmpty(streak, "makeup_cards"), "balance") <= 0 {
		return ""
	}
	// 2. 汇总已覆盖日期：热力图中 score>0 的活跃日 + streak 已补登日期
	covered := map[string]bool{}
	if cells, ok := heatmap["cells"].([]any); ok {
		for _, c := range cells {
			cm, _ := c.(map[string]any)
			if jsonI64(cm, "score") > 0 {
				covered[jsonStr(cm, "date")] = true
			}
		}
	}
	if dates, ok := streak["makeup_dates"].([]any); ok {
		for _, d := range dates {
			if s, ok := d.(string); ok {
				covered[s] = true
			}
		}
	}
	// 3. 起点取 max(当月 1 日, 上线日)，终点取昨日，返回首个未覆盖日期
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	if launch, err := time.ParseInLocation("2006-01-02", jsonStr(streak, "launch_date"), now.Location()); err == nil && launch.After(start) {
		start = launch
	}
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	for d := start; !d.After(yesterday); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if !covered[key] {
			return key
		}
	}
	return ""
}

// growthRedeemTierOrder 是兑换档位的从高到低优先级顺序。
var growthRedeemTierOrder = []struct {
	Tier   string
	Status string
}{
	{"28d", "legendary_status"},
	{"14d", "advanced_status"},
	{"7d", "starter_status"},
}

// growthRunRedeem 执行兑换分段：选取可兑换的最高档位并领取。
// [参数] sa：账号凭据；res：结果累加器。
// [返回] 无（结果写入 res）。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthRunRedeem(sa *storedAuth, res *growthResult) {
	// 1. 读兑换状态；失败即记录并退出本段
	summary, err := fetchGrowthRedeemSummary(sa)
	if err != nil {
		res.Errors = append(res.Errors, "redeem_summary: "+err.Error())
		return
	}
	// 2. 按从高到低档位顺序，取首个可兑换档位领取（一轮只兑一档）
	for _, t := range growthRedeemTierOrder {
		if !growthStatusRedeemable(summary[t.Status]) {
			continue
		}
		m, redeemErr := redeemTier(sa, t.Tier)
		if redeemErr != nil {
			res.Errors = append(res.Errors, "redeem "+t.Tier+": "+redeemErr.Error())
			return
		}
		res.CreditsGained += jsonI64(m, "credit_granted")
		res.EnergyGained += jsonI64(m, "energy_granted")
		res.Segments = append(res.Segments, "redeem:"+t.Tier)
		return
	}
}

// growthStatusRedeemable 判断兑换状态字段是否表示"可领取"。
// 兼容布尔 true 与常见就绪字符串；未知形态一律视为不可兑换（保守，不误发写请求）。
// [参数] v：/redeem/summary 中对应档位的状态值。
// [返回] true 表示可兑换。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthStatusRedeemable(v any) bool {
	// 1. 布尔 true 视为可兑换；字符串按就绪词表匹配；未知形态保守判否
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "ready", "redeemable", "available", "unredeemed", "achieved", "done":
			return true
		}
	}
	return false
}

// growthRunLottery 执行抽奖分段：有次数时最多抽 1 次。
// [参数] sa：账号凭据；res：结果累加器。
// [返回] 无（结果写入 res）。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthRunLottery(sa *storedAuth, res *growthResult) {
	// 1. 读抽奖次数；失败即记录并退出本段
	chances, err := fetchGrowthLotteryChances(sa)
	if err != nil {
		res.Errors = append(res.Errors, "lottery_chances: "+err.Error())
		return
	}
	// 2. 无次数则本段无操作（每轮最多抽 1 次）
	if jsonI64(chances, "balance") <= 0 {
		return
	}
	// 3. 抽 1 次并累加收益
	m, drawErr := drawLottery(sa)
	if drawErr != nil {
		res.Errors = append(res.Errors, "lottery_draw: "+drawErr.Error())
		return
	}
	res.CreditsGained += jsonI64(m, "credit")
	res.EnergyGained += jsonI64(m, "energy")
	res.Segments = append(res.Segments, "lottery:"+jsonStr(m, "prize_name", "prize"))
}

// growthRunBuddyBox 执行盲盒分段：可开时开启一个盲盒。
// [参数] sa：账号凭据；res：结果累加器。
// [返回] 无（结果写入 res）。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func growthRunBuddyBox(sa *storedAuth, res *growthResult) {
	// 1. 读盲盒额度；失败即记录并退出本段
	quota, err := fetchBuddyQuota(sa)
	if err != nil {
		res.Errors = append(res.Errors, "buddy_quota: "+err.Error())
		return
	}
	// 2. 不可开则本段无操作
	if !jsonBool(quota, "affordable") {
		return
	}
	// 3. 开 1 个盲盒并累加收益
	m, openErr := openBuddyBox(sa, 1)
	if openErr != nil {
		res.Errors = append(res.Errors, "buddy_open: "+openErr.Error())
		return
	}
	res.EnergyGained += jsonI64(m, "energy")
	res.CreditsGained += jsonI64(m, "credit")
	res.Segments = append(res.Segments, "buddy_open")
}

// mapOrEmpty 返回 m 中 key 对应的嵌套对象，缺失或类型不符时返回空 map。
// [参数] m：外层 map；key：嵌套字段名。
// [返回] 嵌套 map（永不为 nil）。
// 最近修改时间：2026-10-15 21:40:00 新增成长中心编排（TASK-2.1）
func mapOrEmpty(m map[string]any, key string) map[string]any {
	// 1. 取嵌套对象；m 为 nil 或字段缺失/类型不符时统一返回空 map
	if m == nil {
		return map[string]any{}
	}
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}
