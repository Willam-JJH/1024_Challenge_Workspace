package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	adminDashboardPattern    = "GET /api/v1/admin/dashboard"
	adminUsersPattern        = "GET /api/v1/admin/users"
	adminAuditPattern        = "GET /api/v1/admin/audit"
	adminAuditActionsPattern = "GET /api/v1/admin/audit/actions"
	adminEventPattern        = "GET /api/v1/admin/event"
)

var roleRank = map[string]int{"participant": 0, "member": 1, "captain": 2, "staff": 3, "admin": 4}

func (s *Server) requireStaff(w http.ResponseWriter, r *http.Request) (*auth.Principal, bool) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return nil, false
	}
	if roleRank[principal.User.Role] < roleRank["staff"] {
		writeError(w, r, http.StatusForbidden, "forbidden", "需要组织者权限")
		return nil, false
	}
	return principal, true
}

type dashboardOut struct {
	Users             int `json:"users"`
	Companies         int `json:"companies"`
	ProjectsPublished int `json:"projects_published"`
	ProjectsDraft     int `json:"projects_draft"`
	Votes             int `json:"votes"`
	Comments          int `json:"comments"`
	OfflineSessions   int `json:"offline_sessions"`
}

func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	eventID, err := s.currentEventID(r)
	if err != nil {
		if err == errNoCurrentEvent {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	var out dashboardOut
	err = s.readPool().QueryRow(r.Context(), `
		SELECT
			(SELECT count(*) FROM users WHERE status = 'active'),
			(SELECT count(*) FROM companies WHERE event_id = $1),
			(SELECT count(*) FROM projects WHERE status = 'published'),
			(SELECT count(*) FROM projects WHERE status = 'draft'),
			(SELECT count(*) FROM votes WHERE event_id = $1 AND status = 'live'),
			(SELECT count(*) FROM comments WHERE status = 'visible'),
			(SELECT count(*) FROM offline_sessions WHERE event_id = $1)`, eventID).
		Scan(&out.Users, &out.Companies, &out.ProjectsPublished, &out.ProjectsDraft,
			&out.Votes, &out.Comments, &out.OfflineSessions)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) currentEventID(r *http.Request) (string, error) {
	pool := s.readPool()
	if pool == nil {
		return "", errInternal
	}
	var id string
	err := pool.QueryRow(r.Context(),
		`SELECT id FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).Scan(&id)
	if err != nil {
		if isNoRows(err) {
			return "", errNoCurrentEvent
		}
		return "", err
	}
	return id, nil
}

type adminUserRow struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Mode        string `json:"mode"`
	Locale      string `json:"locale"`
	CreatedAt   string `json:"created_at"`
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		limit = parsed
	}
	query := r.URL.Query().Get("q")
	if len([]rune(query)) > 120 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}

	rows, err := s.readPool().Query(r.Context(), `
		SELECT id, email, display_name, role, status, mode, locale, created_at
		FROM users
		WHERE ($1 = '' OR lower(email) LIKE $2 OR lower(display_name) LIKE $2)
		ORDER BY created_at DESC
		LIMIT $3`, query, "%"+strings.ToLower(query)+"%", limit)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	users := make([]adminUserRow, 0, limit)
	for rows.Next() {
		var user adminUserRow
		var createdAt time.Time
		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.Role,
			&user.Status, &user.Mode, &user.Locale, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		user.CreatedAt = formatTime(createdAt)
		users = append(users, user)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type auditOut struct {
	ID         string  `json:"id"`
	ActorID    *string `json:"actor_id"`
	Action     string  `json:"action"`
	TargetType string  `json:"target_type"`
	TargetID   *string `json:"target_id"`
	Note       *string `json:"note"`
	CreatedAt  string  `json:"created_at"`
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		limit = parsed
	}
	rows, err := s.readPool().Query(r.Context(), `
		SELECT id, actor_id, action, target_type, target_id, note, created_at
		FROM audit_log
		ORDER BY created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	entries := make([]auditOut, 0, limit)
	for rows.Next() {
		var entry auditOut
		var actor, target, note pgtype.Text
		var createdAt time.Time
		if err := rows.Scan(&entry.ID, &actor, &entry.Action, &entry.TargetType, &target,
			&note, &createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		entry.ActorID = textPtr(actor)
		entry.TargetID = textPtr(target)
		entry.Note = textPtr(note)
		entry.CreatedAt = formatTime(createdAt)
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) adminAuditActions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	rows, err := s.readPool().Query(r.Context(),
		`SELECT DISTINCT action FROM audit_log ORDER BY action`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	actions := make([]string, 0, 16)
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		actions = append(actions, action)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, actions)
}

func (s *Server) adminEvent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
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
