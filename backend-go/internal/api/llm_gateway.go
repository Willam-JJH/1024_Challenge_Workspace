package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	llmModelsPattern     = "GET /v1/models"
	llmChatPattern       = "POST /v1/chat/completions"
	llmEmbeddingsPattern = "POST /v1/embeddings"
	llmResponsesPattern  = "POST /v1/responses"
	llmSearchPattern     = "POST /v1/search"
)

// upstreamClient 与 Python 侧的 httpx.Client(timeout=120) 对齐。
var upstreamClient = &http.Client{Timeout: 120 * time.Second}

type llmEventRow struct {
	ID          string
	BudgetMicro int
	KillSwitch  bool
}

// llmGuard 复刻 services/gateway.guard：熔断、单人封禁与赛事预算。
func (s *Server) llmGuard(w http.ResponseWriter, r *http.Request, userID string) (*llmEventRow, bool) {
	var event llmEventRow
	err := s.writePool().QueryRow(r.Context(),
		`SELECT id, llm_budget_micro, llm_kill_switch FROM events WHERE slug = $1`,
		s.cfg.CurrentEventSlug).Scan(&event.ID, &event.BudgetMicro, &event.KillSwitch)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	if event.KillSwitch {
		writeError(w, r, http.StatusServiceUnavailable, "llm_disabled", "LLM 服务已暂停")
		return nil, false
	}

	var blocked bool
	err = s.writePool().QueryRow(r.Context(),
		`SELECT llm_blocked FROM users WHERE id = $1`, userID).Scan(&blocked)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "需要登录")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	if blocked {
		writeError(w, r, http.StatusForbidden, "forbidden", "你的账号已被暂停使用 LLM")
		return nil, false
	}

	if event.BudgetMicro > 0 {
		var spent int
		if err := s.writePool().QueryRow(r.Context(),
			`SELECT COALESCE(sum(cost_micro), 0) FROM llm_usage`).Scan(&spent); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		if spent >= event.BudgetMicro {
			writeError(w, r, http.StatusPaymentRequired, "budget_exhausted", "赛事 LLM 预算已用完")
			return nil, false
		}
	}
	return &event, true
}

type llmModelRow struct {
	ID               string
	Name             string
	InputPriceMicro  int
	OutputPriceMicro int
	ContextWindow    int
	Enabled          bool
}

type llmRouteRow struct {
	UpstreamID    string
	UpstreamModel string
	Priority      int
	BaseURL       string
	APIKey        string
}

// resolveLLMRoute 复刻 services/gateway.resolve：取优先级最高的可用路由。
func (s *Server) resolveLLMRoute(w http.ResponseWriter, r *http.Request,
	modelName string) (*llmModelRow, *llmRouteRow, bool) {
	var model llmModelRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, name, input_price_micro, output_price_micro, context_window, enabled
		FROM llm_models WHERE name = $1`, modelName).
		Scan(&model.ID, &model.Name, &model.InputPriceMicro, &model.OutputPriceMicro,
			&model.ContextWindow, &model.Enabled)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "模型不存在或已停用")
			return nil, nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, nil, false
	}
	if !model.Enabled {
		writeError(w, r, http.StatusNotFound, "not_found", "模型不存在或已停用")
		return nil, nil, false
	}

	var route llmRouteRow
	err = s.writePool().QueryRow(r.Context(), `
		SELECT u.id, r.upstream_model, r.priority, u.base_url, u.api_key
		FROM llm_routes r JOIN llm_upstreams u ON u.id = r.upstream_id
		WHERE r.model_id = $1 AND r.enabled AND u.enabled
		ORDER BY r.priority LIMIT 1`, model.ID).
		Scan(&route.UpstreamID, &route.UpstreamModel, &route.Priority, &route.BaseURL, &route.APIKey)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusBadGateway, "upstream_failed", "该模型没有可用的上游路由")
			return nil, nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, nil, false
	}
	return &model, &route, true
}

func (s *Server) listLLMModels(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if _, ok := s.llmGuard(w, r, principal.User.ID); !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(),
		`SELECT name, context_window FROM llm_models WHERE enabled`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	type modelEntry struct {
		ID            string `json:"id"`
		Object        string `json:"object"`
		OwnedBy       string `json:"owned_by"`
		ContextWindow int    `json:"context_window"`
	}
	data := make([]modelEntry, 0)
	for rows.Next() {
		var name string
		var contextWindow int
		if err := rows.Scan(&name, &contextWindow); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		data = append(data, modelEntry{
			ID:            name,
			Object:        "model",
			OwnedBy:       "1024-challenge",
			ContextWindow: contextWindow,
		})
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Object string       `json:"object"`
		Data   []modelEntry `json:"data"`
	}{Object: "list", Data: data})
}

func upstreamErrorMessage(data map[string]any) string {
	raw, ok := data["error"]
	if !ok || raw == nil {
		return "上游返回错误"
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return "上游返回错误"
	}
	message, ok := obj["message"]
	if !ok || message == nil {
		return "上游返回错误"
	}
	if text, ok := message.(string); ok && text != "" {
		return text
	}
	return "上游返回错误"
}

// pyInt 近似 Python 的 int(... or 0)。
func pyInt(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return int(parsed)
		}
		return 0
	case float64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return int(parsed)
		}
		return 0
	case bool:
		if typed {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	modelValue, present := payload["model"]
	if !present || !pyTruthy(modelValue) {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_request", "缺少 model 字段")
		return
	}

	if _, ok := s.llmGuard(w, r, principal.User.ID); !ok {
		return
	}
	model, route, ok := s.resolveLLMRoute(w, r, jsonStr(payload, "model", ""))
	if !ok {
		return
	}

	// 流式要边转发边解析用量，后置记账；非流式一次性处理。
	if value, present := payload["stream"]; present && pyTruthy(value) {
		s.chatStream(w, r, principal, model, route, payload)
		return
	}

	body := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		body[key] = value
	}
	body["model"] = route.UpstreamModel
	raw, err := json.Marshal(body)
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}

	url := strings.TrimRight(route.BaseURL, "/") + "/v1/chat/completions"
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	request.Header.Set("Authorization", "Bearer "+route.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := upstreamClient.Do(request)
	if err != nil {
		s.logger.Error("upstream request", "request_id", RequestID(r), "error", err)
		WriteError(w, r, errInternal)
		return
	}
	responseBody, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		s.logger.Error("upstream read", "request_id", RequestID(r), "error", readErr)
		WriteError(w, r, errInternal)
		return
	}

	s.relayCompletion(w, r, principal, model, response.StatusCode, responseBody)
}

// llmNotImplemented 对应 Python 侧预留的三个 OpenAI 兼容端点。
func (s *Server) llmNotImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusNotImplemented, "not_implemented", "该接口尚未实现")
}
