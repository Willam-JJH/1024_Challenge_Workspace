package api

import (
	"net/http"
	"sort"
	"time"

	"challenge-api/internal/auth"
)

const (
	adminLLMOverviewPattern       = "GET /api/v1/admin/llm/overview"
	adminLLMBudgetGetPattern      = "GET /api/v1/admin/llm/budget"
	adminLLMBudgetPutPattern      = "PUT /api/v1/admin/llm/budget"
	adminLLMModelsPattern         = "GET /api/v1/admin/llm/models"
	adminLLMModelCreatePattern    = "POST /api/v1/admin/llm/models"
	adminLLMModelPattern          = "PATCH /api/v1/admin/llm/models/{model_id}"
	adminLLMModelDeletePattern    = "DELETE /api/v1/admin/llm/models/{model_id}"
	adminLLMUpstreamsPattern      = "GET /api/v1/admin/llm/upstreams"
	adminLLMUpstreamCreatePattern = "POST /api/v1/admin/llm/upstreams"
	adminLLMUpstreamPattern       = "PATCH /api/v1/admin/llm/upstreams/{upstream_id}"
	adminLLMUpstreamDeletePattern = "DELETE /api/v1/admin/llm/upstreams/{upstream_id}"
	adminLLMRouteCreatePattern    = "POST /api/v1/admin/llm/models/{model_id}/routes"
	adminLLMRoutePattern          = "PATCH /api/v1/admin/llm/routes/{route_id}"
	adminLLMRouteDeletePattern    = "DELETE /api/v1/admin/llm/routes/{route_id}"
	adminLLMKillSwitchesPattern   = "GET /api/v1/admin/llm/kill-switches"
	adminLLMKillGlobalPattern     = "PUT /api/v1/admin/llm/kill-switches/global"
	adminLLMKillModelPattern      = "PUT /api/v1/admin/llm/kill-switches/models/{model_id}"
	adminLLMKillUserPattern       = "PUT /api/v1/admin/llm/kill-switches/users/{user_id}"
	adminLLMUsageTeamsPattern     = "GET /api/v1/admin/llm/usage/teams"
	adminLLMUsageUsersPattern     = "GET /api/v1/admin/llm/usage/users"
)

type llmBudgetOut struct {
	BudgetMicro    int `json:"budget_micro"`
	SpentMicro     int `json:"spent_micro"`
	RemainingMicro int `json:"remaining_micro"`
}

type llmOverviewOut struct {
	llmBudgetOut
	Requests     int `json:"requests"`
	Burn24HMicro int `json:"burn_24h_micro"`
	Models       int `json:"models"`
	Upstreams    int `json:"upstreams"`
}

func (s *Server) llmBudgetEvent(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	var eventID string
	var budget int
	err := s.writePool().QueryRow(r.Context(),
		`SELECT id, llm_budget_micro FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).
		Scan(&eventID, &budget)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return "", 0, false
		}
		s.writeQueryError(w, r, err)
		return "", 0, false
	}
	return eventID, budget, true
}

func (s *Server) llmSpent(r *http.Request) (int, error) {
	var spent int
	err := s.writePool().QueryRow(r.Context(),
		`SELECT COALESCE(sum(cost_micro), 0) FROM llm_usage`).Scan(&spent)
	return spent, err
}

func (s *Server) llmBudget(w http.ResponseWriter, r *http.Request) (*llmBudgetOut, bool) {
	_, budget, ok := s.llmBudgetEvent(w, r)
	if !ok {
		return nil, false
	}
	spent, err := s.llmSpent(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	remaining := budget - spent
	if remaining < 0 {
		remaining = 0
	}
	return &llmBudgetOut{BudgetMicro: budget, SpentMicro: spent, RemainingMicro: remaining}, true
}

func (s *Server) adminLLMOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	base, ok := s.llmBudget(w, r)
	if !ok {
		return
	}
	var out llmOverviewOut
	out.llmBudgetOut = *base
	since := time.Now().UTC().Add(-24 * time.Hour)
	err := s.writePool().QueryRow(r.Context(), `
		SELECT
			(SELECT count(*) FROM llm_usage),
			(SELECT COALESCE(sum(cost_micro), 0) FROM llm_usage WHERE created_at >= $1),
			(SELECT count(*) FROM llm_models),
			(SELECT count(*) FROM llm_upstreams)`, since).
		Scan(&out.Requests, &out.Burn24HMicro, &out.Models, &out.Upstreams)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminLLMBudget(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	out, ok := s.llmBudget(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type llmBudgetIn struct {
	BudgetMicro *int `json:"budget_micro"`
}

func (s *Server) adminLLMSetBudget(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in llmBudgetIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.BudgetMicro == nil || *in.BudgetMicro < 0 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	eventID, _, ok := s.llmBudgetEvent(w, r)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE events SET llm_budget_micro = $2, updated_at = $3 WHERE id = $1`,
		eventID, *in.BudgetMicro, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := strconvItoa(*in.BudgetMicro)
	if err := s.recordAudit(r.Context(), principal.User.ID, "llm.budget", "event", eventID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	out, ok := s.llmBudget(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type llmModelOut struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	InputPriceMicro  int    `json:"input_price_micro"`
	OutputPriceMicro int    `json:"output_price_micro"`
	ContextWindow    int    `json:"context_window"`
	Enabled          bool   `json:"enabled"`
}

func (s *Server) adminLLMModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, name, input_price_micro, output_price_micro, context_window, enabled
		FROM llm_models ORDER BY name`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]llmModelOut, 0)
	for rows.Next() {
		var item llmModelOut
		if err := rows.Scan(&item.ID, &item.Name, &item.InputPriceMicro, &item.OutputPriceMicro,
			&item.ContextWindow, &item.Enabled); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type llmModelIn struct {
	Name             *string `json:"name"`
	InputPriceMicro  *int    `json:"input_price_micro"`
	OutputPriceMicro *int    `json:"output_price_micro"`
	ContextWindow    *int    `json:"context_window"`
	Enabled          *bool   `json:"enabled"`
}

func (s *Server) adminLLMCreateModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in llmModelIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Name == nil || len([]rune(*in.Name)) < 1 || len([]rune(*in.Name)) > 120 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if (in.InputPriceMicro != nil && *in.InputPriceMicro < 0) ||
		(in.OutputPriceMicro != nil && *in.OutputPriceMicro < 0) ||
		(in.ContextWindow != nil && *in.ContextWindow < 0) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	inputPrice, outputPrice, contextWindow := 0, 0, 0
	if in.InputPriceMicro != nil {
		inputPrice = *in.InputPriceMicro
	}
	if in.OutputPriceMicro != nil {
		outputPrice = *in.OutputPriceMicro
	}
	if in.ContextWindow != nil {
		contextWindow = *in.ContextWindow
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	var one int
	err := s.writePool().QueryRow(r.Context(),
		`SELECT 1 FROM llm_models WHERE name = $1 LIMIT 1`, *in.Name).Scan(&one)
	if err == nil {
		writeError(w, r, http.StatusConflict, "conflict", "同名模型已存在")
		return
	}
	if !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}

	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO llm_models (id, name, input_price_micro, output_price_micro, context_window,
			enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, *in.Name, inputPrice, outputPrice, contextWindow, enabled, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, llmModelOut{
		ID:               id,
		Name:             *in.Name,
		InputPriceMicro:  inputPrice,
		OutputPriceMicro: outputPrice,
		ContextWindow:    contextWindow,
		Enabled:          enabled,
	})
}

func (s *Server) llmModelByID(w http.ResponseWriter, r *http.Request,
	modelID string) (*llmModelOut, bool) {
	var row llmModelOut
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, name, input_price_micro, output_price_micro, context_window, enabled
		FROM llm_models WHERE id = $1`, modelID).
		Scan(&row.ID, &row.Name, &row.InputPriceMicro, &row.OutputPriceMicro,
			&row.ContextWindow, &row.Enabled)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "模型不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return &row, true
}

func (s *Server) adminLLMPatchModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in llmModelIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if (in.InputPriceMicro != nil && *in.InputPriceMicro < 0) ||
		(in.OutputPriceMicro != nil && *in.OutputPriceMicro < 0) ||
		(in.ContextWindow != nil && *in.ContextWindow < 0) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.llmModelByID(w, r, r.PathValue("model_id"))
	if !ok {
		return
	}
	if in.Name != nil {
		row.Name = *in.Name
	}
	if in.InputPriceMicro != nil {
		row.InputPriceMicro = *in.InputPriceMicro
	}
	if in.OutputPriceMicro != nil {
		row.OutputPriceMicro = *in.OutputPriceMicro
	}
	if in.ContextWindow != nil {
		row.ContextWindow = *in.ContextWindow
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE llm_models SET name = $2, input_price_micro = $3, output_price_micro = $4,
			context_window = $5, enabled = $6, updated_at = $7 WHERE id = $1`,
		row.ID, row.Name, row.InputPriceMicro, row.OutputPriceMicro, row.ContextWindow,
		row.Enabled, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) adminLLMDeleteModel(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, ok := s.llmModelByID(w, r, r.PathValue("model_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM llm_models WHERE id = $1`, row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type llmUpstreamOut struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
	HasKey   bool   `json:"has_key"`
}

func (s *Server) adminLLMUpstreams(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, name, base_url, priority, enabled, api_key
		FROM llm_upstreams ORDER BY priority, name`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]llmUpstreamOut, 0)
	for rows.Next() {
		var item llmUpstreamOut
		var apiKey string
		if err := rows.Scan(&item.ID, &item.Name, &item.BaseURL, &item.Priority, &item.Enabled,
			&apiKey); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.HasKey = apiKey != ""
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type llmUpstreamIn struct {
	Name     *string `json:"name"`
	BaseURL  *string `json:"base_url"`
	APIKey   *string `json:"api_key"`
	Priority *int    `json:"priority"`
	Enabled  *bool   `json:"enabled"`
}

func (s *Server) adminLLMCreateUpstream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in llmUpstreamIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Name == nil || len([]rune(*in.Name)) < 1 || len([]rune(*in.Name)) > 120 ||
		in.BaseURL == nil || len([]rune(*in.BaseURL)) < 4 || len([]rune(*in.BaseURL)) > 500 ||
		(in.APIKey != nil && len([]rune(*in.APIKey)) > 500) ||
		(in.Priority != nil && (*in.Priority < 0 || *in.Priority > 1000)) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	apiKey := ""
	if in.APIKey != nil {
		apiKey = *in.APIKey
	}
	priority := 100
	if in.Priority != nil {
		priority = *in.Priority
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO llm_upstreams (id, name, base_url, api_key, enabled, priority,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, *in.Name, *in.BaseURL, apiKey, enabled, priority, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, llmUpstreamOut{
		ID:       id,
		Name:     *in.Name,
		BaseURL:  *in.BaseURL,
		Priority: priority,
		Enabled:  enabled,
		HasKey:   apiKey != "",
	})
}

type llmUpstreamRow struct {
	llmUpstreamOut
	APIKey string
}

func (s *Server) llmUpstreamByID(w http.ResponseWriter, r *http.Request,
	upstreamID string) (*llmUpstreamRow, bool) {
	var row llmUpstreamRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, name, base_url, priority, enabled, api_key
		FROM llm_upstreams WHERE id = $1`, upstreamID).
		Scan(&row.ID, &row.Name, &row.BaseURL, &row.Priority, &row.Enabled, &row.APIKey)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "上游不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	row.HasKey = row.APIKey != ""
	return &row, true
}

func (s *Server) adminLLMPatchUpstream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in llmUpstreamIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if (in.BaseURL != nil && len([]rune(*in.BaseURL)) > 500) ||
		(in.APIKey != nil && len([]rune(*in.APIKey)) > 500) ||
		(in.Priority != nil && (*in.Priority < 0 || *in.Priority > 1000)) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.llmUpstreamByID(w, r, r.PathValue("upstream_id"))
	if !ok {
		return
	}
	if in.Name != nil {
		row.Name = *in.Name
	}
	if in.BaseURL != nil {
		row.BaseURL = *in.BaseURL
	}
	if in.APIKey != nil {
		row.APIKey = *in.APIKey
	}
	if in.Priority != nil {
		row.Priority = *in.Priority
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE llm_upstreams SET name = $2, base_url = $3, api_key = $4, priority = $5,
			enabled = $6, updated_at = $7 WHERE id = $1`,
		row.ID, row.Name, row.BaseURL, row.APIKey, row.Priority, row.Enabled,
		time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	row.HasKey = row.APIKey != ""
	writeJSON(w, http.StatusOK, row.llmUpstreamOut)
}

func (s *Server) adminLLMDeleteUpstream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, ok := s.llmUpstreamByID(w, r, r.PathValue("upstream_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM llm_upstreams WHERE id = $1`, row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type llmRouteOut struct {
	ID            string `json:"id"`
	ModelID       string `json:"model_id"`
	UpstreamID    string `json:"upstream_id"`
	UpstreamName  string `json:"upstream_name"`
	UpstreamModel string `json:"upstream_model"`
	Priority      int    `json:"priority"`
	Enabled       bool   `json:"enabled"`
}

type llmRouteIn struct {
	UpstreamID    *string `json:"upstream_id"`
	UpstreamModel *string `json:"upstream_model"`
	Priority      *int    `json:"priority"`
}

func (s *Server) adminLLMCreateRoute(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in llmRouteIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.UpstreamID == nil || in.UpstreamModel == nil ||
		len([]rune(*in.UpstreamModel)) < 1 || len([]rune(*in.UpstreamModel)) > 160 ||
		(in.Priority != nil && (*in.Priority < 0 || *in.Priority > 1000)) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	model, ok := s.llmModelByID(w, r, r.PathValue("model_id"))
	if !ok {
		return
	}
	upstream, ok := s.llmUpstreamByID(w, r, *in.UpstreamID)
	if !ok {
		return
	}
	var one int
	err := s.writePool().QueryRow(r.Context(), `
		SELECT 1 FROM llm_routes WHERE model_id = $1 AND upstream_id = $2 LIMIT 1`,
		model.ID, upstream.ID).Scan(&one)
	if err == nil {
		writeError(w, r, http.StatusConflict, "conflict", "该模型已有指向这个上游的路由")
		return
	}
	if !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	priority := 100
	if in.Priority != nil {
		priority = *in.Priority
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO llm_routes (id, model_id, upstream_id, upstream_model, priority, enabled,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, true, $6, $6)`,
		id, model.ID, upstream.ID, *in.UpstreamModel, priority, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, llmRouteOut{
		ID:            id,
		ModelID:       model.ID,
		UpstreamID:    upstream.ID,
		UpstreamName:  upstream.Name,
		UpstreamModel: *in.UpstreamModel,
		Priority:      priority,
		Enabled:       true,
	})
}

type flagIn struct {
	Enabled *bool `json:"enabled"`
}

func decodeFlag(w http.ResponseWriter, r *http.Request) (*flagIn, bool) {
	var in flagIn
	if !decodeJSON(w, r, &in) {
		return nil, false
	}
	if in.Enabled == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return nil, false
	}
	return &in, true
}

func (s *Server) adminLLMPatchRoute(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	in, ok := decodeFlag(w, r)
	if !ok {
		return
	}
	routeID := r.PathValue("route_id")
	var one int
	err := s.writePool().QueryRow(r.Context(),
		`SELECT 1 FROM llm_routes WHERE id = $1`, routeID).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "路由不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE llm_routes SET enabled = $2, updated_at = $3 WHERE id = $1`,
		routeID, *in.Enabled, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminLLMDeleteRoute(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	routeID := r.PathValue("route_id")
	result, err := s.writePool().Exec(r.Context(),
		`DELETE FROM llm_routes WHERE id = $1`, routeID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, r, http.StatusNotFound, "not_found", "路由不存在")
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type llmKillSwitchOut struct {
	Global         bool     `json:"global"`
	DisabledModels []string `json:"disabled_models"`
	BlockedUsers   []string `json:"blocked_users"`
}

func (s *Server) adminLLMKillSwitches(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var global bool
	err := s.writePool().QueryRow(r.Context(),
		`SELECT llm_kill_switch FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).Scan(&global)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	out := llmKillSwitchOut{Global: global, DisabledModels: []string{}, BlockedUsers: []string{}}
	rows, err := s.writePool().Query(r.Context(),
		`SELECT id FROM llm_models WHERE NOT enabled`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		out.DisabledModels = append(out.DisabledModels, id)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	rows, err = s.writePool().Query(r.Context(),
		`SELECT id FROM users WHERE llm_blocked`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		out.BlockedUsers = append(out.BlockedUsers, id)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func pyBoolText(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func (s *Server) adminLLMSetGlobalSwitch(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	in, ok := decodeFlag(w, r)
	if !ok {
		return
	}
	eventID, _, ok := s.llmBudgetEvent(w, r)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE events SET llm_kill_switch = $2, updated_at = $3 WHERE id = $1`,
		eventID, !*in.Enabled, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := pyBoolText(*in.Enabled)
	if err := s.recordAudit(r.Context(), principal.User.ID, "llm.kill_switch", "event", eventID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminLLMSetModelSwitch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	in, ok := decodeFlag(w, r)
	if !ok {
		return
	}
	model, ok := s.llmModelByID(w, r, r.PathValue("model_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE llm_models SET enabled = $2, updated_at = $3 WHERE id = $1`,
		model.ID, *in.Enabled, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminLLMSetUserSwitch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	in, ok := decodeFlag(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	var one int
	err := s.writePool().QueryRow(r.Context(),
		`SELECT 1 FROM users WHERE id = $1`, userID).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE users SET llm_blocked = $2, updated_at = $3 WHERE id = $1`,
		userID, !*in.Enabled, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type llmUsageRowOut struct {
	Label            string `json:"label"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	CostMicro        int    `json:"cost_micro"`
}

// groupLLMUsage 按 label 聚合，再按 cost_micro 降序（稳定排序，与 Python 一致）。
func groupLLMUsage(rows []llmUsageRowOut) []llmUsageRowOut {
	order := make([]string, 0, len(rows))
	grouped := map[string]*llmUsageRowOut{}
	for _, row := range rows {
		item, seen := grouped[row.Label]
		if !seen {
			copied := row
			grouped[row.Label] = &copied
			order = append(order, row.Label)
			continue
		}
		item.PromptTokens += row.PromptTokens
		item.CompletionTokens += row.CompletionTokens
		item.CostMicro += row.CostMicro
	}
	out := make([]llmUsageRowOut, 0, len(order))
	for _, label := range order {
		out = append(out, *grouped[label])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CostMicro > out[j].CostMicro })
	return out
}

func (s *Server) adminLLMUsageTeams(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT c.name, u.prompt_tokens, u.completion_tokens, u.cost_micro
		FROM llm_usage u
		JOIN memberships m ON m.user_id = u.user_id
		JOIN companies c ON c.id = m.company_id`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	collected := make([]llmUsageRowOut, 0)
	for rows.Next() {
		var item llmUsageRowOut
		if err := rows.Scan(&item.Label, &item.PromptTokens, &item.CompletionTokens,
			&item.CostMicro); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		collected = append(collected, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, groupLLMUsage(collected))
}

func (s *Server) adminLLMUsageUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT us.display_name, u.prompt_tokens, u.completion_tokens, u.cost_micro
		FROM llm_usage u JOIN users us ON us.id = u.user_id`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	collected := make([]llmUsageRowOut, 0)
	for rows.Next() {
		var item llmUsageRowOut
		if err := rows.Scan(&item.Label, &item.PromptTokens, &item.CompletionTokens,
			&item.CostMicro); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		collected = append(collected, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, groupLLMUsage(collected))
}
