package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	offlineSessionsPattern = "GET /api/v1/offline-sessions"
	offlineSessionPattern  = "GET /api/v1/offline-sessions/{session_id}"
	offlineCalendarPattern = "GET /api/v1/offline-sessions/{session_id}/calendar.ics"
	offlineRsvpPattern     = "PUT /api/v1/offline-sessions/{session_id}/rsvp"
	offlineCancelPattern   = "DELETE /api/v1/offline-sessions/{session_id}/rsvp"
	announcementsPattern   = "GET /api/v1/announcements"
	sponsorsPattern        = "GET /api/v1/sponsors"
	cspReportPattern       = "POST /api/v1/csp-reports"
)

type rsvpOut struct {
	SessionID  string `json:"session_id"`
	Answer     string `json:"answer"`
	Waitlisted bool   `json:"waitlisted"`
	CheckedIn  bool   `json:"checked_in"`
}

type sessionWithMine struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description *string  `json:"description"`
	Location    *string  `json:"location"`
	StartsAt    string   `json:"starts_at"`
	EndsAt      *string  `json:"ends_at"`
	Seats       int      `json:"seats"`
	Status      string   `json:"status"`
	Mine        *rsvpOut `json:"mine"`
	GoingCount  int      `json:"going_count"`
	SeatsLeft   int      `json:"seats_left"`
}

type offlineSessionRow struct {
	ID          string
	Title       string
	Description pgtype.Text
	Location    pgtype.Text
	StartsAt    time.Time
	EndsAt      pgtype.Timestamp
	Seats       int
	Status      string
}

func (s *Server) offlineSessionByID(w http.ResponseWriter, r *http.Request,
	sessionID string) (*offlineSessionRow, bool) {
	var row offlineSessionRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, title, description, location, starts_at, ends_at, seats, status
		FROM offline_sessions WHERE id = $1`, sessionID).
		Scan(&row.ID, &row.Title, &row.Description, &row.Location, &row.StartsAt,
			&row.EndsAt, &row.Seats, &row.Status)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "场次不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return &row, true
}

func (s *Server) goingAndLeft(w http.ResponseWriter, r *http.Request,
	session *offlineSessionRow) (int, int, bool) {
	var going int
	err := s.writePool().QueryRow(r.Context(), `
		SELECT count(*) FROM offline_rsvps
		WHERE session_id = $1 AND answer = 'going' AND waitlisted = false`,
		session.ID).Scan(&going)
	if err != nil {
		s.writeQueryError(w, r, err)
		return 0, 0, false
	}
	left := session.Seats - going
	if left < 0 {
		left = 0
	}
	return going, left, true
}

func (s *Server) sessionOut(w http.ResponseWriter, r *http.Request,
	session *offlineSessionRow, userID string) (*sessionWithMine, bool) {
	var mine *rsvpOut
	if userID != "" {
		var answer string
		var waitlisted bool
		var checkedIn pgtype.Timestamp
		err := s.writePool().QueryRow(r.Context(), `
			SELECT answer, waitlisted, checked_in_at FROM offline_rsvps
			WHERE session_id = $1 AND user_id = $2`, session.ID, userID).
			Scan(&answer, &waitlisted, &checkedIn)
		if err != nil && !isNoRows(err) {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		if err == nil {
			mine = &rsvpOut{
				SessionID:  session.ID,
				Answer:     answer,
				Waitlisted: waitlisted,
				CheckedIn:  checkedIn.Valid,
			}
		}
	}
	going, left, ok := s.goingAndLeft(w, r, session)
	if !ok {
		return nil, false
	}
	return &sessionWithMine{
		ID:          session.ID,
		Title:       session.Title,
		Description: textPtr(session.Description),
		Location:    textPtr(session.Location),
		StartsAt:    formatTime(session.StartsAt),
		EndsAt:      formatTimePtr(timestampPtr(session.EndsAt)),
		Seats:       session.Seats,
		Status:      session.Status,
		Mine:        mine,
		GoingCount:  going,
		SeatsLeft:   left,
	}, true
}

func (s *Server) listOfflineSessions(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	userID := ""
	if principal != nil {
		userID = principal.User.ID
	}
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, title, description, location, starts_at, ends_at, seats, status
		FROM offline_sessions WHERE event_id = $1 ORDER BY starts_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var sessions []offlineSessionRow
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
		item, ok := s.sessionOut(w, r, &sessions[index], userID)
		if !ok {
			return
		}
		out = append(out, *item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getOfflineSession(w http.ResponseWriter, r *http.Request) {
	principal, err := s.authenticate(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	userID := ""
	if principal != nil {
		userID = principal.User.ID
	}
	session, ok := s.offlineSessionByID(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	out, ok := s.sessionOut(w, r, session, userID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func icsTime(value time.Time) string { return value.UTC().Format("20060102T150405Z") }

func (s *Server) offlineCalendar(w http.ResponseWriter, r *http.Request) {
	session, ok := s.offlineSessionByID(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//1024 Challenge Workspace//ZH",
		"BEGIN:VEVENT",
		"UID:" + session.ID + "@1024-challenge",
		"DTSTAMP:" + icsTime(time.Now().UTC()),
		"DTSTART:" + icsTime(session.StartsAt),
	}
	if session.EndsAt.Valid {
		lines = append(lines, "DTEND:"+icsTime(session.EndsAt.Time))
	}
	lines = append(lines, "SUMMARY:"+session.Title)
	if session.Location.Valid {
		lines = append(lines, "LOCATION:"+session.Location.String)
	}
	if session.Description.Valid {
		lines = append(lines, "DESCRIPTION:"+
			strings.ReplaceAll(session.Description.String, "\n", " "))
	}
	lines = append(lines, "END:VEVENT", "END:VCALENDAR", "")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+session.ID+`.ics"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(strings.Join(lines, "\r\n")))
}

type rsvpIn struct {
	Answer string `json:"answer"`
}

func (s *Server) offlineRsvp(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in rsvpIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Answer != "going" && in.Answer != "maybe" && in.Answer != "no" {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	session, ok := s.offlineSessionByID(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	if session.Status != "scheduled" {
		writeError(w, r, http.StatusConflict, "conflict", "场次已取消")
		return
	}
	if session.StartsAt.Before(time.Now().UTC()) {
		writeError(w, r, http.StatusConflict, "conflict", "场次已开始")
		return
	}
	ctx := r.Context()
	var existingID string
	var checkedIn pgtype.Timestamp
	err := s.writePool().QueryRow(ctx, `
		SELECT id, checked_in_at FROM offline_rsvps WHERE session_id = $1 AND user_id = $2`,
		session.ID, principal.User.ID).Scan(&existingID, &checkedIn)
	if err != nil && !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	if in.Answer == "no" {
		if err == nil {
			if _, err := s.writePool().Exec(ctx,
				`DELETE FROM offline_rsvps WHERE id = $1`, existingID); err != nil {
				s.writeQueryError(w, r, err)
				return
			}
		}
		writeJSON(w, http.StatusOK, rsvpOut{SessionID: session.ID, Answer: "no"})
		return
	}
	_, left, ok := s.goingAndLeft(w, r, session)
	if !ok {
		return
	}
	waitlisted := in.Answer == "going" && left <= 0
	now := time.Now().UTC()
	if err == nil {
		if _, err := s.writePool().Exec(ctx, `
			UPDATE offline_rsvps SET answer = $2, waitlisted = $3, updated_at = $4
			WHERE id = $1`, existingID, in.Answer, waitlisted, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	} else {
		id, err := auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		if _, err := s.writePool().Exec(ctx, `
			INSERT INTO offline_rsvps (id, session_id, user_id, answer, waitlisted,
				created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			id, session.ID, principal.User.ID, in.Answer, waitlisted, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		checkedIn = pgtype.Timestamp{}
	}
	writeJSON(w, http.StatusOK, rsvpOut{
		SessionID:  session.ID,
		Answer:     in.Answer,
		Waitlisted: waitlisted,
		CheckedIn:  checkedIn.Valid,
	})
}

func (s *Server) cancelOfflineRsvp(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	session, ok := s.offlineSessionByID(w, r, r.PathValue("session_id"))
	if !ok {
		return
	}
	ctx := r.Context()
	tag, err := s.writePool().Exec(ctx,
		`DELETE FROM offline_rsvps WHERE session_id = $1 AND user_id = $2`,
		session.ID, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if tag.RowsAffected() > 0 {
		s.promoteWaitlist(w, r, session)
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

// promoteWaitlist 座位释放后按报名先后把候补转正。
func (s *Server) promoteWaitlist(w http.ResponseWriter, r *http.Request,
	session *offlineSessionRow) {
	for {
		_, left, ok := s.goingAndLeft(w, r, session)
		if !ok || left <= 0 {
			return
		}
		var id string
		err := s.writePool().QueryRow(r.Context(), `
			SELECT id FROM offline_rsvps
			WHERE session_id = $1 AND answer = 'going' AND waitlisted = true
			ORDER BY created_at LIMIT 1`, session.ID).Scan(&id)
		if isNoRows(err) {
			return
		}
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE offline_rsvps SET waitlisted = false, updated_at = $2 WHERE id = $1`,
			id, time.Now().UTC()); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
}

type announcementOut struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Body        string  `json:"body"`
	Status      string  `json:"status"`
	PublishedAt *string `json:"published_at"`
	CreatedAt   string  `json:"created_at"`
}

func (s *Server) listAnnouncements(w http.ResponseWriter, r *http.Request) {
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, title, body, status, published_at, created_at
		FROM announcements
		WHERE event_id = $1 AND status = 'published'
		ORDER BY published_at DESC`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]announcementOut, 0, 8)
	for rows.Next() {
		var item announcementOut
		var publishedAt pgtype.Timestamp
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &item.Status,
			&publishedAt, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.PublishedAt = formatTimePtr(timestampPtr(publishedAt))
		item.CreatedAt = formatTime(createdAt)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type sponsorOut struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	LogoURL  *string `json:"logo_url"`
	Website  *string `json:"website"`
	Position int     `json:"position"`
}

func (s *Server) listSponsors(w http.ResponseWriter, r *http.Request) {
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, name, logo_url, website, position
		FROM sponsors WHERE event_id = $1 ORDER BY position, created_at`, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]sponsorOut, 0, 8)
	for rows.Next() {
		var item sponsorOut
		var logo, website pgtype.Text
		if err := rows.Scan(&item.ID, &item.Name, &logo, &website,
			&item.Position); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.LogoURL = textPtr(logo)
		item.Website = textPtr(website)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) cspReport(w http.ResponseWriter, r *http.Request) {
	// 只接收，不记录内容：请求体里可能带页面 URL 与脚本片段。
	w.WriteHeader(http.StatusNoContent)
}
