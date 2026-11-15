package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	myProjectPattern      = "GET /api/v1/projects/mine"
	draftSchemaPattern    = "GET /api/v1/projects/draft-schema"
	projectGetPattern     = "GET /api/v1/projects/{project_id}"
	editSessionPattern    = "POST /api/v1/projects/{project_id}/edit-sessions"
	heartbeatPattern      = "POST /api/v1/projects/{project_id}/edit-sessions/{session_id}/heartbeats"
	releaseLockPattern    = "DELETE /api/v1/projects/{project_id}/edit-sessions/{session_id}"
	saveDraftPattern      = "PATCH /api/v1/projects/{project_id}/draft"
	publishPattern        = "POST /api/v1/projects/{project_id}/publications"
	versionsPattern       = "GET /api/v1/projects/{project_id}/versions"
	versionPattern        = "GET /api/v1/projects/{project_id}/versions/{version}"
	restoreVersionPattern = "POST /api/v1/projects/{project_id}/versions/{version}/restorations"
	attendanceGetPattern  = "GET /api/v1/projects/{project_id}/attendance"
	attendancePutPattern  = "PUT /api/v1/projects/{project_id}/attendance"
	agentApprovalPattern  = "POST /api/v1/projects/{project_id}/agent-draft-approvals"
)

const lockTTL = 2 * time.Minute

type projectRow struct {
	ID            string
	CompanyID     string
	EventID       string
	Status        string
	Title         string
	Summary       string
	Content       string
	Revision      int
	LiveVersion   pgtype.Int4
	Attendance    pgtype.Text
	AgentReviewed bool
}

type projectOut struct {
	ID          string `json:"id"`
	CompanyID   string `json:"company_id"`
	Status      string `json:"status"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	Content     string `json:"content"`
	Revision    int    `json:"revision"`
	LiveVersion *int   `json:"live_version"`
}

func (p *projectRow) out() projectOut {
	out := projectOut{
		ID:        p.ID,
		CompanyID: p.CompanyID,
		Status:    p.Status,
		Title:     p.Title,
		Summary:   p.Summary,
		Content:   p.Content,
		Revision:  p.Revision,
	}
	if p.LiveVersion.Valid {
		value := int(p.LiveVersion.Int32)
		out.LiveVersion = &value
	}
	return out
}

type versionOut struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	PublishedAt string `json:"published_at"`
}

type draftSavedOut struct {
	Revision  int    `json:"revision"`
	UpdatedAt string `json:"updated_at"`
}

type editSessionOut struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type publishOut struct {
	Project projectOut `json:"project"`
	Version versionOut `json:"version"`
}

var draftSchema = map[string]any{
	"type":     "object",
	"required": []string{"title"},
	"properties": map[string]any{
		"title":   map[string]any{"type": "string", "maxLength": 160},
		"summary": map[string]any{"type": "string", "maxLength": 2000},
		"content": map[string]any{"type": "string", "maxLength": 200000, "description": "Markdown"},
	},
}

func (s *Server) projectByID(ctx context.Context, id string) (*projectRow, error) {
	var project projectRow
	err := s.writePool().QueryRow(ctx, `
		SELECT id, company_id, event_id, status, title, summary, content, revision,
			live_version, attendance, agent_reviewed
		FROM projects WHERE id = $1`, id).
		Scan(&project.ID, &project.CompanyID, &project.EventID, &project.Status,
			&project.Title, &project.Summary, &project.Content, &project.Revision,
			&project.LiveVersion, &project.Attendance, &project.AgentReviewed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &project, nil
}

func (s *Server) requireProject(w http.ResponseWriter, r *http.Request) (*projectRow, bool) {
	project, err := s.projectByID(r.Context(), r.PathValue("project_id"))
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	if project == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "作品不存在")
		return nil, false
	}
	return project, true
}

// membershipOf 返回用户在公司里的角色；不在公司返回空串。
func (s *Server) membershipRole(ctx context.Context, companyID, userID string) (string, error) {
	var role string
	err := s.writePool().QueryRow(ctx,
		`SELECT role FROM memberships WHERE company_id = $1 AND user_id = $2`,
		companyID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (s *Server) requireReader(w http.ResponseWriter, r *http.Request,
	project *projectRow, principal *auth.Principal) bool {
	if principal.User.Role == "staff" || principal.User.Role == "admin" {
		return true
	}
	role, err := s.membershipRole(r.Context(), project.CompanyID, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	if role == "" {
		writeError(w, r, http.StatusForbidden, "forbidden", "不是该公司成员")
		return false
	}
	return true
}

func (s *Server) requireEditor(w http.ResponseWriter, r *http.Request,
	project *projectRow, principal *auth.Principal) bool {
	if principal.User.Role == "staff" || principal.User.Role == "admin" {
		return true
	}
	role, err := s.membershipRole(r.Context(), project.CompanyID, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	if role == "" {
		writeError(w, r, http.StatusForbidden, "forbidden", "不是该公司成员")
		return false
	}
	if role != "captain" && role != "submitter" {
		writeError(w, r, http.StatusForbidden, "forbidden", "只有队长或提交人可以编辑作品")
		return false
	}
	return true
}

func (s *Server) myProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var companyID string
	err := s.writePool().QueryRow(ctx,
		`SELECT company_id FROM memberships WHERE user_id = $1 LIMIT 1`,
		principal.User.ID).Scan(&companyID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "not_found", "你还没有公司，先组建或加入一家")
		return
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var projectID string
	err = s.writePool().QueryRow(ctx,
		`SELECT id FROM projects WHERE company_id = $1`, companyID).Scan(&projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		var eventID string
		if err := s.writePool().QueryRow(ctx,
			`SELECT event_id FROM companies WHERE id = $1`, companyID).Scan(&eventID); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		id, err := auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		now := time.Now().UTC()
		_, err = s.writePool().Exec(ctx, `
			INSERT INTO projects (id, event_id, company_id, status, title, summary, content,
				revision, agent_reviewed, created_at, updated_at)
			VALUES ($1, $2, $3, 'draft', '', '', '', 0, false, $4, $4)`,
			id, eventID, companyID, now)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		projectID = id
	} else if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	project, err := s.projectByID(ctx, projectID)
	if err != nil || project == nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, project.out())
}

func (s *Server) draftSchema(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, draftSchema)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireReader(w, r, project, principal) {
		return
	}
	writeJSON(w, http.StatusOK, project.out())
}

type editSessionIn struct {
	TakeOver bool `json:"take_over"`
}

func (s *Server) acquireEditSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireEditor(w, r, project, principal) {
		return
	}
	var in editSessionIn
	if !decodeJSON(w, r, &in) {
		return
	}

	ctx := r.Context()
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 锁住作品行，让两个并发请求串行：后到的那个会读到刚提交的会话并返回 409
	if _, err := tx.Exec(ctx,
		`SELECT 1 FROM projects WHERE id = $1 FOR UPDATE`, project.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	now := time.Now().UTC()
	if _, err := tx.Exec(ctx,
		`DELETE FROM project_edit_sessions WHERE project_id = $1 AND expires_at < $2`,
		project.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var activeID, activeUser string
	err = tx.QueryRow(ctx,
		`SELECT id, user_id FROM project_edit_sessions WHERE project_id = $1`,
		project.ID).Scan(&activeID, &activeUser)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.writeQueryError(w, r, err)
		return
	}
	if err == nil {
		if activeUser != principal.User.ID && !in.TakeOver {
			writeError(w, r, http.StatusConflict, "conflict",
				"其他标签页正在编辑，请稍后再试或选择接管")
			return
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM project_edit_sessions WHERE id = $1`, activeID); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}

	token, err := auth.NewToken("")
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	sessionID, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	expiresAt := now.Add(lockTTL)
	_, err = tx.Exec(ctx, `
		INSERT INTO project_edit_sessions
			(id, project_id, user_id, token_hash, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		sessionID, project.ID, principal.User.ID, auth.HashToken(token), expiresAt, now)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, editSessionOut{
		ID:        sessionID,
		Token:     token,
		ExpiresAt: formatTime(expiresAt),
	})
}

type lockRow struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
}

// ensureLock 复刻 Python 的 _ensure_lock，错误信息保持一致。
func (s *Server) ensureLock(w http.ResponseWriter, r *http.Request,
	projectID string) (*lockRow, bool) {
	token := r.Header.Get("X-Edit-Token")
	if token == "" {
		writeError(w, r, http.StatusConflict, "conflict", "缺少编辑锁，请先获取")
		return nil, false
	}
	var session lockRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, user_id, expires_at FROM project_edit_sessions
		WHERE project_id = $1 AND token_hash = $2`, projectID, auth.HashToken(token)).
		Scan(&session.ID, &session.UserID, &session.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, http.StatusConflict, "conflict", "编辑锁无效")
		return nil, false
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	if session.ExpiresAt.Before(time.Now().UTC()) {
		writeError(w, r, http.StatusConflict, "conflict", "编辑锁已过期，请重新获取")
		return nil, false
	}
	return &session, true
}

func (s *Server) heartbeatLock(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireEditor(w, r, project, principal) {
		return
	}
	session, ok := s.ensureLock(w, r, project.ID)
	if !ok {
		return
	}
	if session.ID != r.PathValue("session_id") || session.UserID != principal.User.ID {
		writeError(w, r, http.StatusConflict, "conflict", "编辑锁不属于这个会话")
		return
	}
	expiresAt := time.Now().UTC().Add(lockTTL)
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE project_edit_sessions SET expires_at = $2, updated_at = $3 WHERE id = $1`,
		session.ID, expiresAt, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, editSessionOut{
		ID:        session.ID,
		Token:     r.Header.Get("X-Edit-Token"),
		ExpiresAt: formatTime(expiresAt),
	})
}

func (s *Server) releaseLock(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	var ownerID, projectID string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT user_id, project_id FROM project_edit_sessions WHERE id = $1`,
		r.PathValue("session_id")).Scan(&ownerID, &projectID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.writeQueryError(w, r, err)
		return
	}
	if err == nil && projectID == project.ID &&
		(ownerID == principal.User.ID || principal.User.Role == "staff" ||
			principal.User.Role == "admin") {
		if _, err := s.writePool().Exec(r.Context(),
			`DELETE FROM project_edit_sessions WHERE id = $1`,
			r.PathValue("session_id")); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type projectDraftIn struct {
	BaseRevision int    `json:"base_revision"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	Content      string `json:"content"`
}

func (s *Server) saveDraft(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireEditor(w, r, project, principal) {
		return
	}
	if _, ok := s.ensureLock(w, r, project.ID); !ok {
		return
	}
	var in projectDraftIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.BaseRevision < 0 || len([]rune(in.Title)) > 160 || len([]rune(in.Summary)) > 2000 ||
		len([]rune(in.Content)) > 200000 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if in.BaseRevision != project.Revision {
		writeError(w, r, http.StatusConflict, "conflict", "修订号已过期，请重新拉取草稿")
		return
	}
	now := time.Now().UTC()
	_, err := s.writePool().Exec(r.Context(), `
		UPDATE projects
		SET title = $2, summary = $3, content = $4, revision = revision + 1, updated_at = $5
		WHERE id = $1`, project.ID, in.Title, in.Summary, in.Content, now)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, draftSavedOut{Revision: project.Revision + 1, UpdatedAt: formatTime(now)})
}

func (s *Server) publishProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireEditor(w, r, project, principal) {
		return
	}
	if strings.TrimSpace(project.Title) == "" {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "标题不能为空")
		return
	}

	ctx := r.Context()
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var nextVersion int
	if err := tx.QueryRow(ctx,
		`SELECT coalesce(max(version), 0) + 1 FROM project_versions WHERE project_id = $1`,
		project.ID).Scan(&nextVersion); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	versionID, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `
		INSERT INTO project_versions
			(id, project_id, version, title, summary, content, published_at, published_by,
			 created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $7, $7)`,
		versionID, project.ID, nextVersion, project.Title, project.Summary, project.Content,
		now, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE projects SET status = 'published', live_version = $2, updated_at = $3
		WHERE id = $1`, project.ID, nextVersion, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	project.Status = "published"
	project.LiveVersion = pgtype.Int4{Int32: int32(nextVersion), Valid: true}
	writeJSON(w, http.StatusOK, publishOut{
		Project: project.out(),
		Version: versionOut{
			ID:          versionID,
			Version:     nextVersion,
			Title:       project.Title,
			Summary:     project.Summary,
			PublishedAt: formatTime(now),
		},
	})
}

func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireReader(w, r, project, principal) {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, version, title, summary, published_at
		FROM project_versions WHERE project_id = $1
		ORDER BY version DESC`, project.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	versions := make([]versionOut, 0, 8)
	for rows.Next() {
		var version versionOut
		var publishedAt time.Time
		if err := rows.Scan(&version.ID, &version.Version, &version.Title, &version.Summary,
			&publishedAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		version.PublishedAt = formatTime(publishedAt)
		versions = append(versions, version)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireReader(w, r, project, principal) {
		return
	}
	number, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "版本不存在")
		return
	}
	var version versionOut
	var publishedAt time.Time
	err = s.writePool().QueryRow(r.Context(), `
		SELECT id, version, title, summary, published_at
		FROM project_versions WHERE project_id = $1 AND version = $2`,
		project.ID, number).Scan(&version.ID, &version.Version, &version.Title,
		&version.Summary, &publishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "not_found", "版本不存在")
		return
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	version.PublishedAt = formatTime(publishedAt)
	writeJSON(w, http.StatusOK, version)
}

func (s *Server) restoreVersion(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireEditor(w, r, project, principal) {
		return
	}
	number, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "版本不存在")
		return
	}
	var title, summary, content string
	err = s.writePool().QueryRow(r.Context(), `
		SELECT title, summary, content FROM project_versions
		WHERE project_id = $1 AND version = $2`, project.ID, number).
		Scan(&title, &summary, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, r, http.StatusNotFound, "not_found", "版本不存在")
		return
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	now := time.Now().UTC()
	_, err = s.writePool().Exec(r.Context(), `
		UPDATE projects
		SET title = $2, summary = $3, content = $4, revision = revision + 1, updated_at = $5
		WHERE id = $1`, project.ID, title, summary, content, now)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, draftSavedOut{Revision: project.Revision + 1, UpdatedAt: formatTime(now)})
}

type attendanceOut struct {
	Attendance *string `json:"attendance"`
}

func (s *Server) getAttendance(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireReader(w, r, project, principal) {
		return
	}
	writeJSON(w, http.StatusOK, attendanceOut{Attendance: textPtr(project.Attendance)})
}

type attendanceIn struct {
	Attendance string `json:"attendance"`
}

func (s *Server) setAttendance(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireEditor(w, r, project, principal) {
		return
	}
	var in attendanceIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Attendance != "yes" && in.Attendance != "no" && in.Attendance != "maybe" {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE projects SET attendance = $2, updated_at = $3 WHERE id = $1`,
		project.ID, in.Attendance, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, attendanceOut{Attendance: &in.Attendance})
}

func (s *Server) approveAgentDraft(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	if !s.requireEditor(w, r, project, principal) {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE projects SET agent_reviewed = true, updated_at = $2 WHERE id = $1`,
		project.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
