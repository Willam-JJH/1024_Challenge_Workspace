package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"time"

	"challenge-api/internal/auth"
)

const (
	adminCreateUserPattern   = "POST /api/v1/admin/users"
	adminUserGetPattern      = "GET /api/v1/admin/users/{user_id}"
	adminUserTeamsPattern    = "GET /api/v1/admin/users/{user_id}/teams"
	adminUserRolePattern     = "PUT /api/v1/admin/users/{user_id}/role"
	adminUserModePattern     = "PUT /api/v1/admin/users/{user_id}/mode"
	adminUserLocalePattern   = "PUT /api/v1/admin/users/{user_id}/locale"
	adminUserSuspendPattern  = "POST /api/v1/admin/users/{user_id}/suspensions"
	adminUserResumePattern   = "DELETE /api/v1/admin/users/{user_id}/suspensions"
	adminUserSignOutPattern  = "POST /api/v1/admin/users/{user_id}/sign-outs"
	adminUserResendPattern   = "POST /api/v1/admin/users/{user_id}/activation-deliveries"
	adminUserDeletePattern   = "DELETE /api/v1/admin/users/{user_id}"
	adminBulkRolePattern     = "PUT /api/v1/admin/users/roles"
	adminBulkLocalePattern   = "PUT /api/v1/admin/users/locales"
	adminBulkDeletePattern   = "DELETE /api/v1/admin/users"
	adminExportCSVPattern    = "GET /api/v1/admin/users/export.csv"
)

var adminRoles = map[string]bool{
	"participant": true, "member": true, "captain": true, "staff": true, "admin": true,
}

var adminModes = map[string]bool{"online": true, "in_person": true, "both": true}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (*auth.Principal, bool) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return nil, false
	}
	if principal.User.Role != "admin" {
		writeError(w, r, http.StatusForbidden, "forbidden", "需要管理员权限")
		return nil, false
	}
	return principal, true
}

// recordAudit 与 Python 的 services/audit.record 对齐。
func (s *Server) recordAudit(ctx context.Context, actorID, action, targetType, targetID string,
	note *string) error {
	id, err := auth.NewID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = s.writePool().Exec(ctx, `
		INSERT INTO audit_log (id, actor_id, action, target_type, target_id, note,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		id, actorID, action, targetType, targetID, note, now)
	return err
}

func (s *Server) adminUserBadge(ctx context.Context, userID string) (*adminUserRow, error) {
	var row adminUserRow
	var createdAt time.Time
	err := s.writePool().QueryRow(ctx, `
		SELECT id, email, display_name, role, status, mode, locale, created_at
		FROM users WHERE id = $1`, userID).
		Scan(&row.ID, &row.Email, &row.DisplayName, &row.Role, &row.Status, &row.Mode,
			&row.Locale, &createdAt)
	if err != nil {
		return nil, err
	}
	row.CreatedAt = formatTime(createdAt)
	return &row, nil
}

type adminUserDetail struct {
	adminUserRow
	Memberships []membershipBrief `json:"memberships"`
}

type membershipBrief struct {
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	Role        string `json:"role"`
}

func (s *Server) adminUserOr404(w http.ResponseWriter, r *http.Request,
	userID string) bool {
	var exists bool
	if err := s.writePool().QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists); err != nil {
		s.writeQueryError(w, r, err)
		return false
	}
	if !exists {
		writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
		return false
	}
	return true
}

func (s *Server) adminGetUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	ctx := r.Context()
	userID := r.PathValue("user_id")
	user, err := s.adminUserBadge(ctx, userID)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	memberships, ok := s.adminUserTeams(w, r, userID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, adminUserDetail{adminUserRow: *user, Memberships: memberships})
}

func (s *Server) adminUserTeamsHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	if !s.adminUserOr404(w, r, r.PathValue("user_id")) {
		return
	}
	memberships, ok := s.adminUserTeams(w, r, r.PathValue("user_id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, memberships)
}

func (s *Server) adminUserTeams(w http.ResponseWriter, r *http.Request,
	userID string) ([]membershipBrief, bool) {
	rows, err := s.writePool().Query(r.Context(), `
		SELECT c.id, c.name, m.role
		FROM memberships m JOIN companies c ON c.id = m.company_id
		WHERE m.user_id = $1`, userID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	defer rows.Close()
	out := make([]membershipBrief, 0, 2)
	for rows.Next() {
		var item membershipBrief
		if err := rows.Scan(&item.CompanyID, &item.CompanyName, &item.Role); err != nil {
			s.writeQueryError(w, r, err)
			return nil, false
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return nil, false
	}
	return out, true
}

type createUserIn struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Locale      string `json:"locale"`
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in createUserIn
	if !decodeJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	locale := in.Locale
	if locale == "" {
		locale = "zh"
	}
	if !validEmail(email) || strings.TrimSpace(in.DisplayName) == "" ||
		len([]rune(in.DisplayName)) > 120 || len(locale) > 8 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	var exists bool
	if err := s.writePool().QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)`, email).Scan(&exists); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if exists {
		writeError(w, r, http.StatusConflict, "conflict", "该邮箱已存在")
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, display_name, locale, role, status, mode,
			llm_blocked, digest_comments, created_at, updated_at)
		VALUES ($1, $2, '!pending', $3, $4, 'participant', 'pending', 'both',
			false, true, $5, $5)`, id, email, in.DisplayName, locale, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	code, err := s.store.IssueCode(ctx, email, "register", now)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	s.mailer.SendCode(email, code, "register", locale)
	if err := s.recordAudit(ctx, principal.User.ID, "user.create", "user", id, nil); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	row := adminUserRow{ID: id, Email: email, DisplayName: in.DisplayName, Role: "participant",
		Status: "pending", Mode: "both", Locale: locale, CreatedAt: formatTime(now)}
	writeJSON(w, http.StatusCreated, row)
}

type roleIn struct {
	Role string `json:"role"`
}

func (s *Server) adminSetRole(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in roleIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !adminRoles[in.Role] {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	userID := r.PathValue("user_id")
	if userID == principal.User.ID {
		writeError(w, r, http.StatusForbidden, "forbidden", "不能修改自己的角色")
		return
	}
	if !s.adminUserOr404(w, r, userID) {
		return
	}
	if in.Role == "admin" && principal.User.Role != "admin" {
		writeError(w, r, http.StatusForbidden, "forbidden", "只有管理员可以授予管理员角色")
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE users SET role = $2, updated_at = $3 WHERE id = $1`,
		userID, in.Role, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := in.Role
	if err := s.recordAudit(r.Context(), principal.User.ID, "user.role", "user", userID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type modeIn struct {
	Mode string `json:"mode"`
}

func (s *Server) adminSetMode(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in modeIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !adminModes[in.Mode] {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	userID := r.PathValue("user_id")
	if !s.adminUserOr404(w, r, userID) {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE users SET mode = $2, updated_at = $3 WHERE id = $1`,
		userID, in.Mode, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := in.Mode
	if err := s.recordAudit(r.Context(), principal.User.ID, "user.mode", "user", userID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type localeIn struct {
	Locale string `json:"locale"`
}

func (s *Server) adminSetLocale(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in localeIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.Locale) < 2 || len(in.Locale) > 8 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	userID := r.PathValue("user_id")
	if !s.adminUserOr404(w, r, userID) {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE users SET locale = $2, updated_at = $3 WHERE id = $1`,
		userID, in.Locale, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	note := in.Locale
	if err := s.recordAudit(r.Context(), principal.User.ID, "user.locale", "user", userID,
		&note); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminSuspend(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if userID == principal.User.ID {
		writeError(w, r, http.StatusForbidden, "forbidden", "不能停用自己")
		return
	}
	if !s.adminUserOr404(w, r, userID) {
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx,
		`UPDATE users SET status = 'suspended', updated_at = $2 WHERE id = $1`,
		userID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.store.RevokeAllSessions(ctx, userID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.recordAudit(ctx, principal.User.ID, "user.suspend", "user", userID, nil); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminResume(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if !s.adminUserOr404(w, r, userID) {
		return
	}
	ctx := r.Context()
	if _, err := s.writePool().Exec(ctx,
		`UPDATE users SET status = 'active', updated_at = $2 WHERE id = $1`,
		userID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.recordAudit(ctx, principal.User.ID, "user.unsuspend", "user", userID,
		nil); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminForceSignOut(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if !s.adminUserOr404(w, r, userID) {
		return
	}
	ctx := r.Context()
	if err := s.store.RevokeAllSessions(ctx, userID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.recordAudit(ctx, principal.User.ID, "user.sign_out", "user", userID,
		nil); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminResendActivation(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	ctx := r.Context()
	userID := r.PathValue("user_id")
	var email, locale, status string
	err := s.writePool().QueryRow(ctx,
		`SELECT email, locale, status FROM users WHERE id = $1`, userID).
		Scan(&email, &locale, &status)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if status != "pending" {
		writeError(w, r, http.StatusConflict, "conflict", "该账号不需要激活")
		return
	}
	code, err := s.store.IssueCode(ctx, email, "register", time.Now().UTC())
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	s.mailer.SendCode(email, code, "register", locale)
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if userID == principal.User.ID {
		writeError(w, r, http.StatusForbidden, "forbidden", "不能删除自己")
		return
	}
	ctx := r.Context()
	var email, role string
	err := s.writePool().QueryRow(ctx, `SELECT email, role FROM users WHERE id = $1`, userID).
		Scan(&email, &role)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if role == "admin" && principal.User.Role != "admin" {
		writeError(w, r, http.StatusForbidden, "forbidden", "只有管理员可以删除管理员")
		return
	}
	if err := s.recordAudit(ctx, principal.User.ID, "user.delete", "user", userID,
		&email); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type bulkIDsIn struct {
	IDs []string `json:"ids"`
}

type bulkRoleIn struct {
	IDs  []string `json:"ids"`
	Role string   `json:"role"`
}

type bulkLocaleIn struct {
	IDs    []string `json:"ids"`
	Locale string   `json:"locale"`
}

func validBulkIDs(ids []string) bool { return len(ids) >= 1 && len(ids) <= 500 }

func (s *Server) adminBulkRole(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireStaff(w, r)
	if !ok {
		return
	}
	var in bulkRoleIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validBulkIDs(in.IDs) || !adminRoles[in.Role] {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if in.Role == "admin" && principal.User.Role != "admin" {
		writeError(w, r, http.StatusForbidden, "forbidden", "只有管理员可以授予管理员角色")
		return
	}
	ctx := r.Context()
	note := in.Role
	for _, id := range in.IDs {
		tag, err := s.writePool().Exec(ctx,
			`UPDATE users SET role = $2, updated_at = $3 WHERE id = $1`,
			id, in.Role, time.Now().UTC())
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if err := s.recordAudit(ctx, principal.User.ID, "user.role", "user", id,
			&note); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminBulkLocale(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	var in bulkLocaleIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validBulkIDs(in.IDs) || len(in.Locale) < 2 || len(in.Locale) > 8 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	for _, id := range in.IDs {
		if _, err := s.writePool().Exec(r.Context(),
			`UPDATE users SET locale = $2, updated_at = $3 WHERE id = $1`,
			id, in.Locale, time.Now().UTC()); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminBulkDelete(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var in bulkIDsIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validBulkIDs(in.IDs) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	for _, id := range in.IDs {
		if id == principal.User.ID {
			continue
		}
		var email string
		err := s.writePool().QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, id).
			Scan(&email)
		if err != nil {
			if isNoRows(err) {
				continue
			}
			s.writeQueryError(w, r, err)
			return
		}
		if err := s.recordAudit(ctx, principal.User.ID, "user.delete", "user", id,
			&email); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if _, err := s.writePool().Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) adminExportCSV(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireStaff(w, r); !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT email, display_name, role, status, mode, locale, created_at
		FROM users ORDER BY created_at`)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()

	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	writer.UseCRLF = true
	_ = writer.Write([]string{"email", "display_name", "role", "status", "mode", "locale",
		"created_at"})
	for rows.Next() {
		var email, name, role, status, mode, locale string
		var createdAt time.Time
		if err := rows.Scan(&email, &name, &role, &status, &mode, &locale,
			&createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		_ = writer.Write([]string{email, name, role, status, mode, locale,
			formatTimeOffset(createdAt)})
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="users.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
}
