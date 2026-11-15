package api

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
	"sync"
	"time"

	"challenge-api/internal/auth"
)

// upstreamStreamClient 不设整体超时：一次流式响应可能持续几分钟。
// 只限制等响应头的时间，避免上游卡死时连接无限挂起。
var upstreamStreamClient = func() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 120 * time.Second
	return &http.Client{Transport: transport}
}()

// maxStreamDuration 是一次流式响应的硬上限。
// 没有它，上游卡住不动时这条连接会一直占着协程和内存。
const maxStreamDuration = 10 * time.Minute

// streamCapability 记住哪些上游不返回 token 用量。
//
// 流式请求拿不到用量就没法计费，按约定只能拒绝；但把一个从没见过的上游
// 一上来就拒了也不对，所以先放行一次，确认它确实不给用量之后再永久拒绝。
type streamCapability struct {
	mu          sync.RWMutex
	unsupported map[string]bool
}

func newStreamCapability() *streamCapability {
	return &streamCapability{unsupported: make(map[string]bool)}
}

func (c *streamCapability) isUnsupported(upstreamID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.unsupported[upstreamID]
}

func (c *streamCapability) markUnsupported(upstreamID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unsupported[upstreamID] = true
}

// parseStreamUsage 从一条 SSE data 载荷里取 usage。
// 上游在 stream_options.include_usage=true 时会把用量放在最后一个 chunk。
func parseStreamUsage(payload []byte) (promptTokens, completionTokens int, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var data map[string]any
	if err := decoder.Decode(&data); err != nil || data == nil {
		return 0, 0, false
	}
	usage, ok := data["usage"].(map[string]any)
	if !ok || usage == nil {
		return 0, 0, false
	}
	return pyInt(usage["prompt_tokens"]), pyInt(usage["completion_tokens"]), true
}

// sseData 取出一行 SSE 里的 data 值；不是 data 行返回 ok=false。
func sseData(line []byte) ([]byte, bool) {
	trimmed := bytes.TrimRight(line, "\r\n")
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return nil, false
	}
	return bytes.TrimLeft(trimmed[len("data:"):], " "), true
}

func isEventStream(contentType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

// writeSSEError 在流已经开始之后报告错误。
// 此时响应头早就发出去了，不能再写 JSON 信封，只能走 SSE 事件。
func writeSSEError(w io.Writer, flusher http.Flusher, code, message string) {
	payload, err := json.Marshal(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

// chatStream 把上游的 SSE 原样转发给客户端，并在流结束后记账。
//
// 用量是本方法唯一的硬约束：拿不到用量就不发 [DONE]，
// 改发一条 error 事件，并把这个上游标记为「不返回用量」。
func (s *Server) chatStream(w http.ResponseWriter, r *http.Request, principal *auth.Principal,
	model *llmModelRow, route *llmRouteRow, payload map[string]any) {

	if s.streamUsage.isUnsupported(route.UpstreamID) {
		writeError(w, r, http.StatusNotImplemented, "stream_usage_unavailable",
			"该上游不返回 token 用量，流式请求无法计费；请改用 stream=false")
		return
	}

	body := make(map[string]any, len(payload)+2)
	for key, value := range payload {
		body[key] = value
	}
	body["model"] = route.UpstreamModel
	body["stream"] = true
	options, _ := body["stream_options"].(map[string]any)
	if options == nil {
		options = make(map[string]any)
	}
	options["include_usage"] = true
	body["stream_options"] = options

	raw, err := json.Marshal(body)
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}

	url := strings.TrimRight(route.BaseURL, "/") + "/v1/chat/completions"
	// 客户端断线要立刻停下，同时给整条流一个上限。
	ctx, cancel := context.WithTimeout(r.Context(), maxStreamDuration)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	request.Header.Set("Authorization", "Bearer "+route.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")

	response, err := upstreamStreamClient.Do(request)
	if err != nil {
		s.logger.Error("upstream stream request", "request_id", RequestID(r), "error", err)
		WriteError(w, r, errInternal)
		return
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode >= 400 {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		s.relayUpstreamFailure(w, r, response.StatusCode, responseBody)
		return
	}

	// 上游可能无视 stream 直接回一份完整 JSON。这种情况按非流式处理，
	// 否则客户端会一直等不到事件。
	if !isEventStream(response.Header.Get("Content-Type")) {
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
		if readErr != nil {
			s.logger.Error("upstream stream read", "request_id", RequestID(r), "error", readErr)
			WriteError(w, r, errInternal)
			return
		}
		s.relayCompletion(w, r, principal, model, response.StatusCode, responseBody)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "internal", "服务端不支持流式输出")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	outcome := forwardSSE(w, flusher.Flush, response.Body)
	clientGone := outcome.writeErr != nil || r.Context().Err() != nil

	if outcome.usageSeen {
		// 用不受取消影响的 context：客户端断线也要把已经产生的用量记上。
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer cancel()
		if err := s.recordLLMUsage(ctx, principal.User.ID, model,
			outcome.promptTokens, outcome.completionTokens); err != nil {
			s.logger.Error("record stream usage", "request_id", RequestID(r), "error", err)
		}
		if !clientGone {
			if len(outcome.doneLine) > 0 {
				_, _ = w.Write(outcome.doneLine)
			} else {
				// 有些上游给了用量却不发 [DONE]。客户端在等结束标记，
				// 这时候必须替它补上，否则界面会一直停在「输出中」。
				_, _ = io.WriteString(w, "data: [DONE]")
				_, _ = io.WriteString(w, "\n\n")
			}
			flusher.Flush()
		}
		return
	}

	if clientGone {
		// 客户端自己断开，上游没问题，不做任何标记。
		return
	}

	// 上游正常结束却没有用量：不发货、不发 [DONE]，并永久记住这个上游不支持。
	s.streamUsage.markUnsupported(route.UpstreamID)
	s.logger.Error("stream usage missing", "request_id", RequestID(r),
		"model", model.Name, "upstream", route.UpstreamID)
	writeSSEError(w, flusher, "stream_usage_unavailable",
		"上游未返回 token 用量，本次请求无法计费，已中止")
}

// streamOutcome 是转发一轮流之后的结论。
type streamOutcome struct {
	promptTokens     int
	completionTokens int
	usageSeen        bool
	// doneLine 是上游发来的 [DONE] 行，扣住不发，由调用方确认用量后再决定。
	doneLine []byte
	writeErr error
}

// forwardSSE 逐行转发上游的 SSE，同时收集 token 用量。
//
// 两件事是刻意的：一是 [DONE] 要扣住，用量没确认之前不能告诉客户端「结束了」；
// 二是除 [DONE] 外每一行都原样转发，不解析、不改写，上游加什么字段都不受影响。
func forwardSSE(dst io.Writer, flush func(), src io.Reader) streamOutcome {
	var outcome streamOutcome

	reader := bufio.NewReaderSize(src, 64*1024)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if data, isData := sseData(line); isData {
				if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
					outcome.doneLine = append(outcome.doneLine[:0], line...)
				} else {
					if p, c, seen := parseStreamUsage(data); seen {
						outcome.promptTokens, outcome.completionTokens = p, c
						outcome.usageSeen = true
					}
					if _, writeErr := dst.Write(line); writeErr != nil {
						outcome.writeErr = writeErr
						return outcome
					}
					flush()
				}
			} else {
				if _, writeErr := dst.Write(line); writeErr != nil {
					outcome.writeErr = writeErr
					return outcome
				}
				if len(bytes.TrimRight(line, "\r\n")) == 0 {
					flush()
				}
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				outcome.writeErr = readErr
			}
			return outcome
		}
	}
}

// relayUpstreamFailure 把上游的非 2xx 响应转成统一错误信封。
func (s *Server) relayUpstreamFailure(w http.ResponseWriter, r *http.Request,
	statusCode int, responseBody []byte) {
	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil || data == nil {
		data = map[string]any{}
	}
	message := upstreamErrorMessage(data)
	if message == "上游返回错误" && len(responseBody) > 0 {
		message = truncateRunes(string(responseBody), 500)
	}
	writeError(w, r, http.StatusBadGateway, "upstream_failed",
		message+"（HTTP "+strconvItoa(statusCode)+"）")
}

// relayCompletion 处理一份完整的（非流式）补全响应：记账后原样返回。
func (s *Server) relayCompletion(w http.ResponseWriter, r *http.Request, principal *auth.Principal,
	model *llmModelRow, statusCode int, responseBody []byte) {

	var data map[string]any
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil || data == nil {
		data = map[string]any{"error": map[string]any{
			"message": truncateRunes(string(responseBody), 500),
		}}
	}

	if statusCode >= 400 {
		s.relayUpstreamFailure(w, r, statusCode, responseBody)
		return
	}

	var promptTokens, completionTokens int
	if usage, ok := data["usage"].(map[string]any); ok {
		promptTokens = pyInt(usage["prompt_tokens"])
		completionTokens = pyInt(usage["completion_tokens"])
	}
	if err := s.recordLLMUsage(r.Context(), principal.User.ID, model, promptTokens, completionTokens); err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	if _, present := data["created"]; !present {
		data["created"] = time.Now().UTC().Unix()
	}
	writeJSON(w, http.StatusOK, data)
}

// recordLLMUsage 写一条计费记录，流式与非流式共用。
func (s *Server) recordLLMUsage(ctx context.Context, userID string, model *llmModelRow,
	promptTokens, completionTokens int) error {

	cost := (promptTokens*model.InputPriceMicro + completionTokens*model.OutputPriceMicro) / 1_000_000
	id, err := auth.NewID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.writePool().Exec(ctx, `
		INSERT INTO llm_usage (id, user_id, model_id, prompt_tokens, completion_tokens,
			cost_micro, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, userID, model.ID, promptTokens, completionTokens, cost, now)
	return err
}
