package api

import (
	"net/http"
	"time"

	"challenge-api/internal/auth"
)

const (
	adminPitchPatchPattern    = "PATCH /api/v1/admin/pitch"
	adminPitchSourcesPattern  = "GET /api/v1/admin/pitch/sources"
	adminPitchSourceCreate    = "POST /api/v1/admin/pitch/sources"
	adminPitchSourcePattern   = "PATCH /api/v1/admin/pitch/sources/{source_id}"
	adminPitchSourceDelete    = "DELETE /api/v1/admin/pitch/sources/{source_id}"
	adminPitchSourceMove      = "POST /api/v1/admin/pitch/sources/{source_id}/moves"
	adminPitchMessagesPattern = "GET /api/v1/admin/pitch/messages"
	adminPitchModeratePattern = "POST /api/v1/admin/pitch/messages/{message_id}/moderation"
)

var pitchKinds = map[string]bool{"bilibili": true, "youtube": true, "twitch": true}
var pitchRegions = map[string]bool{"cn": true, "global": true}

type pitchPatchIn struct {
	Title *string `json:"title"`
	OnAir *bool   `json:"on_air"`
}

func (s *Server) adminPatchPitch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in pitchPatchIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Title != nil && len([]rune(*in.Title)) > 160 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE events SET pitch_title = COALESCE($2, pitch_title),
			pitch_on_air = COALESCE($3, pitch_on_air), updated_at = $4
		WHERE id = $1`, eventID, in.Title, in.OnAir, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var title string
	var onAir bool
	if err := s.writePool().QueryRow(r.Context(),
		`SELECT pitch_title, pitch_on_air FROM events WHERE id = $1`, eventID).
		Scan(&title, &onAir); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Title string `json:"title"`
		OnAir bool   `json:"on_air"`
	}{Title: title, OnAir: onAir})
}

func (s *Server) adminPitchSources(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, kind, name, url, region, enabled, position
		FROM pitch_sources WHERE event_id = $1 ORDER BY position, created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]sourceOut, 0, 4)
	for rows.Next() {
		var item sourceOut
		if err := rows.Scan(&item.ID, &item.Kind, &item.Name, &item.URL, &item.Region,
			&item.Enabled, &item.Position); err != nil {
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

type pitchSourceIn struct {
	Kind    *string `json:"kind"`
	Name    *string `json:"name"`
	URL     *string `json:"url"`
	Region  *string `json:"region"`
	Enabled *bool   `json:"enabled"`
}

func (s *Server) adminCreatePitchSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in pitchSourceIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Kind == nil || !pitchKinds[*in.Kind] ||
		in.Name == nil || len([]rune(*in.Name)) < 1 || len([]rune(*in.Name)) > 80 ||
		in.URL == nil || len([]rune(*in.URL)) < 4 || len([]rune(*in.URL)) > 500 ||
		(in.Region != nil && !pitchRegions[*in.Region]) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	region := "cn"
	if in.Region != nil {
		region = *in.Region
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	var maxPosition int
	if err := s.writePool().QueryRow(r.Context(), `
		SELECT COALESCE(max(position), 0) FROM pitch_sources WHERE event_id = $1`, eventID).
		Scan(&maxPosition); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	position := maxPosition + 1
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO pitch_sources (id, event_id, kind, name, url, region, enabled, position,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		id, eventID, *in.Kind, *in.Name, *in.URL, region, enabled, position, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, sourceOut{
		ID:       id,
		Kind:     *in.Kind,
		Name:     *in.Name,
		URL:      *in.URL,
		Region:   region,
		Enabled:  enabled,
		Position: position,
	})
}

type pitchSourceRow struct {
	sourceOut
	EventID string
}

func (s *Server) pitchSourceByID(w http.ResponseWriter, r *http.Request,
	sourceID string) (*pitchSourceRow, bool) {
	var row pitchSourceRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, kind, name, url, region, enabled, position, event_id
		FROM pitch_sources WHERE id = $1`, sourceID).
		Scan(&row.ID, &row.Kind, &row.Name, &row.URL, &row.Region, &row.Enabled,
			&row.Position, &row.EventID)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "流源不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return &row, true
}

func (s *Server) adminPatchPitchSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in pitchSourceIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if (in.Name != nil && len([]rune(*in.Name)) > 80) ||
		(in.URL != nil && len([]rune(*in.URL)) > 500) ||
		(in.Region != nil && !pitchRegions[*in.Region]) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.pitchSourceByID(w, r, r.PathValue("source_id"))
	if !ok {
		return
	}
	if in.Name != nil {
		row.Name = *in.Name
	}
	if in.URL != nil {
		row.URL = *in.URL
	}
	if in.Region != nil {
		row.Region = *in.Region
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE pitch_sources SET name = $2, url = $3, region = $4, enabled = $5,
			updated_at = $6 WHERE id = $1`,
		row.ID, row.Name, row.URL, row.Region, row.Enabled, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row.sourceOut)
}

func (s *Server) adminDeletePitchSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	row, ok := s.pitchSourceByID(w, r, r.PathValue("source_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM pitch_sources WHERE id = $1`, row.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type pitchMoveIn struct {
	Direction *string `json:"direction"`
}

func (s *Server) adminMovePitchSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in pitchMoveIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Direction == nil || (*in.Direction != "up" && *in.Direction != "down") {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	row, ok := s.pitchSourceByID(w, r, r.PathValue("source_id"))
	if !ok {
		return
	}
	delta := 1
	if *in.Direction == "up" {
		delta = -1
	}
	var neighbourID string
	var neighbourPosition int
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, position FROM pitch_sources
		WHERE event_id = $1 AND position = $2 LIMIT 1`,
		row.EventID, row.Position+delta).Scan(&neighbourID, &neighbourPosition)
	if err != nil && !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	if err == nil {
		now := time.Now().UTC()
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE pitch_sources SET position = $2, updated_at = $3 WHERE id = $1`,
			row.ID, neighbourPosition, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE pitch_sources SET position = $2, updated_at = $3 WHERE id = $1`,
			neighbourID, row.Position, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminPitchMessages(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT m.id, m.author_id, u.display_name, m.body, m.created_at
		FROM pitch_messages m JOIN users u ON u.id = m.author_id
		WHERE m.event_id = $1
		ORDER BY m.created_at DESC LIMIT 200`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]messageOut, 0, 32)
	for rows.Next() {
		var item messageOut
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.AuthorID, &item.AuthorName, &item.Body,
			&createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.CreatedAt = formatTime(createdAt)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type pitchModerationIn struct {
	Hidden *bool `json:"hidden"`
}

func (s *Server) adminModeratePitchMessage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in pitchModerationIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Hidden == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	messageID := r.PathValue("message_id")
	var one int
	err := s.writePool().QueryRow(r.Context(),
		`SELECT 1 FROM pitch_messages WHERE id = $1`, messageID).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "留言不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	status := "visible"
	if *in.Hidden {
		status = "hidden"
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE pitch_messages SET status = $2, updated_at = $3 WHERE id = $1`,
		messageID, status, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
