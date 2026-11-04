package api

import (
	"net/http"
	"sort"
	"time"

	"challenge-api/internal/auth"
)

const (
	adminShortlistPattern       = "POST /api/v1/admin/results/shortlist"
	adminShortlistDeletePattern = "DELETE /api/v1/admin/results/shortlist"
	adminAwardPattern           = "POST /api/v1/admin/results/awards"
	adminAwardDeletePattern     = "DELETE /api/v1/admin/results/awards/{award_id}"
)

type shortlistOut struct {
	ProjectID    string `json:"project_id"`
	ProjectTitle string `json:"project_title"`
	CompanyName  string `json:"company_name"`
	Position     int    `json:"position"`
}

type advanceIn struct {
	TopN int `json:"top_n"`
}

func (s *Server) adminAnnounceShortlist(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in advanceIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.TopN == 0 {
		in.TopN = 8
	}
	if in.TopN < 1 || in.TopN > 50 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	type entry struct {
		projectID    string
		projectTitle string
		companyName  string
		companyID    string
		votes        int
	}
	rows, err := s.writePool().Query(ctx, `
		SELECT p.id, p.title, c.name, c.id
		FROM projects p JOIN companies c ON c.id = p.company_id
		WHERE p.event_id = $1 AND p.status = 'published'`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	entries := make([]entry, 0, 64)
	for rows.Next() {
		var item entry
		if err := rows.Scan(&item.projectID, &item.projectTitle, &item.companyName,
			&item.companyID); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		entries = append(entries, item)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	countRows, err := s.writePool().Query(ctx, `
		SELECT company_id, count(*) FROM votes
		WHERE event_id = $1 AND status = 'live'
		GROUP BY company_id`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	counts := map[string]int{}
	for countRows.Next() {
		var companyID string
		var count int
		if err := countRows.Scan(&companyID, &count); err != nil {
			countRows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		counts[companyID] = count
	}
	countRows.Close()
	if countRows.Err() != nil {
		s.writeQueryError(w, r, countRows.Err())
		return
	}
	for index := range entries {
		entries[index].votes = counts[entries[index].companyID]
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].votes != entries[j].votes {
			return entries[i].votes > entries[j].votes
		}
		return entries[i].companyName < entries[j].companyName
	})
	if in.TopN < len(entries) {
		entries = entries[:in.TopN]
	}

	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`DELETE FROM shortlist_entries WHERE event_id = $1`, eventID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	now := time.Now().UTC()
	out := make([]shortlistOut, 0, len(entries))
	for index, item := range entries {
		id, err := auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO shortlist_entries
				(id, event_id, project_id, position, announced_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5, $5)`,
			id, eventID, item.projectID, index+1, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		out = append(out, shortlistOut{
			ProjectID:    item.projectID,
			ProjectTitle: item.projectTitle,
			CompanyName:  item.companyName,
			Position:     index + 1,
		})
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminWithdrawShortlist(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM shortlist_entries WHERE event_id = $1`, eventID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type awardIn struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	ProjectID string `json:"project_id"`
}

type awardOut struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	ProjectID    string `json:"project_id"`
	ProjectTitle string `json:"project_title"`
	CompanyName  string `json:"company_name"`
}

func (s *Server) adminCreateAward(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in awardIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if len([]rune(in.Kind)) < 1 || len([]rune(in.Kind)) > 32 ||
		len([]rune(in.Title)) < 1 || len([]rune(in.Title)) > 120 || in.ProjectID == "" {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var status, projectTitle, companyID, companyName string
	err = s.writePool().QueryRow(ctx, `
		SELECT p.status, p.title, c.id, c.name
		FROM projects p JOIN companies c ON c.id = p.company_id
		WHERE p.id = $1`, in.ProjectID).
		Scan(&status, &projectTitle, &companyID, &companyName)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "作品不存在或尚未发布")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if status != "published" {
		writeError(w, r, http.StatusNotFound, "not_found", "作品不存在或尚未发布")
		return
	}
	var exists bool
	if err := s.writePool().QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM awards WHERE event_id = $1 AND kind = $2 AND project_id = $3)`,
		eventID, in.Kind, in.ProjectID).Scan(&exists); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if exists {
		writeError(w, r, http.StatusConflict, "conflict", "这个作品已经获得该奖项")
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx, `
		INSERT INTO awards (id, event_id, kind, title, project_id, announced_at, created_at,
			updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $6)`,
		id, eventID, in.Kind, in.Title, in.ProjectID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, awardOut{
		ID:           id,
		Kind:         in.Kind,
		Title:        in.Title,
		ProjectID:    in.ProjectID,
		ProjectTitle: projectTitle,
		CompanyName:  companyName,
	})
}

func (s *Server) adminDeleteAward(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	awardID := r.PathValue("award_id")
	var exists bool
	if err := s.writePool().QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM awards WHERE id = $1)`, awardID).Scan(&exists); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "奖项不存在")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM awards WHERE id = $1`, awardID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
