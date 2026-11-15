package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"challenge-api/internal/auth"
)

const (
	loginPattern         = "POST /api/v1/auth/sessions"
	logoutCurrentPattern = "DELETE /api/v1/auth/sessions/current"
	logoutAllPattern     = "DELETE /api/v1/auth/sessions"
	mePattern            = "GET /api/v1/me"
)

type userOut struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Locale      string `json:"locale"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

func newUserOut(user auth.User) userOut {
	return userOut{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Locale:      user.Locale,
		Role:        user.Role,
		Status:      user.Status,
		CreatedAt:   formatTime(user.CreatedAt),
	}
}

type sessionResponse struct {
	User      userOut `json:"user"`
	Token     string  `json:"token"`
	ExpiresAt string  `json:"expires_at"`
}

type okOut struct {
	OK bool `json:"ok"`
}

type loginIn struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Remember *bool  `json:"remember"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		WriteError(w, r, errInternal)
		return
	}
	var in loginIn
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&in); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(email, "@") || len(in.Password) < 8 || len(in.Password) > 200 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}

	user, err := s.store.UserByEmail(r.Context(), email)
	if err != nil {
		s.logger.Error("login query", "request_id", RequestID(r), "error", err)
		WriteError(w, r, errInternal)
		return
	}
	if user == nil || !auth.VerifyPassword(in.Password, user.PasswordHash) {
		// 只有失败才计数：正常登录不受影响
		if !s.recordLoginFailure(w, r, email) {
			return
		}
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "邮箱或口令不正确")
		return
	}
	if user.Status != "active" {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "账号尚未激活或已被停用")
		return
	}

	response, err := s.signIn(w, r, user)
	if err != nil {
		s.logger.Error("session create", "request_id", RequestID(r), "error", err)
		WriteError(w, r, errInternal)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// signIn 创建会话、写 cookie，并组装 SessionResponse。
func (s *Server) signIn(w http.ResponseWriter, r *http.Request,
	user *auth.User) (*sessionResponse, error) {
	token, err := auth.NewToken("")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	userAgent := r.UserAgent()
	if len(userAgent) > 400 {
		userAgent = userAgent[:400]
	}
	var uaPtr *string
	if userAgent != "" {
		uaPtr = &userAgent
	}
	var ipPtr *string
	if ip := s.clientIP(r); ip != "" {
		ipPtr = &ip
	}
	if _, err := s.store.CreateSession(r.Context(), user.ID, auth.HashToken(token), uaPtr,
		ipPtr, now); err != nil {
		return nil, err
	}

	ttl := time.Duration(s.cfg.SessionTTLDays) * 24 * time.Hour
	s.setSessionCookie(w, token, ttl)
	return &sessionResponse{
		User:      newUserOut(*user),
		Token:     token,
		ExpiresAt: formatTime(now.Add(ttl)),
	}, nil
}

func (s *Server) logoutCurrent(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if principal.SessionID != "" {
		if err := s.store.RevokeSession(r.Context(), principal.SessionID,
			time.Now().UTC()); err != nil {
			s.logger.Error("session revoke", "request_id", RequestID(r), "error", err)
			WriteError(w, r, errInternal)
			return
		}
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if err := s.store.RevokeAllSessions(r.Context(), principal.User.ID,
		time.Now().UTC()); err != nil {
		s.logger.Error("sessions revoke", "request_id", RequestID(r), "error", err)
		WriteError(w, r, errInternal)
		return
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, newUserOut(principal.User))
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   s.cfg.CookieDomain,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   s.cfg.CookieDomain,
	})
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) (*auth.Principal, bool) {
	principal, err := s.authenticate(r)
	if err != nil {
		s.logger.Error("authenticate", "request_id", RequestID(r), "error", err)
		WriteError(w, r, errInternal)
		return nil, false
	}
	if principal == nil {
		WriteError(w, r, ErrUnauthorized)
		return nil, false
	}
	return principal, true
}

// authenticate 复刻 Python 的 get_principal_optional：Bearer（API key 或会话令牌）优先，
// 其次是 sid cookie；停用账号视为未登录。
func (s *Server) authenticate(r *http.Request) (*auth.Principal, error) {
	if s.store == nil {
		return nil, nil
	}
	if token := bearerToken(r); token != "" {
		if strings.HasPrefix(token, "sk_") {
			principal, err := s.store.ApiKeyPrincipal(r.Context(), auth.HashToken(token))
			if err != nil || principal == nil || principal.User.Status != "active" {
				return nil, err
			}
			if err := s.store.TouchApiKey(r.Context(), principal.ApiKeyID, time.Now().UTC()); err != nil {
				s.logger.Warn("touch api key", "error", err)
			}
			return principal, nil
		}
		return s.sessionPrincipal(r, token)
	}
	if cookie, err := r.Cookie(s.cfg.SessionCookie); err == nil && cookie.Value != "" {
		return s.sessionPrincipal(r, cookie.Value)
	}
	return nil, nil
}

func (s *Server) sessionPrincipal(r *http.Request, token string) (*auth.Principal, error) {
	principal, err := s.store.SessionPrincipal(r.Context(), auth.HashToken(token))
	if err != nil || principal == nil || principal.User.Status != "active" {
		return nil, err
	}
	if err := s.store.TouchSession(r.Context(), principal.SessionID, time.Now().UTC()); err != nil {
		s.logger.Warn("touch session", "error", err)
	}
	return principal, nil
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}
