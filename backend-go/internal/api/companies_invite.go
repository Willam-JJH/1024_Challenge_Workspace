package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	invitationsPattern     = "GET /api/v1/companies/{company_id}/invitations"
	invitePattern          = "POST /api/v1/companies/{company_id}/invitations"
	revokeInvitePattern    = "DELETE /api/v1/companies/{company_id}/invitations/{invitation_id}"
	redeliverInvitePattern = "POST /api/v1/companies/{company_id}/invitations/{invitation_id}/deliveries"
	inviteLinkPattern      = "POST /api/v1/companies/{company_id}/invite-links"
	disableLinksPattern    = "DELETE /api/v1/companies/{company_id}/invite-links"
	joinRequestsPattern    = "GET /api/v1/companies/{company_id}/join-requests"
	joinRequestPattern     = "POST /api/v1/companies/{company_id}/join-requests"
	acceptJoinPattern      = "POST /api/v1/companies/{company_id}/join-requests/{request_id}/acceptance"
	declineJoinPattern     = "POST /api/v1/companies/{company_id}/join-requests/{request_id}/decline"
)

const invitationTTL = 14 * 24 * time.Hour

type invitationOut struct {
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt *string `json:"expires_at"`
}

type invitationCreatedOut struct {
	Invitation invitationOut `json:"invitation"`
	Token      string        `json:"token"`
}

type joinRequestOut struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	Message   *string `json:"message"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
}

type inviteLinkOut struct {
	Token     string  `json:"token"`
	ExpiresAt *string `json:"expires_at"`
}

func (s *Server) listInvitations(w http.ResponseWriter, r *http.Request) {
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
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, email, status, created_at, expires_at
		FROM invitations
		WHERE company_id = $1 AND status = 'pending'
		ORDER BY created_at DESC`, company.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]invitationOut, 0, 8)
	for rows.Next() {
		var item invitationOut
		var createdAt time.Time
		var expiresAt pgtype.Timestamp
		if err := rows.Scan(&item.ID, &item.Email, &item.Status, &createdAt,
			&expiresAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.CreatedAt = formatTime(createdAt)
		item.ExpiresAt = formatTimePtr(timestampPtr(expiresAt))
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// deliverInvitation 生成新令牌、续期并投递激活链接。
func (s *Server) deliverInvitation(ctx context.Context, invitationID, email,
	companyName string) (string, error) {
	token, err := auth.NewToken("")
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(invitationTTL)
	_, err = s.writePool().Exec(ctx, `
		UPDATE invitations SET token_hash = $2, expires_at = $3, status = 'pending',
			updated_at = $4 WHERE id = $1`,
		invitationID, auth.HashToken(token), expiresAt, now)
	if err != nil {
		return "", err
	}
	s.mailer.SendNotice(email, "邀请你加入 "+companyName,
		"用这个链接激活并加入：/auth/activations/"+token)
	return token, nil
}

func (s *Server) publicToken(token string) string {
	if s.cfg.Environment == "prod" {
		return ""
	}
	return token
}

type inviteMemberIn struct {
	Email string `json:"email"`
}

func (s *Server) inviteMember(w http.ResponseWriter, r *http.Request) {
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
	var in inviteMemberIn
	if !decodeJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()

	var userID string
	err := s.writePool().QueryRow(ctx,
		`SELECT id FROM users WHERE email = $1`, email).Scan(&userID)
	if err != nil && !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	if err == nil {
		role, err := s.membershipRole(ctx, company.ID, userID)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if role != "" {
			writeError(w, r, http.StatusConflict, "conflict", "该账号已在公司中")
			return
		}
		other, err := s.myCompanyID(ctx, userID)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if other != "" {
			writeError(w, r, http.StatusConflict, "conflict", "该账号已在其他公司中")
			return
		}
	}
	var pending bool
	if err := s.writePool().QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM invitations
			WHERE company_id = $1 AND email = $2 AND status = 'pending')`,
		company.ID, email).Scan(&pending); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if pending {
		writeError(w, r, http.StatusConflict, "conflict", "已有待接受的邀请")
		return
	}
	seats, err := s.companySeats(ctx, company.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if seats <= 0 {
		writeError(w, r, http.StatusConflict, "conflict", "公司人数已满")
		return
	}

	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx, `
		INSERT INTO invitations (id, company_id, email, token_hash, invited_by, status,
			created_at, updated_at)
		VALUES ($1, $2, $3, '', $4, 'pending', $5, $5)`,
		id, company.ID, email, principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	token, err := s.deliverInvitation(ctx, id, email, company.Name)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var expiresAt pgtype.Timestamp
	if err := s.writePool().QueryRow(ctx,
		`SELECT expires_at FROM invitations WHERE id = $1`, id).Scan(&expiresAt); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, invitationCreatedOut{
		Invitation: invitationOut{
			ID:        id,
			Email:     email,
			Status:    "pending",
			CreatedAt: formatTime(now),
			ExpiresAt: formatTimePtr(timestampPtr(expiresAt)),
		},
		Token: s.publicToken(token),
	})
}

func (s *Server) invitationForCaptain(w http.ResponseWriter, r *http.Request,
	principal *auth.Principal, companyID string) (string, bool) {
	var invitationCompany string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT company_id FROM invitations WHERE id = $1`,
		r.PathValue("invitation_id")).Scan(&invitationCompany)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "邀请不存在")
			return "", false
		}
		s.writeQueryError(w, r, err)
		return "", false
	}
	if invitationCompany != companyID {
		writeError(w, r, http.StatusNotFound, "not_found", "邀请不存在")
		return "", false
	}
	return r.PathValue("invitation_id"), true
}

func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
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
	id, ok := s.invitationForCaptain(w, r, principal, company.ID)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE invitations SET status = 'revoked', updated_at = $2 WHERE id = $1`,
		id, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) redeliverInvitation(w http.ResponseWriter, r *http.Request) {
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
	id, ok := s.invitationForCaptain(w, r, principal, company.ID)
	if !ok {
		return
	}
	ctx := r.Context()
	var email string
	var createdAt time.Time
	if err := s.writePool().QueryRow(ctx,
		`SELECT email, created_at FROM invitations WHERE id = $1`, id).
		Scan(&email, &createdAt); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	token, err := s.deliverInvitation(ctx, id, email, company.Name)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var expiresAt pgtype.Timestamp
	if err := s.writePool().QueryRow(ctx,
		`SELECT expires_at FROM invitations WHERE id = $1`, id).Scan(&expiresAt); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, invitationCreatedOut{
		Invitation: invitationOut{
			ID:        id,
			Email:     email,
			Status:    "pending",
			CreatedAt: formatTime(createdAt),
			ExpiresAt: formatTimePtr(timestampPtr(expiresAt)),
		},
		Token: s.publicToken(token),
	})
}

func (s *Server) createInviteLink(w http.ResponseWriter, r *http.Request) {
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
	token, err := auth.NewToken("")
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		INSERT INTO invite_links (id, company_id, token_hash, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		id, company.ID, auth.HashToken(token), principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, inviteLinkOut{Token: token, ExpiresAt: nil})
}

func (s *Server) disableInviteLinks(w http.ResponseWriter, r *http.Request) {
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
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE invite_links SET disabled_at = $2, updated_at = $2
		WHERE company_id = $1 AND disabled_at IS NULL`, company.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) listJoinRequests(w http.ResponseWriter, r *http.Request) {
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
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, user_id, message, status, created_at
		FROM join_requests
		WHERE company_id = $1 AND status = 'pending'
		ORDER BY created_at`, company.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]joinRequestOut, 0, 8)
	for rows.Next() {
		var item joinRequestOut
		var message pgtype.Text
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.UserID, &message, &item.Status,
			&createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.Message = textPtr(message)
		item.CreatedAt = formatTime(createdAt)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type joinRequestIn struct {
	Message *string `json:"message"`
}

func (s *Server) createJoinRequest(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	company, ok := s.companyByRef(w, r, r.PathValue("company_id"))
	if !ok {
		return
	}
	var in joinRequestIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Message != nil && len([]rune(*in.Message)) > 280 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	ctx := r.Context()
	var registrationOpen bool
	if err := s.writePool().QueryRow(ctx,
		`SELECT registration_open FROM events WHERE id = (SELECT event_id FROM companies WHERE id = $1)`,
		company.ID).Scan(&registrationOpen); err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusForbidden, "forbidden", "报名已关闭")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if !registrationOpen {
		writeError(w, r, http.StatusForbidden, "forbidden", "报名已关闭")
		return
	}
	if company.RosterLocked {
		writeError(w, r, http.StatusConflict, "conflict", "该公司名单已锁定")
		return
	}
	if existing, err := s.myCompanyID(ctx, principal.User.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	} else if existing != "" {
		writeError(w, r, http.StatusConflict, "conflict", "你已经在公司中")
		return
	}
	seats, err := s.companySeats(ctx, company.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if seats <= 0 {
		writeError(w, r, http.StatusConflict, "conflict", "公司人数已满")
		return
	}
	var pending bool
	if err := s.writePool().QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM join_requests
			WHERE company_id = $1 AND user_id = $2 AND status = 'pending')`,
		company.ID, principal.User.ID).Scan(&pending); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if pending {
		writeError(w, r, http.StatusConflict, "conflict", "你已经申请过这家公司")
		return
	}
	id, err := auth.NewID()
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(ctx, `
		INSERT INTO join_requests (id, company_id, user_id, message, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', $5, $5)`,
		id, company.ID, principal.User.ID, in.Message, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, joinRequestOut{
		ID:        id,
		UserID:    principal.User.ID,
		Message:   in.Message,
		Status:    "pending",
		CreatedAt: formatTime(now),
	})
}

func (s *Server) decideJoinRequest(w http.ResponseWriter, r *http.Request,
	accept bool) {
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
	ctx := r.Context()
	var userID, status, requestCompany string
	err := s.writePool().QueryRow(ctx,
		`SELECT user_id, status, company_id FROM join_requests WHERE id = $1`,
		r.PathValue("request_id")).Scan(&userID, &status, &requestCompany)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "申请不存在或已处理")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if requestCompany != company.ID || status != "pending" {
		writeError(w, r, http.StatusNotFound, "not_found", "申请不存在或已处理")
		return
	}
	now := time.Now().UTC()
	if accept {
		if company.RosterLocked {
			writeError(w, r, http.StatusConflict, "conflict", "名单已锁定")
			return
		}
		var totalSeats int
		if err := s.writePool().QueryRow(ctx, `
			SELECT e.company_seats FROM companies c JOIN events e ON e.id = c.event_id
			WHERE c.id = $1`, company.ID).Scan(&totalSeats); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if ok, err := s.addMember(w, r, userID, company.ID, totalSeats); err != nil || !ok {
			if err != nil {
				s.writeQueryError(w, r, err)
			}
			return
		}
	}
	newStatus := "declined"
	if accept {
		newStatus = "accepted"
	}
	if _, err := s.writePool().Exec(ctx,
		`UPDATE join_requests SET status = $2, updated_at = $3 WHERE id = $1`,
		r.PathValue("request_id"), newStatus, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) acceptJoinRequest(w http.ResponseWriter, r *http.Request) {
	s.decideJoinRequest(w, r, true)
}

func (s *Server) declineJoinRequest(w http.ResponseWriter, r *http.Request) {
	s.decideJoinRequest(w, r, false)
}
