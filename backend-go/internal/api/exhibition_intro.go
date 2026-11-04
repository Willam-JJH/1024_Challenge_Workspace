package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	introductionsPattern    = "GET /api/v1/exhibition/introductions"
	introTextPattern        = "PUT /api/v1/exhibition/introductions/{introduction_id}/text"
	introReplyPattern       = "PUT /api/v1/exhibition/introductions/{introduction_id}/reply"
	introDeleteReplyPattern = "DELETE /api/v1/exhibition/introductions/{introduction_id}/reply"
	collaborationsPattern   = "GET /api/v1/exhibition/collaborations"
)

type introductionOut struct {
	ID                string  `json:"id"`
	Role              string  `json:"role"`
	IntroducerCompany string  `json:"introducer_company"`
	TargetCompany     string  `json:"target_company"`
	Text              *string `json:"text"`
	Reply             *string `json:"reply"`
	Status            string  `json:"status"`
}

type collaborationOut struct {
	ID          string `json:"id"`
	FromCompany string `json:"from_company"`
	ToCompany   string `json:"to_company"`
	Kind        string `json:"kind"`
	Body        string `json:"body"`
	Status      string `json:"status"`
}

// myCompanyOrForbidden 复刻 Python 的 _my_company_id。
func (s *Server) myCompanyOrForbidden(w http.ResponseWriter, r *http.Request,
	principal *auth.Principal) (string, bool) {
	companyID, err := s.myCompanyID(r.Context(), principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return "", false
	}
	if companyID == "" {
		writeError(w, r, http.StatusForbidden, "forbidden", "需要先加入一家公司")
		return "", false
	}
	return companyID, true
}

type introductionRow struct {
	ID                string
	IntroducerCompany string
	TargetCompany     string
	IntroducerName    string
	TargetName        string
	Text              pgtype.Text
	Reply             pgtype.Text
	Status            string
	CreatedAt         time.Time
}

func (s *Server) introductionByID(w http.ResponseWriter, r *http.Request,
	introductionID string) (*introductionRow, bool) {
	var row introductionRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT i.id, i.introducer_company_id, i.target_company_id,
			ic.name, tc.name, i.text, i.reply, i.status, i.created_at
		FROM introductions i
		JOIN companies ic ON ic.id = i.introducer_company_id
		JOIN companies tc ON tc.id = i.target_company_id
		WHERE i.id = $1`, introductionID).
		Scan(&row.ID, &row.IntroducerCompany, &row.TargetCompany, &row.IntroducerName,
			&row.TargetName, &row.Text, &row.Reply, &row.Status, &row.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "介绍任务不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return &row, true
}

func introOut(row *introductionRow, myCompanyID string) introductionOut {
	role := "target"
	if row.IntroducerCompany == myCompanyID {
		role = "introducer"
	}
	return introductionOut{
		ID:                row.ID,
		Role:              role,
		IntroducerCompany: row.IntroducerName,
		TargetCompany:     row.TargetName,
		Text:              textPtr(row.Text),
		Reply:             textPtr(row.Reply),
		Status:            row.Status,
	}
}

func (s *Server) listIntroductions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	companyID, ok := s.myCompanyOrForbidden(w, r, principal)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT i.id, i.introducer_company_id, i.target_company_id,
			ic.name, tc.name, i.text, i.reply, i.status, i.created_at
		FROM introductions i
		JOIN companies ic ON ic.id = i.introducer_company_id
		JOIN companies tc ON tc.id = i.target_company_id
		WHERE i.introducer_company_id = $1 OR i.target_company_id = $1
		ORDER BY i.created_at`, companyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]introductionOut, 0, 8)
	for rows.Next() {
		var row introductionRow
		if err := rows.Scan(&row.ID, &row.IntroducerCompany, &row.TargetCompany,
			&row.IntroducerName, &row.TargetName, &row.Text, &row.Reply, &row.Status,
			&row.CreatedAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		out = append(out, introOut(&row, companyID))
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type introTextIn struct {
	Text string `json:"text"`
}

func validIntroText(text string) bool {
	size := len([]rune(text))
	return size >= 1 && size <= 1200
}

func (s *Server) setIntroductionText(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in introTextIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validIntroText(in.Text) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.introductionByID(w, r, r.PathValue("introduction_id"))
	if !ok {
		return
	}
	companyID, ok := s.myCompanyOrForbidden(w, r, principal)
	if !ok {
		return
	}
	if row.IntroducerCompany != companyID {
		writeError(w, r, http.StatusForbidden, "forbidden", "只有介绍方可以撰写")
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE introductions SET text = $2, updated_at = $3 WHERE id = $1`,
		row.ID, in.Text, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	row.Text = pgtype.Text{String: in.Text, Valid: true}
	writeJSON(w, http.StatusOK, introOut(row, companyID))
}

type introReplyIn struct {
	Reply string `json:"reply"`
}

func (s *Server) setIntroductionReply(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in introReplyIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validIntroText(in.Reply) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.introductionByID(w, r, r.PathValue("introduction_id"))
	if !ok {
		return
	}
	companyID, ok := s.myCompanyOrForbidden(w, r, principal)
	if !ok {
		return
	}
	if row.TargetCompany != companyID {
		writeError(w, r, http.StatusForbidden, "forbidden", "只有被介绍的公司可以回复")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE introductions SET reply = $2, updated_at = $3 WHERE id = $1`,
		row.ID, in.Reply, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	row.Reply = pgtype.Text{String: in.Reply, Valid: true}
	writeJSON(w, http.StatusOK, introOut(row, companyID))
}

func (s *Server) deleteIntroductionReply(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.introductionByID(w, r, r.PathValue("introduction_id"))
	if !ok {
		return
	}
	companyID, ok := s.myCompanyOrForbidden(w, r, principal)
	if !ok {
		return
	}
	if row.TargetCompany != companyID {
		writeError(w, r, http.StatusForbidden, "forbidden", "只有被介绍的公司可以删除回复")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE introductions SET reply = NULL, updated_at = $2 WHERE id = $1`,
		row.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) listCollaborations(w http.ResponseWriter, r *http.Request) {
	eventID, err := s.currentEventID(r)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT c.id, src.name, dst.name, c.kind, c.body, c.status
		FROM collaborations c
		JOIN companies src ON src.id = c.from_company_id
		JOIN companies dst ON dst.id = c.to_company_id
		WHERE c.event_id = $1 AND c.status = 'published'
		ORDER BY c.created_at DESC`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]collaborationOut, 0, 8)
	for rows.Next() {
		var item collaborationOut
		if err := rows.Scan(&item.ID, &item.FromCompany, &item.ToCompany, &item.Kind,
			&item.Body, &item.Status); err != nil {
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

