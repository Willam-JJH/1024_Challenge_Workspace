package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	changePasswordPattern     = "PUT /api/v1/auth/password"
	resetRequestPattern       = "POST /api/v1/auth/password-reset-requests"
	resetGrantPattern         = "POST /api/v1/auth/password-reset-grants"
	resetPattern              = "POST /api/v1/auth/password-resets"
	registerPattern           = "POST /api/v1/auth/registrations"
	resendRegistrationPattern = "POST /api/v1/auth/registrations/resend"
	verifyRegistrationPattern = "POST /api/v1/auth/registrations/verifications"
	activationPreviewPattern  = "GET /api/v1/auth/activations/{token}"
	activatePattern           = "POST /api/v1/auth/activations"
	botChallengePattern       = "POST /api/v1/auth/bot-challenges"
	sessionsPattern           = "GET /api/v1/auth/sessions"
	revokeSessionPattern      = "DELETE /api/v1/auth/sessions/{session_id}"
)

type codeSentOut struct {
	Status    string `json:"status"`
	ExpiresIn int    `json:"expires_in"`
}

type resetGrantOut struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

type botChallengeOut struct {
	Challenge  string `json:"challenge"`
	Difficulty int    `json:"difficulty"`
	ExpiresAt  int64  `json:"expires_at"`
}

type activationPreviewOut struct {
	Email       string `json:"email"`
	CompanyName string `json:"company_name"`
	EventName   string `json:"event_name"`
}

type sessionOut struct {
	ID         string  `json:"id"`
	UserAgent  *string `json:"user_agent"`
	ClientIP   *string `json:"client_ip"`
	LastSeenAt string  `json:"last_seen_at"`
	CreatedAt  string  `json:"created_at"`
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(dst); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return false
	}
	return true
}

func validEmail(email string) bool {
	return email != "" && len(email) <= 320 && strings.Contains(email, "@") &&
		!strings.ContainsAny(email, " \t\r\n")
}

func validPassword(password string) bool { return len(password) >= 8 && len(password) <= 200 }

// writeProblem 把业务层 Problem 映射成错误信封，其余错误按 500 处理。
func (s *Server) writeProblem(w http.ResponseWriter, r *http.Request, err error) {
	var problem *auth.Problem
	if errors.As(err, &problem) {
		writeError(w, r, problem.Status, problem.Code, problem.Message)
		return
	}
	s.writeQueryError(w, r, err)
}

func (s *Server) botChallenge(w http.ResponseWriter, r *http.Request) {
	challenge, difficulty, expiresAt, err := auth.IssueBotChallenge(
		s.cfg.BotCheckSecret, time.Now().UTC())
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	writeJSON(w, http.StatusOK, botChallengeOut{
		Challenge:  challenge,
		Difficulty: difficulty,
		ExpiresAt:  expiresAt,
	})
}

type passwordChangeIn struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in passwordChangeIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validPassword(in.CurrentPassword) || !validPassword(in.NewPassword) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if !auth.VerifyPassword(in.CurrentPassword, principal.User.PasswordHash) {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "当前口令不正确")
		return
	}
	if auth.VerifyPassword(in.NewPassword, principal.User.PasswordHash) {
		writeError(w, r, http.StatusConflict, "conflict", "新口令不能与旧口令相同")
		return
	}
	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	now := time.Now().UTC()
	if err := s.store.SetUserPassword(r.Context(), principal.User.ID, hash, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.store.RevokeOtherSessions(r.Context(), principal.User.ID,
		principal.SessionID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type resetRequestIn struct {
	Email string `json:"email"`
}

func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in resetRequestIn
	if !decodeJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	// 先按邮箱限流：不论邮箱是否存在都计数，避免用 429 的有无判断账号状态
	if !s.allowCodeSend(w, r, email) {
		return
	}
	user, err := s.store.UserByEmail(r.Context(), email)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if user != nil {
		code, err := s.store.IssueCode(r.Context(), user.Email, "password_reset", time.Now().UTC())
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if err := s.mailer.SendCode(user.Email, code, "password_reset", user.Locale); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	// 不透露邮箱是否存在。
	writeJSON(w, http.StatusOK, codeSentOut{
		Status:    "code_sent",
		ExpiresIn: int(auth.CodeTTL.Seconds()),
	})
}

type resetGrantIn struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func (s *Server) grantPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in resetGrantIn
	if !decodeJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) || len(in.Code) < 4 || len(in.Code) > 12 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if !s.allowVerify(w, r, email) {
		return
	}
	now := time.Now().UTC()
	if err := s.store.ConsumeCode(r.Context(), email, "password_reset", in.Code, now); err != nil {
		s.writeProblem(w, r, err)
		return
	}
	token, err := s.store.IssueGrant(r.Context(), email, "reset_grant", now)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resetGrantOut{
		Token:     token,
		ExpiresIn: int(auth.GrantTTL.Seconds()),
	})
}

type resetPasswordIn struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in resetPasswordIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.Token) < 16 || !validPassword(in.NewPassword) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	now := time.Now().UTC()
	email, err := s.store.ConsumeGrant(r.Context(), in.Token, "reset_grant", now)
	if err != nil {
		s.writeProblem(w, r, err)
		return
	}
	user, err := s.store.UserByEmail(r.Context(), email)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if user == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
		return
	}
	hash, err := auth.HashPassword(in.NewPassword)
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	if err := s.store.SetUserPassword(r.Context(), user.ID, hash, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if user.Status == "pending" {
		if err := s.store.SetUserStatus(r.Context(), user.ID, "active", now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	if err := s.store.RevokeAllSessions(r.Context(), user.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type registerIn struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	DisplayName  string `json:"display_name"`
	Locale       string `json:"locale"`
	BotChallenge string `json:"bot_challenge"`
	BotNonce     string `json:"bot_nonce"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in registerIn
	if !decodeJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	name := strings.TrimSpace(in.DisplayName)
	locale := in.Locale
	if locale == "" {
		locale = "zh"
	}
	if !validEmail(email) || !validPassword(in.Password) || name == "" ||
		len([]rune(name)) > 120 || len(locale) > 8 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if s.cfg.RequireBotCheck {
		if in.BotChallenge == "" || in.BotNonce == "" {
			writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "缺少人机校验")
			return
		}
		if err := auth.VerifyBotChallenge(s.cfg.BotCheckSecret, in.BotChallenge,
			in.BotNonce, time.Now().UTC()); err != nil {
			s.writeProblem(w, r, err)
			return
		}
	}

	now := time.Now().UTC()
	user, err := s.store.UserByEmail(r.Context(), email)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if user != nil && user.Status == "active" {
		writeError(w, r, http.StatusConflict, "conflict", "该邮箱已注册，请直接登录")
		return
	}
	// 已注册的邮箱在上一行就返回了，这里限流只针对真要发码的请求
	if !s.allowCodeSend(w, r, email) {
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	if user == nil {
		user, err = s.store.CreateUser(r.Context(), email, hash, name, locale, "pending",
			"participant", now)
		if err != nil && isUniqueViolation(err) {
			// 并发注册同一邮箱：另一个请求先插入成功，改为更新已有 pending 账号
			user, err = s.store.UserByEmail(r.Context(), email)
			if err == nil && user != nil {
				err = s.store.UpdateRegistration(r.Context(), user.ID, hash, name, locale, now)
			}
		}
	} else {
		err = s.store.UpdateRegistration(r.Context(), user.ID, hash, name, locale, now)
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	code, err := s.store.IssueCode(r.Context(), email, "register", now)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if err := s.mailer.SendCode(email, code, "register", locale); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, codeSentOut{
		Status:    "code_sent",
		ExpiresIn: int(auth.CodeTTL.Seconds()),
	})
}

func (s *Server) resendRegistration(w http.ResponseWriter, r *http.Request) {
	var in resetRequestIn
	if !decodeJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	// 先按邮箱限流：不论邮箱是否存在都计数，避免用 429 的有无判断账号状态
	if !s.allowCodeSend(w, r, email) {
		return
	}
	user, err := s.store.UserByEmail(r.Context(), email)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if user != nil && user.Status == "pending" {
		code, err := s.store.IssueCode(r.Context(), user.Email, "register", time.Now().UTC())
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if err := s.mailer.SendCode(user.Email, code, "register", user.Locale); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, codeSentOut{
		Status:    "code_sent",
		ExpiresIn: int(auth.CodeTTL.Seconds()),
	})
}

type verifyRegistrationIn struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func (s *Server) verifyRegistration(w http.ResponseWriter, r *http.Request) {
	var in verifyRegistrationIn
	if !decodeJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !validEmail(email) || len(in.Code) < 4 || len(in.Code) > 12 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if !s.allowVerify(w, r, email) {
		return
	}
	now := time.Now().UTC()
	user, err := s.store.UserByEmail(r.Context(), email)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if user == nil {
		writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
		return
	}
	if err := s.store.ConsumeCode(r.Context(), user.Email, "register", in.Code, now); err != nil {
		s.writeProblem(w, r, err)
		return
	}
	if err := s.store.SetUserStatus(r.Context(), user.ID, "active", now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	user.Status = "active"
	response, err := s.signIn(w, r, user)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

type activationIn struct {
	Token       string `json:"token"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type invitationRow struct {
	ID          string
	Email       string
	CompanyID   string
	ExpiresAt   *time.Time
	CompanyName string
	EventName   string
	Seats       int
}

func (s *Server) invitationByToken(w http.ResponseWriter, r *http.Request,
	token string) (*invitationRow, bool) {
	pool := s.writePool()
	var invitation invitationRow
	var expiresAt pgtype.Timestamp
	err := pool.QueryRow(r.Context(), `
		SELECT i.id, i.email, i.company_id, i.expires_at, c.name, e.name, e.company_seats
		FROM invitations i
		JOIN companies c ON c.id = i.company_id
		JOIN events e ON e.id = c.event_id
		WHERE i.token_hash = $1 AND i.status = 'pending'
		LIMIT 1`, auth.HashToken(token)).
		Scan(&invitation.ID, &invitation.Email, &invitation.CompanyID, &expiresAt,
			&invitation.CompanyName, &invitation.EventName, &invitation.Seats)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "邀请无效或已使用")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	invitation.ExpiresAt = timestampPtr(expiresAt)
	if invitation.ExpiresAt != nil && invitation.ExpiresAt.Before(time.Now().UTC()) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable",
			"邀请已过期，请让队长重新发送")
		return nil, false
	}
	return &invitation, true
}

func (s *Server) previewActivation(w http.ResponseWriter, r *http.Request) {
	invitation, ok := s.invitationByToken(w, r, r.PathValue("token"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, activationPreviewOut{
		Email:       invitation.Email,
		CompanyName: invitation.CompanyName,
		EventName:   invitation.EventName,
	})
}

func (s *Server) activate(w http.ResponseWriter, r *http.Request) {
	var in activationIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.Token) < 16 || !validPassword(in.Password) ||
		len([]rune(in.DisplayName)) > 120 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	invitation, ok := s.invitationByToken(w, r, in.Token)
	if !ok {
		return
	}

	now := time.Now().UTC()
	user, err := s.store.UserByEmail(r.Context(), invitation.Email)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if user != nil && user.Status == "active" {
		writeError(w, r, http.StatusConflict, "conflict", "该邮箱已有账号，请登录后接受邀请")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		WriteError(w, r, errInternal)
		return
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if user == nil {
		if displayName == "" {
			displayName = strings.Split(invitation.Email, "@")[0]
		}
		user, err = s.store.CreateUser(r.Context(), invitation.Email, hash, displayName,
			"zh", "active", "participant", now)
	} else {
		if displayName == "" {
			displayName = user.DisplayName
		}
		err = s.store.UpdateRegistration(r.Context(), user.ID, hash, displayName,
			user.Locale, now)
		if err == nil {
			err = s.store.SetUserStatus(r.Context(), user.ID, "active", now)
		}
		if err == nil {
			user.DisplayName = displayName
			user.Status = "active"
		}
	}
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	if ok, err := s.addMember(w, r, user.ID, invitation.CompanyID,
		invitation.Seats); err != nil {
		s.writeQueryError(w, r, err)
		return
	} else if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE invitations SET status = 'accepted', updated_at = $2 WHERE id = $1`,
		invitation.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	response, err := s.signIn(w, r, user)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// addMember 复刻 Python 的 add_member：已属于其他公司或名额已满都返回 409。
func (s *Server) addMember(w http.ResponseWriter, r *http.Request,
	userID, companyID string, seats int) (bool, error) {
	pool := s.writePool()
	var existing string
	err := pool.QueryRow(r.Context(),
		`SELECT company_id FROM memberships WHERE user_id = $1 LIMIT 1`, userID).Scan(&existing)
	if err == nil {
		writeError(w, r, http.StatusConflict, "conflict", "该账号已在其他公司中")
		return false, nil
	}
	if !isNoRows(err) {
		return false, err
	}
	var taken int
	if err := pool.QueryRow(r.Context(),
		`SELECT count(*) FROM memberships WHERE company_id = $1`, companyID).Scan(&taken); err != nil {
		return false, err
	}
	if taken >= seats {
		writeError(w, r, http.StatusConflict, "conflict", "公司人数已满")
		return false, nil
	}
	id, err := auth.NewID()
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	if _, err := pool.Exec(r.Context(), `
		INSERT INTO memberships (id, company_id, user_id, role, created_at, updated_at)
		VALUES ($1, $2, $3, 'member', $4, $4)`, id, companyID, userID, now); err != nil {
		return false, err
	}
	// 与 Python 的 add_member 一致：旁观者入队后升级为 member。
	if _, err := pool.Exec(r.Context(), `
		UPDATE users SET role = 'member', updated_at = $2
		WHERE id = $1 AND role = 'participant'`, userID, now); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	rows, err := s.store.SessionsForUser(r.Context(), principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	out := make([]sessionOut, 0, len(rows))
	for _, row := range rows {
		out = append(out, sessionOut{
			ID:         row.ID,
			UserAgent:  row.UserAgent,
			ClientIP:   row.ClientIP,
			LastSeenAt: formatTime(row.LastSeenAt),
			CreatedAt:  formatTime(row.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	revoked, err := s.store.RevokeSessionOfUser(r.Context(), principal.User.ID,
		r.PathValue("session_id"), time.Now().UTC())
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !revoked {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "会话不存在")
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
