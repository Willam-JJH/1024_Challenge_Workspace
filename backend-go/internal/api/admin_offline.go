package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	adminOfflineSessionsPattern = "GET /api/v1/admin/offline-sessions"
	adminOfflineCreatePattern   = "POST /api/v1/admin/offline-sessions"
	adminOfflineSessionPattern  = "PATCH /api/v1/admin/offline-sessions/{session_id}"
	adminOfflineDeletePattern   = "DELETE /api/v1/admin/offline-sessions/{session_id}"
	adminOfflineCancelPattern   = "POST /api/v1/admin/offline-sessions/{session_id}/cancellations"
	adminOfflineRsvpsPattern    = "GET /api/v1/admin/offline-sessions/{session_id}/rsvps"
	adminOfflineCheckInPattern  = "PUT /api/v1/admin/offline-sessions/{session_id}/rsvps/{rsvp_id}/check-in"
)

func (s *Server) adminOfflineSessions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, title, description, location, starts_at, ends_at, seats, status
		FROM offline_sessions WHERE event_id = $1 ORDER BY starts_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	sessions := make([]offlineSessionRow, 0, 8)
	for rows.Next() {
		var row offlineSessionRow
		if err := rows.Scan(&row.ID, &row.Title, &row.Description, &row.Location,
			&row.StartsAt, &row.EndsAt, &row.Seats, &row.Status); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		sessions = append(sessions, row)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	out := make([]sessionWithMine, 0, len(sessions))
	for index := range sessions {
		item, ok := s.sessionOut(w, r, &sessions[index], "")
		if !ok {
			return
		}
		out = append(out, *item)
	}
	writeJSON(w, http.StatusOK, out)
}

type offlineSessionCreateIn struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Location    *string `json:"location"`
	StartsAt    *string `json:"starts_at"`
	EndsAt      *string `json:"ends_at"`
	Seats       *int    `json:"seats"`
}

func (s *Server) adminCreateOfflineSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in offlineSessionCreateIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Title == nil || len([]rune(*in.Title)) < 1 || len([]rune(*in.Title)) > 160 ||
		(in.Description != nil && len([]rune(*in.Description)) > 2000) ||
		(in.Location != nil && len([]rune(*in.Location)) > 200) ||
		in.StartsAt == nil || (in.Seats != nil && (*in.Seats < 1 || *in.Seats > 500)) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	startsAt, ok := parseFlexibleTime(*in.StartsAt)
	if !ok {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	var endsAt *time.Time
	if in.EndsAt != nil {
		parsed, ok := parseFlexibleTime(*in.EndsAt)
		if !ok {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		endsAt = &parsed
	}
	seats := 30
	if in.Seats != nil {
		seats = *in.Seats
	}
	eventID, ok := s.eventIDOr404(w, r)
	if !ok {
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO offline_sessions (id, event_id, title, description, location, starts_at,
			ends_at, seats, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'scheduled', $9, $9)`,
		id, eventID, *in.Title, in.Description, in.Location, startsAt,
		nilIfNilTime(endsAt), seats, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	session, ok := s.offlineSessionByID(w, r, id)
	if !ok {
		return
	}
	out, ok := s.sessionOut(w, r, session, "")
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

type offlineSessionPatchIn struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Location    *string `json:"location"`
	StartsAt    *string `json:"starts_at"`
	EndsAt      *string `json:"ends_at"`
	Seats       *int    `json:"seats"`
}

func (s *Server) adminPatchOfflineSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in offlineSessionPatchIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if (in.Title != nil && len([]rune(*in.Title)) > 160) ||
		(in.Description != nil && len([]rune(*in.Description)) > 2000) ||
		(in.Location != nil && len([]rune(*in.Location)) > 200) ||
		(in.Seats != nil && (*in.Seats < 1 || *in.Seats > 500)) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	var startsAt, endsAt *time.Time
	if in.StartsAt != nil {
		parsed, ok := parseFlexibleTime(*in.StartsAt)
		if !ok {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		startsAt = &parsed
	}
	if in.EndsAt != nil {
		parsed, ok := parseFlexibleTime(*in.EndsAt)
		if !ok {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		endsAt = &parsed
	}
	session, ok := s.offlineSessionByID(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE offline_sessions SET title = COALESCE($2, title),
			description = COALESCE($3, description), location = COALESCE($4, location),
			starts_at = COALESCE($5, starts_at), ends_at = COALESCE($6, ends_at),
			seats = COALESCE($7, seats), updated_at = $8
		WHERE id = $1`,
		session.ID, in.Title, in.Description, in.Location, nilIfNilTime(startsAt),
		nilIfNilTime(endsAt), in.Seats, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	session, ok = s.offlineSessionByID(w, r, session.ID)
	if !ok {
		return
	}
	s.promoteWaitlist(w, r, session)
	session, ok = s.offlineSessionByID(w, r, session.ID)
	if !ok {
		return
	}
	out, ok := s.sessionOut(w, r, session, "")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminDeleteOfflineSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	session, ok := s.offlineSessionByID(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	var count int
	if err := s.writePool().QueryRow(r.Context(),
		`SELECT count(*) FROM offline_rsvps WHERE session_id = $1`, session.ID).
		Scan(&count); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if count > 0 {
		writeError(w, r, http.StatusConflict, "conflict", "已经有人报名，请先取消场次")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`DELETE FROM offline_sessions WHERE id = $1`, session.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminCancelOfflineSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	session, ok := s.offlineSessionByID(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE offline_sessions SET status = 'cancelled', updated_at = $2 WHERE id = $1`,
		session.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT u.email FROM offline_rsvps r JOIN users u ON u.id = r.user_id
		WHERE r.session_id = $1`, session.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var emails []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		emails = append(emails, email)
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	for _, email := range emails {
		if err := s.mailer.SendNotice(email, session.Title+" 已取消",
			"这场线下活动已取消，请留意后续通知。"); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type adminRsvpOut struct {
	ID          string  `json:"id"`
	UserID      string  `json:"user_id"`
	UserName    string  `json:"user_name"`
	Answer      string  `json:"answer"`
	Waitlisted  bool    `json:"waitlisted"`
	CheckedInAt *string `json:"checked_in_at"`
	CreatedAt   string  `json:"created_at"`
}

func (s *Server) adminOfflineRsvps(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	session, ok := s.offlineSessionByID(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT r.id, r.user_id, u.display_name, r.answer, r.waitlisted, r.checked_in_at,
			r.created_at
		FROM offline_rsvps r JOIN users u ON u.id = r.user_id
		WHERE r.session_id = $1
		ORDER BY r.waitlisted, r.created_at`, session.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]adminRsvpOut, 0, 16)
	for rows.Next() {
		var item adminRsvpOut
		var checkedIn pgtype.Timestamp
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.UserID, &item.UserName, &item.Answer,
			&item.Waitlisted, &checkedIn, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.CheckedInAt = formatTimePtr(timestampPtr(checkedIn))
		item.CreatedAt = formatTime(createdAt)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type checkInIn struct {
	CheckedIn *bool `json:"checked_in"`
}

func (s *Server) adminCheckIn(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in checkInIn
	if !decodeJSON(w, r, &in) {
		return
	}
	checkedIn := true
	if in.CheckedIn != nil {
		checkedIn = *in.CheckedIn
	}
	sessionID := r.PathValue("session_id")
	if _, ok := s.offlineSessionByID(w, r, sessionID); !ok {
		return
	}
	rsvpID := r.PathValue("rsvp_id")
	var rsvpSession string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT session_id FROM offline_rsvps WHERE id = $1`, rsvpID).Scan(&rsvpSession)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "报名记录不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if rsvpSession != sessionID {
		writeError(w, r, http.StatusNotFound, "not_found", "报名记录不存在")
		return
	}
	var value any
	if checkedIn {
		value = time.Now().UTC()
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE offline_rsvps SET checked_in_at = $2, updated_at = $3 WHERE id = $1`,
		rsvpID, value, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
