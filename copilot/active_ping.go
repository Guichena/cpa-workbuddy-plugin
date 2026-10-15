package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// testActiveResponse 是面板「测试」按钮的结果结构。
type testActiveResponse struct {
	OK        bool   `json:"ok"`
	Model     string `json:"model,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// pingUpstreamOnce 向上游发起一次流式首包即停探活，仅返回传输结果，不写入账号状态。
//
// 契约（对齐既有插件成熟策略）：
//  1. 必须用流式请求（stream=true），收到首个有效输出帧即判可用并异步关流，
//     避免思考型模型生成完整思维链导致的数秒等待；
//  2. 严禁仅凭「有字节流出」或 HTTP 200 盲判成功——HTTP 200 下的 error 帧
//     同样代表失败，必须解析内容。
//
// [参数] sa: 目标账号凭据；model: 上游模型 ID。
// [返回] ok 表示收到有效输出帧；statusCode 为 HTTP 状态码；errMsg 为失败原因；latencyMS 为耗时。
// 最近修改时间：2026-10-15 23:40:00；改动原因：新增，面板测试模型的传输内核。
func pingUpstreamOnce(sa *StoredAuth, model string) (ok bool, statusCode int, errMsg string, latencyMS int64) {
	if sa == nil {
		return false, 0, "nil account", 0
	}
	cfg := currentConfig()

	// 1. 构造最小流式探活负载。
	pingPayload, errPayload := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1,
		"stream":     true,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
	})
	if errPayload != nil {
		return false, 0, errPayload.Error(), 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	httpReq, err := prepareUpstreamRequest(ctx, pingPayload, sa, cfg)
	if err != nil {
		return false, 0, err.Error(), 0
	}

	// 2. 用推理客户端发起请求（不设整体超时，避免长思考被掐断）。
	start := time.Now()
	resp, err := inferenceHTTPClient.Do(httpReq)
	if err != nil {
		return false, 0, err.Error(), time.Since(start).Milliseconds()
	}
	defer func() {
		go func() {
			_ = resp.Body.Close()
			cancel()
		}()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return false, resp.StatusCode,
			fmt.Sprintf("upstream status %d: %s", resp.StatusCode, truncateForLog(string(body), 200)),
			time.Since(start).Milliseconds()
	}

	// 3. 读取首个完整 SSE 帧即视为流已建立，同时拦截流内错误帧。
	//    按帧边界（空行）读取而非固定字节数：错误帧的 JSON 可能超过 512 字节，
	//    截断后解析失败会漏判，把坏号误判为可用。
	firstChunk := readFirstSSEFrame(resp.Body)
	latency := time.Since(start).Milliseconds()

	if firstChunk == "" {
		return false, resp.StatusCode, "upstream closed stream without any chunk", latency
	}
	if errFrame := detectStreamError(firstChunk); errFrame != "" {
		return false, http.StatusForbidden, errFrame, latency
	}
	return true, resp.StatusCode, "", latency
}

// readFirstSSEFrame 读取首个完整 SSE 帧（以空行分隔），带上限保护。
//
// 上限 8KB：正常首帧（含 prompt_filter_results）通常 < 2KB，给足余量同时
// 防止对端持续吐数据却不发空行导致读取无限延长。
// [参数] body: 上游响应体。
// [返回] 首帧文本；无任何字节时返回空串。
// 最近修改时间：2026-10-16 00:30:00；改动原因：新增，避免固定字节读取截断错误帧 JSON。
func readFirstSSEFrame(body io.Reader) string {
	const maxFrameBytes = 8 << 10
	reader := bufio.NewReader(body)

	var builder strings.Builder
	for builder.Len() < maxFrameBytes {
		line, err := reader.ReadString('\n')
		if line != "" {
			builder.WriteString(line)
			// 空行（或仅含 \r\n）即帧结束。
			if trimmed := strings.TrimSpace(line); trimmed == "" {
				break
			}
		}
		if err != nil {
			break
		}
	}
	return builder.String()
}

// detectStreamError 从首个 SSE 分片文本中提取业务错误。
//
// Copilot 上游在配额耗尽 / 模型不可用等场景仍返回 HTTP 200，错误放在
// SSE 帧体（event: error 或 JSON error 字段）中；只判状态码会把坏号误判为成功。
// [参数] chunk: 首个分片文本。
// [返回] 错误描述；无错误时返回空串。
// 最近修改时间：2026-10-15 23:40:00；改动原因：新增，拦截 HTTP 200 下的业务错误帧。
func detectStreamError(chunk string) string {
	trimmed := strings.TrimSpace(chunk)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "event: error") {
		return "上游返回业务错误帧: " + truncateForLog(extractSSEPayload(trimmed), 160)
	}
	for _, line := range strings.Split(trimmed, "\n") {
		payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:"))
		if payload == "" || payload == "[DONE]" || !strings.HasPrefix(payload, "{") {
			continue
		}
		if errNode := gjson.Get(payload, "error"); errNode.Exists() && errNode.String() != "" {
			return "上游返回错误: " + truncateForLog(errNode.String(), 160)
		}
	}
	return ""
}

// extractSSEPayload 从 SSE 文本中取出 data: 行的载荷。
// [参数] text: SSE 文本。
// [返回] 首个 data 行载荷；无 data 行时返回原文。
func extractSSEPayload(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	return text
}

// truncateForLog 截断文本用于错误展示，避免超长响应体淹没结果。
// [参数] text: 原文；limit: 最大字符数。
// [返回] 截断后的文本。
func truncateForLog(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

// handleTestActive 处理面板「测试」按钮发起的单账号手动活跃推理请求。
//
// 手动测试绕过定时节流限制，直接发起一次真实请求并返回结果。
// [参数] body: 面板请求体（{auth_index, model}）。
// [返回] 管理响应。
// 最近修改时间：2026-10-15 23:40:00；改动原因：新增，面板手动测试模型入口。
func handleTestActive(body []byte) ([]byte, error) {
	var req struct {
		AuthIndex string `json:"auth_index"`
		AuthID    string `json:"auth_id"`
		Model     string `json:"model"`
	}
	_ = json.Unmarshal(body, &req)

	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		authIndex = strings.TrimSpace(req.AuthID)
	}
	if authIndex == "" {
		return makeHTTPResponse(http.StatusBadRequest, []byte(`{"error":"auth_index is required"}`))
	}

	sa, err := findAccountByAuthID(authIndex)
	if err != nil {
		return makeHTTPResponse(http.StatusNotFound, []byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		models := authModelsForIndex(sa.AuthID)
		if len(models) == 0 {
			return makeHTTPResponse(http.StatusBadRequest, []byte(`{"error":"no model available to test"}`))
		}
		model = models[0]
	}

	upstreamModel := mappedModel(model, currentConfig())
	ok, statusCode, errMsg, latency := pingUpstreamOnce(sa, upstreamModel)
	result := testActiveResponse{Model: model, LatencyMS: latency}
	if !ok {
		result.Error = fmt.Sprintf("测试失败: %s", errMsg)
		if statusCode != 0 {
			result.Error = fmt.Sprintf("测试失败 (HTTP %d): %s", statusCode, errMsg)
		}
		body, _ := json.Marshal(result)
		return makeHTTPResponse(http.StatusOK, body)
	}

	result.OK = true
	result.Message = fmt.Sprintf("活跃成功 (模型: %s, 耗时: %dms)", model, latency)
	body, _ = json.Marshal(result)
	return makeHTTPResponse(http.StatusOK, body)
}

// findAccountByAuthID 按 auth_id / auth_index 查找账号（兼容带与不带 .json）。
// [参数] authID: 账号标识。
// [返回] 账号凭据与错误。
// 最近修改时间：2026-10-15 23:40:00；改动原因：新增，测试与额度查询共用的账号查找。
func findAccountByAuthID(authID string) (*StoredAuth, error) {
	cleanID := strings.TrimSuffix(strings.TrimSpace(authID), ".json")
	if cleanID == "" {
		return nil, fmt.Errorf("empty auth id")
	}
	accounts, err := listAllAuthFiles()
	if err != nil {
		return nil, err
	}
	for _, acc := range accounts {
		if acc.AuthID == cleanID || acc.AuthID == authID || authFileNameFor(acc.GitHubToken) == cleanID+".json" {
			return acc, nil
		}
	}
	return nil, fmt.Errorf("account %s not found", cleanID)
}
