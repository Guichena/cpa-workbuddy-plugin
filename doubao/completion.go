package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 本文件实现 /chat/completion 的请求体构造与 SSE 流解析。
//
// 协议事实来源：Lxapk/DouBao-CPA-Plugin 的 completion.go（实施总览 EXT-001，
// 无 license，仅作字段名与调用形态参考，实现按本仓库风格重写）+ 本仓库实测。
//
// 两个静默降级陷阱（错了两者都不报错，只是行为不对）：
//  1. 消息文本必须放在 content_block[0].content.text_block.text；写成 content
//     会被当作纯聊天，生成请求只会得到「描述」而不是「生成」。
//  2. option.aggregate_params.mode_id 必须为 "1"；缺失时请求被接受但不进入
//     生成路径。
//
// 与 /im/* 族相反，本端点 Content-Type 必须是纯 "application/json"。

// completionRequest 是 /chat/completion 的请求体。
type completionRequest struct {
	ClientMeta  clientMeta        `json:"client_meta"`
	Messages    []completionMsg   `json:"messages"`
	Option      completionOption  `json:"option"`
	UserContext []any             `json:"user_context"`
	Ext         map[string]string `json:"ext"`
}

// clientMeta 把请求锚定到一个会话。
//
// LocalConversationID 是**必填项**：实测缺失或空串都会被上游以
// 710020202 "common invalid param" 拒绝（HTTP 200 + SSE 错误帧，不报 HTTP 错误），
// 且该值任意非空即可（网页端发的是 "local_<随机串>"）。这一项不在 Lxapk 的
// 参考实现里，是本仓库对照浏览器真实请求逐字段消融定位出来的。
//
// last_section_id / last_message_index 是续接点：新会话可空，续接既有会话时必须带。
type clientMeta struct {
	LocalConversationID string       `json:"local_conversation_id"`
	ConversationID      string       `json:"conversation_id"`
	BotID               string       `json:"bot_id"`
	LastSectionID       string       `json:"last_section_id,omitempty"`
	LastMessageIndex    int64        `json:"last_message_index,omitempty"`
	LocalPermissions    []permission `json:"local_permissions,omitempty"`
}

// permission 是一条本地权限声明，网页端固定全量上报。
type permission struct {
	PermissionName string `json:"permission_name"`
	Status         int    `json:"status"`
}

// completionMsg 是请求中的一条消息。
type completionMsg struct {
	LocalMessageID string         `json:"local_message_id"`
	ContentBlock   []requestBlock `json:"content_block"`
	MessageStatus  int            `json:"message_status"`
}

// requestBlock 是上行消息的一个内容块。
type requestBlock struct {
	BlockType    int              `json:"block_type"`
	Content      requestBlockBody `json:"content"`
	BlockID      string           `json:"block_id"`
	ParentID     string           `json:"parent_id"`
	MetaInfo     []any            `json:"meta_info"`
	AppendFields []any            `json:"append_fields"`
}

// requestBlockBody 是上行块体。
type requestBlockBody struct {
	TextBlock    *textBlockPayload `json:"text_block,omitempty"`
	PCEventBlock string            `json:"pc_event_block"`
}

// textBlockPayload 是文本块内容。
type textBlockPayload struct {
	Text        string `json:"text"`
	IconURL     string `json:"icon_url"`
	IconURLDark string `json:"icon_url_dark"`
	Summary     string `json:"summary"`
}

// completionOption 承载行为开关。
type completionOption struct {
	SendMessageScene  string `json:"send_message_scene"`
	CreateTimeMS      int64  `json:"create_time_ms"`
	CollectID         string `json:"collect_id"`
	IsAudio           bool   `json:"is_audio"`
	AnswerWithSuggest bool   `json:"answer_with_suggest"`
	AgentMode         int    `json:"agent_mode"`
	TTSSwitch         bool   `json:"tts_switch"`
	NeedDeepThink     int    `json:"need_deep_think"`

	// AggregateParams 是必需项，mode_id 缺失会让请求退回纯聊天路径。
	AggregateParams aggregateParams `json:"aggregate_params"`

	ModelConfig modelConfig `json:"model_config"`

	SSERecvEventOptions struct {
		SupportChunkDelta bool `json:"support_chunk_delta"`
	} `json:"sse_recv_event_options"`
	SupportLazyFetchStream bool `json:"support_lazy_fetch_stream"`

	UniqueKey string `json:"unique_key"`
	StartSeq  int    `json:"start_seq"`

	// NeedCreateConversation 要求上游分配新会话；空 conversation_id 且不带它
	// 会被以「参数不合法」拒绝。
	NeedCreateConversation bool `json:"need_create_conversation"`

	ConversationMode string `json:"conversation_mode,omitempty"`
}

// aggregateParams 是生成路径选择参数。
type aggregateParams struct {
	MentionSkillList  string `json:"mention_skill_list"`
	MentionPluginList string `json:"mention_plugin_list"`
	MentionExt        string `json:"mention_ext"`
	ConversationMode  string `json:"conversation_mode"`
	ModeID            string `json:"mode_id"`
	ModelItemKey      string `json:"model_item_key"`
	AgentMode         string `json:"agent_mode"`
	ReasoningEffort   string `json:"reasoning_effort"`
	ProviderID        string `json:"provider_id"`
}

// modelConfig 是模型侧配置。
type modelConfig struct {
	ModelItemKey     string         `json:"model_item_key"`
	ModelExtraParams map[string]any `json:"model_extra_params"`
	ReasoningEffort  int            `json:"reasoning_effort"`
}

// nowMillis 返回当前 Unix 毫秒时间戳。
// [参数] 无。
// [返回] 毫秒时间戳。
// 最近修改时间：2026-10-17；改动原因：新增，请求体 create_time_ms 取值。
func nowMillis() int64 {
	return time.Now().UnixMilli()
}

// buildCompletionRequest 组装一轮对话的请求体。
//
// mode 决定生成行为：Skill 为 0 是普通对话，大于 0 时把 skill id 写进
// mention_skill_list，让上游把请求路由到生成管线。
// [参数] sa: 账号（提供 bot_id）；text: 用户文本；mode: 生成模式；
// conversationID / lastSectionID / lastMessageIndex: 会话续接点；
// needCreate: 是否请求上游分配新会话。
// [返回] 请求体。
// 最近修改时间：2026-10-17；改动原因：新增，GAP-010 契约落地。
func buildCompletionRequest(sa *StoredAuth, text string, mode generationMode, conversationID, lastSectionID string, lastMessageIndex int64, needCreate bool) *completionRequest {
	botID := strings.TrimSpace(sa.BotID)
	if botID == "" {
		botID = defaultBotID
	}

	option := completionOption{
		AgentMode:     2,
		CreateTimeMS:  nowMillis(),
		NeedDeepThink: 0,
		AggregateParams: aggregateParams{
			MentionSkillList:  "[]",
			MentionPluginList: "[]",
			MentionExt:        "[{}]",
			ModeID:            "1",
			ModelItemKey:      "0",
			AgentMode:         "2",
			ReasoningEffort:   "3",
		},
		ModelConfig: modelConfig{
			ModelItemKey:     "0",
			ModelExtraParams: map[string]any{},
			ReasoningEffort:  3,
		},
		UniqueKey:              randomUUID(),
		NeedCreateConversation: needCreate,
	}
	option.SSERecvEventOptions.SupportChunkDelta = true
	option.SupportLazyFetchStream = true

	// 生成请求必须显式点名 skill，否则上游按普通对话处理。
	if mode.Skill > 0 {
		option.AggregateParams.MentionSkillList = fmt.Sprintf("[%d]", mode.Skill)
	}

	generalTaskParam, _ := json.Marshal(map[string]any{
		"runtime_type":     0,
		"agent_task_param": map[string]any{},
		"agent_task_param_change": map[string]any{
			"runtime_changed":           false,
			"device_changed":            false,
			"sandbox_auth_type_changed": false,
		},
		"project_id":               "",
		"need_modify_conversation": false,
	})

	return &completionRequest{
		ClientMeta: clientMeta{
			LocalConversationID: "local_" + randomUUID(),
			ConversationID:      conversationID,
			BotID:               botID,
			LastSectionID:       lastSectionID,
			LastMessageIndex:    lastMessageIndex,
			LocalPermissions: []permission{
				{PermissionName: "ACCESS_COARSE_LOCATION", Status: 3},
				{PermissionName: "ACCESS_FINE_LOCATION", Status: 3},
				{PermissionName: "ACCESS_BACKGROUND_LOCATION", Status: 3},
			},
		},
		Messages: []completionMsg{{
			LocalMessageID: randomUUID(),
			MessageStatus:  0,
			ContentBlock: []requestBlock{{
				BlockType:    blockTypeText,
				BlockID:      randomUUID(),
				ParentID:     "",
				MetaInfo:     []any{},
				AppendFields: []any{},
				Content: requestBlockBody{
					TextBlock: &textBlockPayload{Text: text},
				},
			}},
		}},
		Option:      option,
		UserContext: []any{},
		Ext: map[string]string{
			"agent_mode":                    "2",
			"use_deep_think":                "0",
			"general_task_param":            string(generalTaskParam),
			"collection_id":                 "",
			"is_finish":                     "1",
			"commerce_credit_config_enable": "0",
		},
	}
}

// completionEvent 是一条解码后的 SSE 事件。
type completionEvent struct {
	// Name 是 "event:" 字段。
	Name string
	// Data 是 "data:" 负载（多行 data 以换行拼接）。
	Data []byte
}

// responseBlock 是流式消息中的一个内容块。
//
// Content 用 RawMessage：流式端点内联下发对象，而存储会话端点会二次编码，
// 两种形态都由调用方各自解析。
type responseBlock struct {
	BlockType int             `json:"block_type"`
	BlockID   string          `json:"block_id"`
	Content   json.RawMessage `json:"content"`
	IsFinish  bool            `json:"is_finish"`
	PatchType int             `json:"patch_type"`
}

// blockContent 是本插件读取的块体并集。
type blockContent struct {
	TextBlock     *textBlockContent `json:"text_block"`
	CreationBlock json.RawMessage   `json:"creation_block"`
}

// textBlockContent 是下行文本块内容。
type textBlockContent struct {
	Text string `json:"text"`
}

// streamMessageContent 是 STREAM_MSG_NOTIFY / STREAM_CHUNK 的 content 对象。
type streamMessageContent struct {
	ContentBlock  []responseBlock `json:"content_block"`
	ContentStatus int             `json:"content_status"`
	Ext           json.RawMessage `json:"ext"`
}

// textOf 拼接消息中的文本块。
// [参数] 无。
// [返回] 文本内容。
// 最近修改时间：2026-10-17；改动原因：新增，文本增量提取。
func (m *streamMessageContent) textOf() string {
	var b strings.Builder
	for i := range m.ContentBlock {
		if m.ContentBlock[i].BlockType != blockTypeText {
			continue
		}
		var c blockContent
		if json.Unmarshal(m.ContentBlock[i].Content, &c) != nil || c.TextBlock == nil {
			continue
		}
		b.WriteString(c.TextBlock.Text)
	}
	return b.String()
}

// reasoningOf 拼接深度思考块。
// [参数] 无。
// [返回] 思考内容。
// 最近修改时间：2026-10-17；改动原因：新增，reasoning_content 数据来源。
func (m *streamMessageContent) reasoningOf() string {
	var b strings.Builder
	for i := range m.ContentBlock {
		if m.ContentBlock[i].BlockType != blockTypeDeepThink {
			continue
		}
		var c blockContent
		if json.Unmarshal(m.ContentBlock[i].Content, &c) != nil || c.TextBlock == nil {
			continue
		}
		b.WriteString(c.TextBlock.Text)
	}
	return b.String()
}

// patchOp 是 STREAM_CHUNK 的增量补丁项。
type patchOp struct {
	PatchObject int        `json:"patch_object"`
	PatchType   int        `json:"patch_type"`
	PatchValue  patchValue `json:"patch_value"`
}

// patchValue 是补丁值。
//
// ContentBlock 是当前形态；Content 是旧包装，部分补丁类型仍在用，两种都要认。
type patchValue struct {
	ContentBlock []responseBlock    `json:"content_block"`
	Content      messageContentBody `json:"content"`
	Ext          json.RawMessage    `json:"ext"`
}

// blocks 返回补丁携带的全部块（兼容两种拼写）。
// [参数] 无。
// [返回] 内容块列表。
// 最近修改时间：2026-10-17；改动原因：新增，双形态兼容。
func (p *patchValue) blocks() []responseBlock {
	if len(p.ContentBlock) > 0 {
		return p.ContentBlock
	}
	return p.Content.ContentBlock
}

// textOf 拼接补丁中的文本块。
//
// 空 text_block 只是保持块 id 存活，不是内容，不能当增量。
// [参数] 无。
// [返回] 文本内容。
// 最近修改时间：2026-10-17；改动原因：新增，补丁文本提取。
func (p *patchValue) textOf() string {
	var out strings.Builder
	for _, b := range p.blocks() {
		if b.BlockType != blockTypeText {
			continue
		}
		var c blockContent
		if json.Unmarshal(b.Content, &c) != nil || c.TextBlock == nil {
			continue
		}
		out.WriteString(c.TextBlock.Text)
	}
	return out.String()
}

// messageContentBody 是补丁与整消息共用的 content 包装。
type messageContentBody struct {
	ContentBlock []responseBlock `json:"content_block"`
}

// streamError 是 STREAM_ERROR 事件的负载。
type streamError struct {
	ErrorCode int    `json:"error_code"`
	ErrorMsg  string `json:"error_msg"`
}

// parseStreamError 解码 STREAM_ERROR 事件。
// [参数] data: 事件负载。
// [返回] 错误结构；不是错误帧时第二返回值为 false。
// 最近修改时间：2026-10-17；改动原因：新增，流内业务错误识别。
func parseStreamError(data []byte) (*streamError, bool) {
	var e streamError
	if json.Unmarshal(data, &e) != nil || e.ErrorCode == 0 {
		return nil, false
	}
	return &e, true
}

// scanSSE 逐帧解析 SSE 流。
//
// 分帧规则：空行结束一帧；"event:" 取事件名；"data:" 可多行，以换行拼接。
// 缓冲区放大到 8MiB：生成块携带图片数据时单行可轻易超过 bufio 默认的 64KiB，
// 否则会被当成超长行丢弃。
// onEvent 返回 false 表示调用方已拿到所需内容，立即停止读取。
// [参数] r: 流读取器；onEvent: 事件回调。
// [返回] 读取错误。
// 最近修改时间：2026-10-17；改动原因：新增，SSE 分帧核心。
func scanSSE(r io.Reader, onEvent func(completionEvent) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)

	var current completionEvent
	flush := func() bool {
		if current.Name == "" && len(current.Data) == 0 {
			return true
		}
		keep := onEvent(current)
		current = completionEvent{}
		return keep
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if !flush() {
				return nil
			}
		case strings.HasPrefix(line, "event:"):
			current.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			chunk := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if len(current.Data) > 0 {
				current.Data = append(current.Data, '\n')
			}
			current.Data = append(current.Data, chunk...)
		}
	}
	// 流结束时若没有尾随空行，最后一帧仍然有效。
	if !flush() {
		return nil
	}
	return scanner.Err()
}

// newUpstreamClient 构造带上游凭据的 HTTP 客户端。
//
// 使用 durableJar 而非原始 header：上游下发的 Set-Cookie 需要被捕获并落盘
// （见 cookiejar.go）。已落盘的快照优先，首次导入的账号用原始 header 播种。
// [参数] sa: 账号；timeout: 客户端超时，0 表示不设（流式请求由 context 约束）。
// [返回] HTTP 客户端与 Jar。
// 最近修改时间：2026-10-17；改动原因：新增，统一上游传输入口。
func newUpstreamClient(sa *StoredAuth, timeout time.Duration) (*http.Client, *durableJar) {
	jar := newDurableJar()
	if len(sa.Cookies) > 0 {
		jar.restore(sa.Cookies)
	} else {
		jar.seedFromHeader(sa.CookieHeader, doubaoSiteHost+"/")
	}
	if sa.PhysicalPath != "" {
		jar.bind(sa.PhysicalPath)
	}
	return &http.Client{Jar: jar, Timeout: timeout}, jar
}

// applyUpstreamHeaders 设置业务端点所需的请求头。
// [参数] req: 请求。
// [返回] 无。
// 最近修改时间：2026-10-17；改动原因：新增，请求头统一设置。
func applyUpstreamHeaders(req *http.Request) {
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Origin", doubaoSiteHost)
	req.Header.Set("Referer", doubaoSiteHost+"/chat/")
	req.Header.Set("Agw-Js-Conv", "str")
}

// completionEndpointOverride 是 /chat/completion 端点的测试覆盖点；非空时优先使用。
// 生产路径下始终为空，端点由 doubaoSiteHost + 路径常量拼装。
var completionEndpointOverride string

// swapCompletionEndpoint 临时覆盖对话端点，返回恢复函数（仅测试使用）。
// [参数] endpoint: 替换后的完整 URL；空串表示恢复默认。
// [返回] 恢复原值的函数。
// 最近修改时间：2026-10-17；改动原因：新增，让流式链路可在 httptest 下验证。
func swapCompletionEndpoint(endpoint string) func() {
	prev := completionEndpointOverride
	completionEndpointOverride = endpoint
	return func() { completionEndpointOverride = prev }
}

// completionURL 返回对话端点完整地址（测试可通过 swapCompletionEndpoint 覆盖）。
// [参数] sa: 账号（提供设备级标识）。
// [返回] 完整 URL。
// 最近修改时间：2026-10-17；改动原因：新增，端点解析与测试注入分离。
func completionURL(sa *StoredAuth) string {
	if completionEndpointOverride != "" {
		return completionEndpointOverride
	}
	return endpointURL(sa, "/chat/completion", nil)
}

// streamCompletion 发起一次 /chat/completion 请求并逐帧回调。
//
// 流式请求不设客户端超时：回复可能持续数分钟，由 ctx 的 deadline 约束。
// [参数] ctx: 上下文；sa: 账号；body: 请求体；onEvent: 事件回调（返回 false 提前结束）。
// [返回] 请求或读取错误。
// 最近修改时间：2026-10-17；改动原因：新增，SSE 上游调用。
func streamCompletion(ctx context.Context, sa *StoredAuth, body *completionRequest, onEvent func(completionEvent) bool) error {
	raw, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		return fmt.Errorf("编码请求失败：%w", errMarshal)
	}

	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, completionURL(sa), bytes.NewReader(raw))
	if errReq != nil {
		return fmt.Errorf("构造请求失败：%w", errReq)
	}
	// 与 /im/* 相反：本端点只接受纯 application/json。
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", "text/event-stream")
	applyUpstreamHeaders(req)

	client, jar := newUpstreamClient(sa, 0)
	resp, errDo := client.Do(req)
	if errDo != nil {
		return fmt.Errorf("请求上游失败：%w", errDo)
	}
	defer resp.Body.Close()
	defer func() {
		// 滚动下发的设备 Cookie 在请求结束后落盘；失败不影响本次回复。
		_ = jar.persist(sa)
	}()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &upstreamError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("上游返回 HTTP %d：%s", resp.StatusCode, truncateText(string(payload), 200)),
		}
	}

	return scanSSE(resp.Body, onEvent)
}

// truncateText 截断过长文本，用于错误信息。
// [参数] s: 原文；n: 最大字节数。
// [返回] 截断后的文本。
// 最近修改时间：2026-10-17；改动原因：新增，错误信息裁剪。
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// businessEndpointOverride 是业务 JSON 端点的测试覆盖点；非空时所有业务 POST
// 都打到该地址。生产路径下始终为空。
var businessEndpointOverride string

// swapBusinessEndpoint 临时覆盖业务端点基址，返回恢复函数（仅测试使用）。
// [参数] base: 替换后的基址（不含路径）；空串表示恢复默认。
// [返回] 恢复原值的函数。
// 最近修改时间：2026-10-17；改动原因：新增，无水印解析等端点可在 httptest 下验证。
func swapBusinessEndpoint(base string) func() {
	prev := businessEndpointOverride
	businessEndpointOverride = base
	return func() { businessEndpointOverride = prev }
}

// postBusinessJSON 向业务端点发一次 JSON POST 并返回原始响应体。
//
// 与 streamCompletion 的区别：这些端点（如无水印解析）返回普通 JSON 而非 SSE。
// [参数] ctx: 上下文；sa: 账号；path: 端点路径；raw: 已编码的请求体。
// [返回] 响应体与错误。
// 最近修改时间：2026-10-17；改动原因：新增，非流式业务端点调用。
func postBusinessJSON(ctx context.Context, sa *StoredAuth, path string, raw []byte) ([]byte, error) {
	url := endpointURL(sa, path, nil)
	if businessEndpointOverride != "" {
		url = businessEndpointOverride + path
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if errReq != nil {
		return nil, fmt.Errorf("构造请求失败：%w", errReq)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", "application/json")
	applyUpstreamHeaders(req)

	client, jar := newUpstreamClient(sa, time.Duration(currentConfig().TimeoutSeconds)*time.Second)
	resp, errDo := client.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("请求上游失败：%w", errDo)
	}
	defer resp.Body.Close()
	defer func() {
		_ = jar.persist(sa)
	}()

	body, errRead := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if errRead != nil {
		return nil, fmt.Errorf("读取上游响应失败：%w", errRead)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &upstreamError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("上游返回 HTTP %d：%s", resp.StatusCode, truncateText(string(body), 200)),
		}
	}
	return body, nil
}

// completionResult 是一次对话的聚合结果。
type completionResult struct {
	// Text 是可见回复正文。
	Text string
	// Reasoning 是深度思考内容。
	Reasoning string
	// ConversationID 是上游分配的会话 id。
	ConversationID string
	// SectionID 是本次回复所在分节 id。
	SectionID string
	// MessageIndex 是本次提问在会话中的序号。
	MessageIndex int64
	// Assets 是本次回复中的生成结果（图片/视频），纯文本回复为空。
	Assets []creationAsset
}

// collectCompletion 执行一轮对话并聚合完整结果。
//
// 文本聚合口径（2026-10-17 实测确认）：回复正文分散在多种事件里，必须按到达顺序
// 累加——STREAM_MSG_NOTIFY 的首块、STREAM_CHUNK 的 patch、CHUNK_DELTA 的 text
// 各贡献一段，缺任何一类都会丢字。反过来，FULL_MSG_NOTIFY 携带的是**用户自己的
// 提问回显**（message.user_type=1），把它计入会把问题拼进回答；只有 user_type=2
// 的整消息才是助手回复，可作最终权威值覆盖累加结果。
//
// onDelta 非空时，每收到一段文本增量回调一次（流式调用方使用）。
// [参数] ctx: 上下文；sa: 账号；text: 用户文本；mode: 生成模式；
// conversationID: 会话 id（空表示新会话）；needCreate: 是否要求上游新建会话；
// onDelta: 文本增量回调。
// [返回] 聚合结果与错误。
// 最近修改时间：2026-10-17；改动原因：新增，TASK-004 流解析聚合。
func collectCompletion(ctx context.Context, sa *StoredAuth, text string, mode generationMode, conversationID string, needCreate bool, onDelta func(string)) (*completionResult, error) {
	body := buildCompletionRequest(sa, text, mode, conversationID, "", 0, needCreate)

	var (
		textBuf   strings.Builder
		fullText  string
		reasoning strings.Builder
		assets    []creationAsset
		convID    = conversationID
		sectionID string
		msgIndex  int64
		streamErr error
	)

	// appendText 按到达顺序累加正文，并同步回调增量。
	appendText := func(piece string) {
		if piece == "" {
			return
		}
		textBuf.WriteString(piece)
		if onDelta != nil {
			onDelta(piece)
		}
	}

	handleEvent := func(ev completionEvent) bool {
		switch ev.Name {
		case "SSE_ACK":
			var ack struct {
				AckClientMeta struct {
					ConversationID string `json:"conversation_id"`
					SectionID      string `json:"section_id"`
				} `json:"ack_client_meta"`
				QueryList []struct {
					MessageIndex int64 `json:"message_index"`
				} `json:"query_list"`
			}
			if json.Unmarshal(ev.Data, &ack) == nil {
				if ack.AckClientMeta.ConversationID != "" {
					convID = ack.AckClientMeta.ConversationID
				}
				sectionID = ack.AckClientMeta.SectionID
				if len(ack.QueryList) > 0 {
					msgIndex = ack.QueryList[0].MessageIndex
				}
			}

		case "FULL_MSG_NOTIFY":
			// 整消息事件：user_type=1 是用户提问回显，必须忽略；
			// user_type=2 才是助手回复，作为最终权威值。
			var payload struct {
				Message struct {
					UserType     int             `json:"user_type"`
					ContentBlock []responseBlock `json:"content_block"`
					Content      string          `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(ev.Data, &payload) == nil && payload.Message.UserType == 2 {
				content := streamMessageContent{ContentBlock: payload.Message.ContentBlock}
				if t := content.textOf(); t != "" {
					fullText = t
				}
				if r := content.reasoningOf(); r != "" {
					reasoning.Reset()
					reasoning.WriteString(r)
				}
				assets = collectCreationAssets(content.ContentBlock, assets)
			}

		case "STREAM_MSG_NOTIFY":
			var payload struct {
				Content streamMessageContent `json:"content"`
			}
			if json.Unmarshal(ev.Data, &payload) == nil {
				appendText(payload.Content.textOf())
				if r := payload.Content.reasoningOf(); r != "" {
					reasoning.Reset()
					reasoning.WriteString(r)
				}
				assets = collectCreationAssets(payload.Content.ContentBlock, assets)
			}

		case "STREAM_CHUNK", "CHUNK_DELTA":
			var payload struct {
				Content streamMessageContent `json:"content"`
				PatchOp []patchOp            `json:"patch_op"`
				Delta   string               `json:"delta"`
				Text    string               `json:"text"`
			}
			if json.Unmarshal(ev.Data, &payload) != nil {
				break
			}

			// 同一帧可能同时携带多种形态，按优先级只取一种，避免重复。
			switch {
			case payload.Delta != "":
				appendText(payload.Delta)
			case payload.Text != "":
				appendText(payload.Text)
			default:
				for _, op := range payload.PatchOp {
					appendText(op.PatchValue.textOf())
				}
				appendText(payload.Content.textOf())
			}
			if r := payload.Content.reasoningOf(); r != "" {
				reasoning.WriteString(r)
			}
			// 生成结果可能出现在 patch_value 里（实测路径），也可能在 content 里。
			for _, op := range payload.PatchOp {
				assets = collectCreationAssets(op.PatchValue.blocks(), assets)
			}
			assets = collectCreationAssets(payload.Content.ContentBlock, assets)

		case "STREAM_ERROR", "ERROR":
			if e, ok := parseStreamError(ev.Data); ok {
				streamErr = &upstreamError{Code: e.ErrorCode, Message: statusCodeHint(e.ErrorCode, e.ErrorMsg)}
				return false
			}

		case "SSE_REPLY_END":
			// 正文在首个 SSE_REPLY_END（end_type=1，消息已结束）之前全部到齐；
			// 其后的帧只是推荐追问等附属信息。立即停止读取，不等待 Keep-Alive 空闲连接。
			return false
		}
		return true
	}

	if errStream := streamCompletion(ctx, sa, body, handleEvent); errStream != nil {
		return nil, errStream
	}
	if streamErr != nil {
		return nil, streamErr
	}

	// 助手整消息事件存在时以它为准（它包含完整正文），否则用增量累加结果。
	final := textBuf.String()
	if fullText != "" {
		final = fullText
	}
	// 生成类回复可能只有媒体没有正文；有媒体就不算空回复。
	if strings.TrimSpace(final) == "" && strings.TrimSpace(reasoning.String()) == "" && len(assets) == 0 {
		return nil, errors.New("上游没有返回内容")
	}
	if len(assets) > 0 {
		assets = hydrateAssets(ctx, sa, assets)
	}

	return &completionResult{
		Text:           final,
		Reasoning:      reasoning.String(),
		ConversationID: convID,
		SectionID:      sectionID,
		MessageIndex:   msgIndex,
		Assets:         assets,
	}, nil
}
