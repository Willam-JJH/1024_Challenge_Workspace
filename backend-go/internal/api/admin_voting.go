package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const (
	adminVotingPattern      = "GET /api/v1/admin/voting"
	adminBallotsPattern     = "GET /api/v1/admin/voting/ballots"
	adminVoteEventsPattern  = "GET /api/v1/admin/voting/events"
	adminVisibilityPattern  = "PUT /api/v1/admin/voting/visibility"
	adminVoidsPattern       = "POST /api/v1/admin/voting/voids"
)

type votingAdminOut struct {
	Open       bool       `json:"open"`
	Visible    bool       `json:"visible"`
	TotalVotes int        `json:"total_votes"`
	Ballots    int        `json:"ballots"`
	Board      []boardRow `json:"board"`
}

type ballotOut struct {
	ID          string `json:"id"`
	VoterID     string `json:"voter_id"`
	VoterName   string `json:"voter_name"`
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

func (s *Server) adminVoting(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	ctx := r.Context()
	var eventID string
	var open, visible bool
	err := s.writePool().QueryRow(ctx,
		`SELECT id, voting_open, votes_visible FROM events WHERE slug = $1`,
		s.cfg.CurrentEventSlug).Scan(&eventID, &open, &visible)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	board, err := s.buildBoard(ctx, false)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var ballots int
	if err := s.writePool().QueryRow(ctx,
		`SELECT count(*) FROM votes WHERE event_id = $1`, eventID).Scan(&ballots); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	total := 0
	for _, row := range board.Items {
		total += row.Votes
	}
	writeJSON(w, http.StatusOK, votingAdminOut{
		Open:       open,
		Visible:    visible,
		TotalVotes: total,
		Ballots:    ballots,
		Board:      board.Items,
	})
}

func (s *Server) adminBallots(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "live" && status != "withdrawn" && status != "voided" {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	s.writeBallots(w, r, status)
}

func (s *Server) adminVoteEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	s.writeBallots(w, r, "")
}

func (s *Server) writeBallots(w http.ResponseWriter, r *http.Request, status string) {
	ctx := r.Context()
	eventID, err := s.currentEventID(r)
	if err != nil {
		if err == errNoCurrentEvent {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	query := `
		SELECT v.id, u.id, u.display_name, c.id, c.name, v.status, v.created_at
		FROM votes v
		JOIN users u ON u.id = v.voter_id
		JOIN companies c ON c.id = v.company_id
		WHERE v.event_id = $1`
	args := []any{eventID}
	if status != "" {
		query += ` AND v.status = $2`
		args = append(args, status)
	}
	query += ` ORDER BY v.created_at DESC LIMIT 500`

	rows, err := s.writePool().Query(ctx, query, args...)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	ballots := make([]ballotOut, 0, 64)
	for rows.Next() {
		var ballot ballotOut
		var createdAt time.Time
		if err := rows.Scan(&ballot.ID, &ballot.VoterID, &ballot.VoterName, &ballot.CompanyID,
			&ballot.CompanyName, &ballot.Status, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		ballot.CreatedAt = formatTime(createdAt)
		ballots = append(ballots, ballot)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, ballots)
}

type visibilityIn struct {
	Visible bool `json:"visible"`
}

func (s *Server) adminVisibility(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in visibilityIn
	if !decodeJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(ctx,
		`UPDATE events SET votes_visible = $2, updated_at = $3 WHERE id = $1`,
		eventID, in.Visible, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := "False"
	if in.Visible {
		note = "True"
	}
	if err := s.recordAudit(ctx, principal.User.ID, "voting.visibility", "event", eventID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type voidIn struct {
	VoterID   *string `json:"voter_id"`
	CompanyID *string `json:"company_id"`
}

func (s *Server) adminVoids(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in voidIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if (in.VoterID == nil || *in.VoterID == "") && (in.CompanyID == nil || *in.CompanyID == "") {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "需要指定投票人或公司")
		return
	}
	ctx := r.Context()
	eventID, err := s.currentEventID(r)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	query := `UPDATE votes SET status = 'voided', updated_at = $2
		WHERE event_id = $1 AND status = 'live'`
	args := []any{eventID, time.Now().UTC()}
	next := 3
	if in.VoterID != nil && *in.VoterID != "" {
		query += fmt.Sprintf(" AND voter_id = $%d", next)
		args = append(args, *in.VoterID)
		next++
	}
	if in.CompanyID != nil && *in.CompanyID != "" {
		query += fmt.Sprintf(" AND company_id = $%d", next)
		args = append(args, *in.CompanyID)
	}
	tag, err := s.writePool().Exec(ctx, query, args...)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	target := ""
	if in.VoterID != nil && *in.VoterID != "" {
		target = *in.VoterID
	} else if in.CompanyID != nil {
		target = *in.CompanyID
	}
	note := strconv.Itoa(int(tag.RowsAffected())) + " 枚"
	if err := s.recordAudit(ctx, principal.User.ID, "voting.void", "vote", target,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
