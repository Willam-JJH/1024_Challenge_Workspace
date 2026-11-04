package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"challenge-api/internal/auth"
)

const (
	createCompanyPattern    = "POST /api/v1/companies"
	updateCompanyPattern    = "PATCH /api/v1/companies/{company_id}"
	removeMemberPattern     = "DELETE /api/v1/companies/{company_id}/members/{user_id}"
	leaveCompanyPattern     = "POST /api/v1/companies/{company_id}/leavings"
	companyCaptainPattern   = "POST /api/v1/companies/{company_id}/captain-transfers"
	companySubmitterPattern = "POST /api/v1/companies/{company_id}/submitter-appointments"
	companyRosterPattern    = "PUT /api/v1/companies/{company_id}/roster-lock"
)

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	slug := strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
	if len(slug) > 60 {
		slug = slug[:60]
	}
	if slug == "" {
		return "company"
	}
	return slug
}

func (s *Server) requireMember(w http.ResponseWriter, r *http.Request,
	companyID string, principal *auth.Principal) (string, bool) {
	role, err := s.membershipRole(r.Context(), companyID, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return "", false
	}
	if role == "" {
		writeError(w, r, http.StatusForbidden, "forbidden", "不是该公司成员")
		return "", false
	}
	return role, true
}

func (s *Server) requireCaptain(w http.ResponseWriter, r *http.Request,
	companyID string, principal *auth.Principal) bool {
	role, ok := s.requireMember(w, r, companyID, principal)
	if !ok {
		return false
	}
	if role != "captain" {
		writeError(w, r, http.StatusForbidden, "forbidden", "需要队长权限")
		return false
	}
	return true
}

type companyCreateIn struct {
	Name  string  `json:"name"`
	Blurb *string `json:"blurb"`
}

// insertCompany 在一个事务里建公司、加队长成员并提升角色；
// slug 撞车时返回 conflict=true，让调用方换后缀重试。
func (s *Server) insertCompany(ctx context.Context, companyID, membershipID, eventID, slug,
	name string, blurb *string, userID string, now time.Time) (bool, error) {
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO companies (id, event_id, slug, name, blurb, captain_id, roster_locked,
			present, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, false, false, $7, $7)`,
		companyID, eventID, slug, name, blurb, userID, now); err != nil {
		if isUniqueViolation(err) {
			return true, nil
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (id, company_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'captain', $4, $4)`,
		membershipID, companyID, userID, now); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'captain', updated_at = $2 WHERE id = $1`,
		userID, now); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Server) createCompany(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in companyCreateIn
	if !decodeJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if len([]rune(name)) < 2 || len([]rune(name)) > 120 ||
		(in.Blurb != nil && len([]rune(*in.Blurb)) > 500) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	var eventID string
	var registrationOpen bool
	err := s.writePool().QueryRow(ctx,
		`SELECT id, registration_open FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).
		Scan(&eventID, &registrationOpen)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if !registrationOpen {
		writeError(w, r, http.StatusForbidden, "forbidden", "报名已关闭")
		return
	}
	if existing, err := s.myCompanyID(ctx, principal.User.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	} else if existing != "" {
		writeError(w, r, http.StatusConflict, "conflict", "你已经在公司中")
		return
	}

	base := slugify(name)
	now := time.Now().UTC()

	// 并发同名建队时 slug 可能撞车：重算后缀后重试，最多三次
	var companyID, slug string
	created := false
	for attempt := 0; attempt < 3 && !created; attempt++ {
		slug = base
		for n := 1; ; n++ {
			var taken int
			if err := s.writePool().QueryRow(ctx, `
				SELECT count(*) FROM companies WHERE event_id = $1 AND slug = $2`,
				eventID, slug).Scan(&taken); err != nil {
				s.writeQueryError(w, r, err)
				return
			}
			if taken == 0 {
				break
			}
			slug = base + "-" + strconv.Itoa(n+1)
		}

		companyID, err = auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		membershipID, err := auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		conflict, err := s.insertCompany(ctx, companyID, membershipID, eventID, slug, name,
			in.Blurb, principal.User.ID, now)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if conflict {
			continue
		}
		created = true
	}
	if !created {
		writeError(w, r, http.StatusConflict, "conflict", "并发创建冲突，请重试")
		return
	}
	s.writeCompanyDetail(w, r, companyBase{
		ID:        companyID,
		Slug:      slug,
		Name:      name,
		Blurb:     in.Blurb,
		CaptainID: &principal.User.ID,
	}, http.StatusCreated)
}

type companyUpdateIn struct {
	Name  *string `json:"name"`
	Blurb *string `json:"blurb"`
}

func (s *Server) updateCompany(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	company, ok := s.companyByRef(w, r, r.PathValue("company_id"))
	if !ok {
		return
	}
	if _, ok := s.requireMember(w, r, company.ID, principal); !ok {
		return
	}
	var in companyUpdateIn
	if !decodeJSON(w, r, &in) {
		return
	}
	name := company.Name
	blurb := company.Blurb
	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		if len([]rune(trimmed)) < 2 || len([]rune(trimmed)) > 120 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		name = trimmed
	}
	if in.Blurb != nil {
		if len([]rune(*in.Blurb)) > 500 {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
			return
		}
		blurb = in.Blurb
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE companies SET name = $2, blurb = $3, updated_at = $4 WHERE id = $1`,
		company.ID, name, blurb, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	company.Name = name
	company.Blurb = blurb
	s.writeCompanyDetail(w, r, company, http.StatusOK)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	company, ok := s.companyByRef(w, r, r.PathValue("company_id"))
	if !ok {
		return
	}
	if !s.requireCaptain(w, r, company.ID, principal) {
		return
	}
	if company.RosterLocked {
		writeError(w, r, http.StatusConflict, "conflict", "名单已锁定")
		return
	}
	targetID := r.PathValue("user_id")
	if targetID == principal.User.ID {
		writeError(w, r, http.StatusConflict, "conflict",
			"队长不能用这个接口移除自己，请先移交队长")
		return
	}
	role, err := s.membershipRole(r.Context(), company.ID, targetID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if role == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "该成员不在公司中")
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`DELETE FROM memberships WHERE company_id = $1 AND user_id = $2`,
		company.ID, targetID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'participant', updated_at = $2 WHERE id = $1`,
		targetID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) leaveCompany(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	company, ok := s.companyByRef(w, r, r.PathValue("company_id"))
	if !ok {
		return
	}
	role, ok := s.requireMember(w, r, company.ID, principal)
	if !ok {
		return
	}
	if company.RosterLocked {
		writeError(w, r, http.StatusConflict, "conflict", "名单已锁定")
		return
	}
	ctx := r.Context()
	if role == "captain" {
		var taken int
		if err := s.writePool().QueryRow(ctx,
			`SELECT count(*) FROM memberships WHERE company_id = $1`,
			company.ID).Scan(&taken); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if taken > 1 {
			writeError(w, r, http.StatusConflict, "conflict", "请先移交队长再退出")
			return
		}
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx,
		`DELETE FROM memberships WHERE company_id = $1 AND user_id = $2`,
		company.ID, principal.User.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(ctx,
		`UPDATE users SET role = 'participant', updated_at = $2 WHERE id = $1`,
		principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) companyTransferCaptain(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	company, ok := s.companyByRef(w, r, r.PathValue("company_id"))
	if !ok {
		return
	}
	if !s.requireCaptain(w, r, company.ID, principal) {
		return
	}
	var in transferIn
	if !decodeJSON(w, r, &in) {
		return
	}
	role, err := s.membershipRole(r.Context(), company.ID, in.UserID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if role == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "对方不是公司成员")
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	tx, err := s.writePool().Begin(ctx)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`UPDATE memberships SET role = 'member', updated_at = $3
		 WHERE company_id = $1 AND user_id = $2`, company.ID, principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE memberships SET role = 'captain', updated_at = $3
		 WHERE company_id = $1 AND user_id = $2`, company.ID, in.UserID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE companies SET captain_id = $2, updated_at = $3 WHERE id = $1`,
		company.ID, in.UserID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'captain', updated_at = $2 WHERE id = $1`,
		in.UserID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'member', updated_at = $2 WHERE id = $1`,
		principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) companyAppointSubmitter(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	company, ok := s.companyByRef(w, r, r.PathValue("company_id"))
	if !ok {
		return
	}
	if !s.requireCaptain(w, r, company.ID, principal) {
		return
	}
	var in transferIn
	if !decodeJSON(w, r, &in) {
		return
	}
	role, err := s.membershipRole(r.Context(), company.ID, in.UserID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if role == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "对方不是公司成员")
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx,
		`UPDATE memberships SET role = 'member', updated_at = $2
		 WHERE company_id = $1 AND role = 'submitter'`, company.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(ctx,
		`UPDATE memberships SET role = 'submitter', updated_at = $3
		 WHERE company_id = $1 AND user_id = $2`, company.ID, in.UserID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) companyRosterLock(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	company, ok := s.companyByRef(w, r, r.PathValue("company_id"))
	if !ok {
		return
	}
	if principal.User.Role != "staff" && principal.User.Role != "admin" {
		if !s.requireCaptain(w, r, company.ID, principal) {
			return
		}
	}
	var in rosterLockIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE companies SET roster_locked = $2, updated_at = $3 WHERE id = $1`,
		company.ID, in.Locked, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
