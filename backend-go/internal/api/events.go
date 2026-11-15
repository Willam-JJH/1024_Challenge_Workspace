package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	eventsPattern       = "GET /api/v1/events"
	eventCurrentPattern = "GET /api/v1/events/current"
	eventGetPattern     = "GET /api/v1/events/{event_id}"
	eventStatsPattern   = "GET /api/v1/events/{event_id}/stats"
)

type eventOut struct {
	ID               string  `json:"id"`
	Slug             string  `json:"slug"`
	Name             string  `json:"name"`
	Stage            string  `json:"stage"`
	StartsAt         *string `json:"starts_at"`
	EndsAt           *string `json:"ends_at"`
	RegistrationOpen bool    `json:"registration_open"`
	VotingOpen       bool    `json:"voting_open"`
	CompanySeats     int     `json:"company_seats"`
}

const eventColumns = `id, slug, name, stage, starts_at, ends_at,
	registration_open, voting_open, company_seats`

func scanEvent(row pgx.Row) (*eventOut, error) {
	var event eventOut
	var startsAt, endsAt pgtype.Timestamp
	err := row.Scan(&event.ID, &event.Slug, &event.Name, &event.Stage, &startsAt, &endsAt,
		&event.RegistrationOpen, &event.VotingOpen, &event.CompanySeats)
	if err != nil {
		return nil, err
	}
	event.StartsAt = formatTimePtr(timestampPtr(startsAt))
	event.EndsAt = formatTimePtr(timestampPtr(endsAt))
	return &event, nil
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	rows, err := pool.Query(r.Context(),
		`SELECT `+eventColumns+` FROM events ORDER BY created_at DESC`)
	if err != nil {
		s.logger.Error("events query", "request_id", RequestID(r), "error", err)
		WriteError(w, r, errInternal)
		return
	}
	defer rows.Close()

	events := make([]eventOut, 0, 8)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			s.logger.Error("events scan", "request_id", RequestID(r), "error", err)
			WriteError(w, r, errInternal)
			return
		}
		events = append(events, *event)
	}
	if rows.Err() != nil {
		WriteError(w, r, errInternal)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) currentEvent(w http.ResponseWriter, r *http.Request) {
	event, err := s.eventBySlug(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if event == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	event, err := scanEvent(pool.QueryRow(r.Context(),
		`SELECT `+eventColumns+` FROM events WHERE id = $1`, r.PathValue("event_id")))
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "赛事不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, event)
}

type eventStatsOut struct {
	Companies         int `json:"companies"`
	ProjectsPublished int `json:"projects_published"`
	Votes             int `json:"votes"`
	Comments          int `json:"comments"`
	Visits            int `json:"visits"`
}

func (s *Server) eventStats(w http.ResponseWriter, r *http.Request) {
	pool := s.readPool()
	if pool == nil {
		WriteError(w, r, errInternal)
		return
	}
	ctx := r.Context()
	eventID := r.PathValue("event_id")
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id = $1)`,
		eventID).Scan(&exists); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "赛事不存在")
		return
	}

	var stats eventStatsOut
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM companies WHERE event_id = $1),
			(SELECT count(*) FROM projects WHERE event_id = $1 AND status = 'published'),
			(SELECT count(*) FROM votes WHERE event_id = $1 AND status = 'live'),
			(SELECT count(*) FROM comments WHERE status = 'visible'),
			(SELECT count(*) FROM visits WHERE event_id = $1)`, eventID).
		Scan(&stats.Companies, &stats.ProjectsPublished, &stats.Votes, &stats.Comments, &stats.Visits)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) eventBySlug(r *http.Request) (*eventOut, error) {
	pool := s.readPool()
	if pool == nil {
		return nil, errInternal
	}
	event, err := scanEvent(pool.QueryRow(r.Context(),
		`SELECT `+eventColumns+` FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug))
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return event, nil
}

func (s *Server) writeQueryError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("query", "request_id", RequestID(r), "error", err)
	WriteError(w, r, errInternal)
}
