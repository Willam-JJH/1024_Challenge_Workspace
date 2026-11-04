package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	wellKnownPattern        = "GET /.well-known/oauth-authorization-server"
	oauthAuthorizePattern   = "GET /api/v1/oauth/authorize"
	oauthConsentGetPattern  = "GET /api/v1/oauth/consent"
	oauthConsentPostPattern = "POST /api/v1/oauth/consent"
	oauthTokenPattern       = "POST /api/v1/oauth/token"
	oauthRevokePattern      = "POST /api/v1/oauth/revoke"
	oauthUserinfoPattern    = "GET /api/v1/oauth/userinfo"
	connectedAppsPattern    = "GET /api/v1/me/connected-apps"
	revokeAppPattern        = "DELETE /api/v1/me/connected-apps/{client_id}"
	revokeDevicePattern     = "DELETE /api/v1/me/connected-apps/{client_id}/devices/{device_id}"
)

const (
	oauthCodeTTL    = 10 * time.Minute
	oauthAccessTTL  = time.Hour
	oauthRefreshTTL = 30 * 24 * time.Hour
)

type oauthClientRow struct {
	Name         string
	RedirectURIs string
	Scopes       string
}

func (c *oauthClientRow) redirectURIs() []string {
	var out []string
	if err := json.Unmarshal([]byte(c.RedirectURIs), &out); err != nil {
		return nil
	}
	return out
}

func (c *oauthClientRow) allows(redirectURI string) bool {
	for _, candidate := range c.redirectURIs() {
		if candidate == redirectURI {
			return true
		}
	}
	return false
}

func (c *oauthClientRow) scopeList() []string {
	return strings.Fields(c.Scopes)
}

func (s *Server) oauthClient(w http.ResponseWriter, r *http.Request,
	clientID string) (*oauthClientRow, bool) {
	var row oauthClientRow
	err := s.writePool().QueryRow(r.Context(), `
		SELECT name, redirect_uris, scopes FROM oauth_clients WHERE client_id = $1`, clientID).
		Scan(&row.Name, &row.RedirectURIs, &row.Scopes)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "应用不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return &row, true
}

func (s *Server) checkRedirect(w http.ResponseWriter, r *http.Request, client *oauthClientRow,
	redirectURI string) bool {
	if !client.allows(redirectURI) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "redirect_uri 未登记")
		return false
	}
	return true
}

func (s *Server) oauthConsented(ctx context.Context, clientID, userID string) (bool, error) {
	var one int
	err := s.writePool().QueryRow(ctx, `
		SELECT 1 FROM oauth_consents
		WHERE client_id = $1 AND user_id = $2 AND revoked_at IS NULL`, clientID, userID).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func verifyPKCE(verifier, challenge string) bool {
	digest := sha256.Sum256([]byte(verifier))
	expected := base64.RawURLEncoding.EncodeToString(digest[:])
	return subtle.ConstantTimeCompare([]byte(expected), []byte(challenge)) == 1
}

func (s *Server) issueOAuthCode(ctx context.Context, clientID, userID, redirectURI,
	challenge, scope string) (string, error) {
	raw, err := auth.NewToken("")
	if err != nil {
		return "", err
	}
	id, err := auth.NewID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = s.writePool().Exec(ctx, `
		INSERT INTO oauth_codes (id, client_id, user_id, redirect_uri, code_hash, code_challenge,
			scope, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		id, clientID, userID, redirectURI, auth.HashToken(raw), challenge, scope,
		now.Add(oauthCodeTTL), now)
	if err != nil {
		return "", err
	}
	return raw, nil
}

type oauthTokenOut struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (s *Server) issueOAuthTokens(ctx context.Context, clientID, userID, scope,
	deviceID string) (*oauthTokenOut, error) {
	access, err := auth.NewToken("")
	if err != nil {
		return nil, err
	}
	refresh, err := auth.NewToken("rt_")
	if err != nil {
		return nil, err
	}
	id, err := auth.NewID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	_, err = s.writePool().Exec(ctx, `
		INSERT INTO oauth_tokens (id, client_id, user_id, device_id, access_hash, refresh_hash,
			scope, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		id, clientID, userID, deviceID, auth.HashToken(access), auth.HashToken(refresh),
		scope, now.Add(oauthAccessTTL), now)
	if err != nil {
		return nil, err
	}
	return &oauthTokenOut{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresIn:    int(oauthAccessTTL.Seconds()),
		Scope:        scope,
	}, nil
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, code, clientID,
	redirectURI, verifier string) (*oauthTokenOut, bool) {
	var row struct {
		ID            string
		ClientID      string
		UserID        string
		RedirectURI   string
		CodeChallenge string
		Scope         string
		ExpiresAt     time.Time
		ConsumedAt    pgtype.Timestamp
	}
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, client_id, user_id, redirect_uri, code_challenge, scope, expires_at,
			consumed_at
		FROM oauth_codes WHERE code_hash = $1`, auth.HashToken(code)).
		Scan(&row.ID, &row.ClientID, &row.UserID, &row.RedirectURI, &row.CodeChallenge,
			&row.Scope, &row.ExpiresAt, &row.ConsumedAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusBadRequest, "invalid_grant", "授权码无效")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	if row.ConsumedAt.Valid {
		writeError(w, r, http.StatusBadRequest, "invalid_grant", "授权码无效")
		return nil, false
	}
	if row.ClientID != clientID || row.RedirectURI != redirectURI {
		writeError(w, r, http.StatusBadRequest, "invalid_grant", "授权码与请求不匹配")
		return nil, false
	}
	if row.ExpiresAt.Before(time.Now().UTC()) {
		writeError(w, r, http.StatusBadRequest, "invalid_grant", "授权码已过期")
		return nil, false
	}
	if !verifyPKCE(verifier, row.CodeChallenge) {
		writeError(w, r, http.StatusBadRequest, "invalid_grant", "PKCE 校验失败")
		return nil, false
	}

	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE oauth_codes SET consumed_at = $2, updated_at = $2 WHERE id = $1`,
		row.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	out, err := s.issueOAuthTokens(r.Context(), row.ClientID, row.UserID, row.Scope, row.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return out, true
}

func (s *Server) rotateRefresh(w http.ResponseWriter, r *http.Request, refreshToken,
	clientID string) (*oauthTokenOut, bool) {
	var row struct {
		ID        string
		ClientID  string
		UserID    string
		DeviceID  string
		Scope     string
		CreatedAt time.Time
		RevokedAt pgtype.Timestamp
	}
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, client_id, user_id, device_id, scope, created_at, revoked_at
		FROM oauth_tokens WHERE refresh_hash = $1`, auth.HashToken(refreshToken)).
		Scan(&row.ID, &row.ClientID, &row.UserID, &row.DeviceID, &row.Scope, &row.CreatedAt,
			&row.RevokedAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusBadRequest, "invalid_grant", "刷新令牌无效")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	if row.RevokedAt.Valid {
		writeError(w, r, http.StatusBadRequest, "invalid_grant", "刷新令牌无效")
		return nil, false
	}
	if row.ClientID != clientID {
		writeError(w, r, http.StatusBadRequest, "invalid_grant", "刷新令牌与客户端不匹配")
		return nil, false
	}
	now := time.Now().UTC()
	if row.CreatedAt.Add(oauthRefreshTTL).Before(now) {
		writeError(w, r, http.StatusBadRequest, "invalid_grant", "刷新令牌已过期")
		return nil, false
	}

	if _, err := s.writePool().Exec(r.Context(),
		`UPDATE oauth_tokens SET revoked_at = $2, updated_at = $2 WHERE id = $1`,
		row.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	out, err := s.issueOAuthTokens(r.Context(), row.ClientID, row.UserID, row.Scope, row.DeviceID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return nil, false
	}
	return out, true
}

// oauthFormOrJSON 复刻 Python 的 _form_or_json：表单优先，JSON 解析失败视为空表。
func oauthFormOrJSON(r *http.Request) map[string]any {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		out := map[string]any{}
		if err := r.ParseForm(); err != nil {
			return out
		}
		for key, values := range r.PostForm {
			if len(values) > 0 {
				out[key] = values[0]
			}
		}
		return out
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload == nil {
		return map[string]any{}
	}
	return payload
}

func jsonStr(payload map[string]any, key, fallback string) string {
	value, ok := payload[key]
	if !ok {
		return fallback
	}
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return fallback
	}
	return fmt.Sprint(value)
}

// pyTruthy 近似 Python 的真值判断，供 consent 的 approve/state 使用。
func pyTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case json.Number:
		return typed.String() != "" && typed.String() != "0"
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func redirectWithCode(redirectURI, code, state string) string {
	joiner := "?"
	if strings.Contains(redirectURI, "?") {
		joiner = "&"
	}
	url := redirectURI + joiner + "code=" + code
	if state != "" {
		url += "&state=" + state
	}
	return url
}

// quoteLocation 复刻 Starlette RedirectResponse 对 Location 的百分号编码。
func quoteLocation(raw string) string {
	const safe = "_.-~:/%#?=@[]!$&'()*+,;"
	var builder strings.Builder
	for _, r := range raw {
		if r < 128 && (isASCIIAlnum(byte(r)) || strings.ContainsRune(safe, r)) {
			builder.WriteByte(byte(r))
			continue
		}
		for _, b := range []byte(string(r)) {
			fmt.Fprintf(&builder, "%%%02X", b)
		}
	}
	return builder.String()
}

func isASCIIAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	clientID, hasClient := query["client_id"]
	redirectURI, hasRedirect := query["redirect_uri"]
	challenge, hasChallenge := query["code_challenge"]
	if !hasClient || !hasRedirect || !hasChallenge {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	responseType := "code"
	if values, ok := query["response_type"]; ok && len(values) > 0 {
		responseType = values[0]
	}
	method := "S256"
	if values, ok := query["code_challenge_method"]; ok && len(values) > 0 {
		method = values[0]
	}
	scope := "profile"
	if values, ok := query["scope"]; ok && len(values) > 0 {
		scope = values[0]
	}
	state := ""
	if values, ok := query["state"]; ok && len(values) > 0 {
		state = values[0]
	}

	client, ok := s.oauthClient(w, r, clientID[0])
	if !ok {
		return
	}
	if !s.checkRedirect(w, r, client, redirectURI[0]) {
		return
	}
	if responseType != "code" || method != "S256" {
		writeError(w, r, http.StatusBadRequest, "unsupported_response_type",
			"只支持授权码 + PKCE S256")
		return
	}
	consented, err := s.oauthConsented(r.Context(), clientID[0], principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !consented {
		writeError(w, r, http.StatusForbidden, "consent_required", "需要用户先授权")
		return
	}

	code, err := s.issueOAuthCode(r.Context(), clientID[0], principal.User.ID, redirectURI[0],
		challenge[0], scope)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	target := redirectWithCode(redirectURI[0], code, state)
	w.Header().Set("Location", quoteLocation(target))
	w.WriteHeader(http.StatusFound)
}

type consentInfoOut struct {
	ClientName string   `json:"client_name"`
	Scopes     []string `json:"scopes"`
	Approved   bool     `json:"approved"`
}

func (s *Server) oauthConsentInfo(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	values, present := r.URL.Query()["client_id"]
	if !present {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	client, ok := s.oauthClient(w, r, values[0])
	if !ok {
		return
	}
	approved, err := s.oauthConsented(r.Context(), values[0], principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, consentInfoOut{
		ClientName: client.Name,
		Scopes:     client.scopeList(),
		Approved:   approved,
	})
}

func (s *Server) oauthConsent(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var payload map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil ||
		payload == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	clientID := jsonStr(payload, "client_id", "")
	redirectURI := jsonStr(payload, "redirect_uri", "")
	client, ok := s.oauthClient(w, r, clientID)
	if !ok {
		return
	}
	if !s.checkRedirect(w, r, client, redirectURI) {
		return
	}
	state := jsonStr(payload, "state", "")
	approved := true
	if value, present := payload["approve"]; present {
		approved = pyTruthy(value)
	}
	if !approved {
		joiner := "?"
		if strings.Contains(redirectURI, "?") {
			joiner = "&"
		}
		url := redirectURI + joiner + "error=access_denied"
		if state != "" {
			url += "&state=" + state
		}
		writeJSON(w, http.StatusOK, map[string]string{"redirect_url": url})
		return
	}

	scope := jsonStr(payload, "scope", "profile")
	now := time.Now().UTC()
	var consentID string
	var revokedAt pgtype.Timestamp
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, revoked_at FROM oauth_consents
		WHERE client_id = $1 AND user_id = $2`, clientID, principal.User.ID).
		Scan(&consentID, &revokedAt)
	switch {
	case err == nil && revokedAt.Valid:
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE oauth_consents SET revoked_at = NULL, updated_at = $2 WHERE id = $1`,
			consentID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	case err == nil:
		// 已授权且未撤销：SQLAlchemy 不会产生 UPDATE。
	case isNoRows(err):
		id, idErr := auth.NewID()
		if idErr != nil {
			WriteError(w, r, errInternal)
			return
		}
		if _, err := s.writePool().Exec(r.Context(), `
			INSERT INTO oauth_consents (id, user_id, client_id, scope, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5)`, id, principal.User.ID, clientID, scope, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	default:
		s.writeQueryError(w, r, err)
		return
	}

	code, err := s.issueOAuthCode(r.Context(), clientID, principal.User.ID, redirectURI,
		jsonStr(payload, "code_challenge", ""), scope)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"redirect_url": redirectWithCode(redirectURI, code, state),
	})
}

func (s *Server) oauthTokenHandler(w http.ResponseWriter, r *http.Request) {
	payload := oauthFormOrJSON(r)
	grantType := jsonStr(payload, "grant_type", "")
	clientID := jsonStr(payload, "client_id", "")

	var out *oauthTokenOut
	var ok bool
	switch grantType {
	case "authorization_code":
		out, ok = s.exchangeCode(w, r,
			jsonStr(payload, "code", ""),
			clientID,
			jsonStr(payload, "redirect_uri", ""),
			jsonStr(payload, "code_verifier", ""))
	case "refresh_token":
		out, ok = s.rotateRefresh(w, r, jsonStr(payload, "refresh_token", ""), clientID)
	default:
		writeError(w, r, http.StatusBadRequest, "unsupported_grant_type", "不支持的 grant_type")
		return
	}
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	payload := oauthFormOrJSON(r)
	raw := jsonStr(payload, "token", "")
	if raw != "" {
		var tokenID string
		err := s.writePool().QueryRow(r.Context(), `
			SELECT id FROM oauth_tokens
			WHERE access_hash = $1 OR refresh_hash = $1
			LIMIT 1`, auth.HashToken(raw)).Scan(&tokenID)
		if err != nil && !isNoRows(err) {
			s.writeQueryError(w, r, err)
			return
		}
		if err == nil {
			now := time.Now().UTC()
			if _, err := s.writePool().Exec(r.Context(),
				`UPDATE oauth_tokens SET revoked_at = $2, updated_at = $2 WHERE id = $1`,
				tokenID, now); err != nil {
				s.writeQueryError(w, r, err)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) oauthUserinfo(w http.ResponseWriter, r *http.Request) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		writeError(w, r, http.StatusUnauthorized, "invalid_token", "缺少令牌")
		return
	}
	raw := strings.TrimSpace(header[7:])

	var row struct {
		ID        string
		UserID    string
		ExpiresAt time.Time
		RevokedAt pgtype.Timestamp
	}
	err := s.writePool().QueryRow(r.Context(), `
		SELECT id, user_id, expires_at, revoked_at FROM oauth_tokens
		WHERE access_hash = $1`, auth.HashToken(raw)).
		Scan(&row.ID, &row.UserID, &row.ExpiresAt, &row.RevokedAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusUnauthorized, "invalid_token", "令牌无效")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if row.RevokedAt.Valid {
		writeError(w, r, http.StatusUnauthorized, "invalid_token", "令牌无效")
		return
	}
	if row.ExpiresAt.Before(time.Now().UTC()) {
		writeError(w, r, http.StatusUnauthorized, "invalid_token", "令牌已过期")
		return
	}

	var email, name, locale string
	err = s.writePool().QueryRow(r.Context(),
		`SELECT email, display_name, locale FROM users WHERE id = $1`, row.UserID).
		Scan(&email, &name, &locale)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "账号不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"sub":    row.UserID,
		"email":  email,
		"name":   name,
		"locale": locale,
	})
}

func (s *Server) oauthMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                           "1024-challenge",
		"authorization_endpoint":           "/api/v1/oauth/authorize",
		"token_endpoint":                   "/api/v1/oauth/token",
		"revocation_endpoint":              "/api/v1/oauth/revoke",
		"userinfo_endpoint":                "/api/v1/oauth/userinfo",
		"response_types_supported":         []string{"code"},
		"grant_types_supported":            []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported": []string{"S256"},
		"scopes_supported":                 []string{"profile", "llm"},
	})
}

type connectedDeviceOut struct {
	DeviceID  string `json:"device_id"`
	CreatedAt string `json:"created_at"`
}

type connectedAppOut struct {
	ClientID   string               `json:"client_id"`
	ClientName string               `json:"client_name"`
	Devices    []connectedDeviceOut `json:"devices"`
}

func (s *Server) connectedApps(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT client_id, device_id, created_at FROM oauth_tokens
		WHERE user_id = $1 AND revoked_at IS NULL`, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	var order []string
	grouped := map[string][]connectedDeviceOut{}
	for rows.Next() {
		var clientID, deviceID string
		var createdAt time.Time
		if err := rows.Scan(&clientID, &deviceID, &createdAt); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		if _, seen := grouped[clientID]; !seen {
			order = append(order, clientID)
		}
		grouped[clientID] = append(grouped[clientID], connectedDeviceOut{
			DeviceID:  deviceID,
			CreatedAt: formatTimeOffset(createdAt),
		})
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	out := make([]connectedAppOut, 0, len(order))
	for _, clientID := range order {
		var name string
		err := s.writePool().QueryRow(r.Context(),
			`SELECT name FROM oauth_clients WHERE client_id = $1`, clientID).Scan(&name)
		if err != nil {
			if !isNoRows(err) {
				s.writeQueryError(w, r, err)
				return
			}
			name = clientID
		}
		out = append(out, connectedAppOut{
			ClientID:   clientID,
			ClientName: name,
			Devices:    grouped[clientID],
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) revokeConnectedApp(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	clientID := r.PathValue("client_id")
	now := time.Now().UTC()
	var one int
	err := s.writePool().QueryRow(r.Context(), `
		SELECT 1 FROM oauth_tokens
		WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL LIMIT 1`,
		principal.User.ID, clientID).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "没有这个应用的授权")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE oauth_tokens SET revoked_at = $3, updated_at = $3
		WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL`,
		principal.User.ID, clientID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE oauth_consents SET revoked_at = $3, updated_at = $3
		WHERE client_id = $1 AND user_id = $2`, clientID, principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) revokeConnectedDevice(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	clientID := r.PathValue("client_id")
	deviceID := r.PathValue("device_id")
	var one int
	err := s.writePool().QueryRow(r.Context(), `
		SELECT 1 FROM oauth_tokens
		WHERE user_id = $1 AND client_id = $2 AND device_id = $3 AND revoked_at IS NULL
		LIMIT 1`, principal.User.ID, clientID, deviceID).Scan(&one)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "设备不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE oauth_tokens SET revoked_at = $4, updated_at = $4
		WHERE user_id = $1 AND client_id = $2 AND device_id = $3 AND revoked_at IS NULL`,
		principal.User.ID, clientID, deviceID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}
