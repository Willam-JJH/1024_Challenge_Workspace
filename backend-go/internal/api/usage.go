package api

import (
	"net/http"
	"time"
)

const (
	usageSummaryPattern  = "GET /api/v1/usage/summary"
	usageRequestsPattern = "GET /api/v1/usage/requests"
	usageTeamPattern     = "GET /api/v1/usage/team"
)

const usageSampleLimit = 100

type usageSample struct {
	ID               string
	ModelID          *string
	PromptTokens     int
	CompletionTokens int
	CostMicro        int
	CreatedAt        time.Time
}

// usageSamples 复刻 Python 的 _usage_rows：最近 100 条。
func (s *Server) usageSamples(r *http.Request, userID string) ([]usageSample, error) {
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, model_id, prompt_tokens, completion_tokens, cost_micro, created_at
		FROM llm_usage WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2`, userID, usageSampleLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]usageSample, 0)
	for rows.Next() {
		var item usageSample
		if err := rows.Scan(&item.ID, &item.ModelID, &item.PromptTokens, &item.CompletionTokens,
			&item.CostMicro, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type usageSummaryOut struct {
	Requests         int `json:"requests"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	CostMicro        int `json:"cost_micro"`
}

func (s *Server) usageSummary(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	samples, err := s.usageSamples(r, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var out usageSummaryOut
	out.Requests = len(samples)
	for _, sample := range samples {
		out.PromptTokens += sample.PromptTokens
		out.CompletionTokens += sample.CompletionTokens
		out.CostMicro += sample.CostMicro
	}
	writeJSON(w, http.StatusOK, out)
}

type usageRequestOut struct {
	ID               string  `json:"id"`
	Model            *string `json:"model"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostMicro        int     `json:"cost_micro"`
	CreatedAt        string  `json:"created_at"`
}

func (s *Server) usageRequests(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	samples, err := s.usageSamples(r, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	ids := make([]string, 0, len(samples))
	for _, sample := range samples {
		if sample.ModelID != nil {
			ids = append(ids, *sample.ModelID)
		}
	}
	names := map[string]string{}
	if len(ids) > 0 {
		rows, err := s.writePool().Query(r.Context(),
			`SELECT id, name FROM llm_models WHERE id = ANY($1)`, ids)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				s.writeQueryError(w, r, err)
				return
			}
			names[id] = name
		}
		rows.Close()
		if rows.Err() != nil {
			s.writeQueryError(w, r, rows.Err())
			return
		}
	}

	out := make([]usageRequestOut, 0, len(samples))
	for _, sample := range samples {
		var name *string
		if sample.ModelID != nil {
			if value, found := names[*sample.ModelID]; found {
				name = &value
			}
		}
		out = append(out, usageRequestOut{
			ID:               sample.ID,
			Model:            name,
			PromptTokens:     sample.PromptTokens,
			CompletionTokens: sample.CompletionTokens,
			CostMicro:        sample.CostMicro,
			CreatedAt:        formatTimeOffset(sample.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) usageTeam(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var companyID string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT company_id FROM memberships WHERE user_id = $1 LIMIT 1`,
		principal.User.ID).Scan(&companyID)
	if err != nil {
		if isNoRows(err) {
			writeJSON(w, http.StatusOK, struct {
				Company   *string `json:"company"`
				Requests  int     `json:"requests"`
				CostMicro int     `json:"cost_micro"`
			}{Requests: 0, CostMicro: 0})
			return
		}
		s.writeQueryError(w, r, err)
		return
	}

	var out usageSummaryOut
	err = s.writePool().QueryRow(r.Context(), `
		SELECT count(*), COALESCE(sum(prompt_tokens), 0),
			COALESCE(sum(completion_tokens), 0), COALESCE(sum(cost_micro), 0)
		FROM llm_usage
		WHERE user_id IN (SELECT user_id FROM memberships WHERE company_id = $1)`,
		companyID).
		Scan(&out.Requests, &out.PromptTokens, &out.CompletionTokens, &out.CostMicro)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	var company *string
	var name string
	err = s.writePool().QueryRow(r.Context(),
		`SELECT name FROM companies WHERE id = $1`, companyID).Scan(&name)
	if err != nil {
		if !isNoRows(err) {
			s.writeQueryError(w, r, err)
			return
		}
	} else {
		company = &name
	}
	writeJSON(w, http.StatusOK, struct {
		Company          *string `json:"company"`
		Requests         int     `json:"requests"`
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		CostMicro        int     `json:"cost_micro"`
	}{company, out.Requests, out.PromptTokens, out.CompletionTokens, out.CostMicro})
}
