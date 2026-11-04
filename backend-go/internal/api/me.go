package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"challenge-api/internal/auth"
)

const (
	mePatchPattern              = "PATCH /api/v1/me"
	apiKeysPattern              = "GET /api/v1/me/api-keys"
	apiKeyCreatePattern         = "POST /api/v1/me/api-keys"
	apiKeyRevokePattern         = "DELETE /api/v1/me/api-keys/{key_id}"
	apiKeyRotatePattern         = "POST /api/v1/me/api-keys/{key_id}/rotations"
	myVotesPattern              = "GET /api/v1/me/votes"
	myVotesPutPattern           = "PUT /api/v1/me/votes"
	myVotePattern               = "PUT /api/v1/me/votes/{company_id}"
	myVoteDeletePattern         = "DELETE /api/v1/me/votes/{company_id}"
	membershipPattern           = "GET /api/v1/me/membership"
	participationPattern        = "PUT /api/v1/me/participation"
	myInvitationsPattern        = "GET /api/v1/me/invitations"
	myInvitationAcceptPattern   = "POST /api/v1/me/invitations/{invitation_id}/acceptance"
	myInvitationDeclinePattern  = "POST /api/v1/me/invitations/{invitation_id}/decline"
	myJoinRequestsPattern       = "GET /api/v1/me/join-requests"
	myJoinRequestPattern        = "DELETE /api/v1/me/join-requests/{request_id}"
	notificationsPattern        = "GET /api/v1/me/notifications"
	notificationsReadPattern    = "POST /api/v1/me/notifications/read"
	digestPattern               = "GET /api/v1/me/digest-preferences"
	digestPutPattern            = "PUT /api/v1/me/digest-preferences"
	visitsPattern               = "GET /api/v1/me/visits"
	deletionRequestPattern      = "POST /api/v1/me/deletion-request"
	deletionCancelPattern       = "DELETE /api/v1/me/deletion-request"
	exhibitionAssignmentPattern = "GET /api/v1/me/exhibition-assignment"
)

// --- 资料 -------------------------------------------------------------------

type profileUpdateIn struct {
	DisplayName *string `json:"display_name"`
	Locale      *string `json:"locale"`
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in profileUpdateIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if (in.DisplayName != nil && (len([]rune(*in.DisplayName)) < 1 ||
		len([]rune(*in.DisplayName)) > 120)) ||
		(in.Locale != nil && len([]rune(*in.Locale)) > 8) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE users SET display_name = COALESCE($2, display_name),
			locale = COALESCE($3, locale), updated_at = $4
		WHERE id = $1`,
		principal.User.ID, in.DisplayName, in.Locale, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	user, err := s.store.UserByID(r.Context(), principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if user == nil {
		WriteError(w, r, errInternal)
		return
	}
	writeJSON(w, http.StatusOK, newUserOut(*user))
}

// --- API key ----------------------------------------------------------------

type apiKeyOut struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	RevokedAt  *string `json:"revoked_at"`
}

type apiKeyCreatedOut struct {
	Key   apiKeyOut `json:"key"`
	Token string    `json:"token"`
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, label, prefix, created_at, last_used_at, revoked_at
		FROM api_keys
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC`, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]apiKeyOut, 0, 4)
	for rows.Next() {
		var item apiKeyOut
		var createdAt time.Time
		var lastUsed, revoked pgtype.Timestamp
		if err := rows.Scan(&item.ID, &item.Label, &item.Prefix, &createdAt, &lastUsed,
			&revoked); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.CreatedAt = formatTime(createdAt)
		item.LastUsedAt = formatTimePtr(timestampPtr(lastUsed))
		item.RevokedAt = formatTimePtr(timestampPtr(revoked))
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) issueAPIKey(ctx context.Context, userID, label string) (*apiKeyCreatedOut, error) {
	raw, err := auth.NewToken("")
	if err != nil {
		return nil, err
	}
	token := "sk_" + raw
	id, err := auth.NewID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	prefix := token
	if len(prefix) > 11 {
		prefix = prefix[:11]
	}
	if _, err := s.writePool().Exec(ctx, `
		INSERT INTO api_keys (id, user_id, label, prefix, key_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		id, userID, label, prefix, auth.HashToken(token), now); err != nil {
		return nil, err
	}
	return &apiKeyCreatedOut{
		Key: apiKeyOut{
			ID:        id,
			Label:     label,
			Prefix:    prefix,
			CreatedAt: formatTime(now),
		},
		Token: token,
	}, nil
}

// mustToken 生成 43 字符的 URL-safe 令牌；NewToken 只会因系统随机源失败而报错。
type apiKeyIn struct {
	Label *string `json:"label"`
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in apiKeyIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Label == nil || len([]rune(*in.Label)) < 1 || len([]rune(*in.Label)) > 80 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	out, err := s.issueAPIKey(r.Context(), principal.User.ID, *in.Label)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

type apiKeyRow struct {
	UserID    string
	Label     string
	RevokedAt pgtype.Timestamp
}

func (s *Server) apiKeyForUser(w http.ResponseWriter, r *http.Request,
	keyID, userID string) (*apiKeyRow, bool) {
	var row apiKeyRow
	err := s.writePool().QueryRow(r.Context(),
		`SELECT user_id, label, revoked_at FROM api_keys WHERE id = $1`, keyID).
		Scan(&row.UserID, &row.Label, &row.RevokedAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "API key 不存在")
			return nil, false
		}
		s.writeQueryError(w, r, err)
		return nil, false
	}
	if row.UserID != userID {
		writeError(w, r, http.StatusNotFound, "not_found", "API key 不存在")
		return nil, false
	}
	return &row, true
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.apiKeyForUser(w, r, r.PathValue("key_id"), principal.User.ID)
	if !ok {
		return
	}
	if !row.RevokedAt.Valid {
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE api_keys SET revoked_at = $2, updated_at = $2 WHERE id = $1`,
			r.PathValue("key_id"), time.Now().UTC()); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) rotateAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, ok := s.apiKeyForUser(w, r, r.PathValue("key_id"), principal.User.ID)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE api_keys SET revoked_at = $2, updated_at = $2 WHERE id = $1`,
		r.PathValue("key_id"), now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	out, err := s.issueAPIKey(r.Context(), principal.User.ID, row.Label)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// --- 投票记录 ---------------------------------------------------------------

type myVotePickOut struct {
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	CompanySlug string `json:"company_slug"`
	CreatedAt   string `json:"created_at"`
}

type myVotesOut struct {
	Votes     []myVotePickOut `json:"votes"`
	VotesLeft int             `json:"votes_left"`
}

func (s *Server) myVotesPayload(r *http.Request, userID string) (*myVotesOut, error) {
	var eventID string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT id FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).Scan(&eventID)
	if err != nil {
		if isNoRows(err) {
			return nil, errNoCurrentEvent
		}
		return nil, err
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT v.company_id, c.name, c.slug, v.created_at
		FROM votes v JOIN companies c ON c.id = v.company_id
		WHERE v.event_id = $1 AND v.voter_id = $2 AND v.status = 'live'
		ORDER BY v.created_at`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &myVotesOut{Votes: make([]myVotePickOut, 0, 3)}
	for rows.Next() {
		var item myVotePickOut
		var createdAt time.Time
		if err := rows.Scan(&item.CompanyID, &item.CompanyName, &item.CompanySlug,
			&createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt = formatTime(createdAt)
		out.Votes = append(out.Votes, item)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	out.VotesLeft = s.cfg.WalletMaxVotes - len(out.Votes)
	if out.VotesLeft < 0 {
		out.VotesLeft = 0
	}
	return out, nil
}

func (s *Server) writeMyVotes(w http.ResponseWriter, r *http.Request, userID string) {
	out, err := s.myVotesPayload(r, userID)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getMyVotes(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	s.writeMyVotes(w, r, principal.User.ID)
}

type votingEvent struct {
	ID   string
	Open bool
}

func (s *Server) votingEvent(r *http.Request) (*votingEvent, error) {
	var event votingEvent
	err := s.writePool().QueryRow(r.Context(),
		`SELECT id, voting_open FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).
		Scan(&event.ID, &event.Open)
	if err != nil {
		if isNoRows(err) {
			return nil, errNoCurrentEvent
		}
		return nil, err
	}
	return &event, nil
}

func (s *Server) companyInEvent(ctx context.Context, q queryer, companyID, eventID string) (bool, error) {
	var companyEvent string
	err := q.QueryRow(ctx,
		`SELECT event_id FROM companies WHERE id = $1`, companyID).Scan(&companyEvent)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return companyEvent == eventID, nil
}

func (s *Server) votesOfUser(ctx context.Context, q queryer, eventID, userID string) (map[string]string, error) {
	rows, err := q.Query(ctx, `
		SELECT id, company_id FROM votes
		WHERE event_id = $1 AND voter_id = $2 AND status = 'live'`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var voteID, companyID string
		if err := rows.Scan(&voteID, &companyID); err != nil {
			return nil, err
		}
		out[companyID] = voteID
	}
	return out, rows.Err()
}

func (s *Server) assertVotable(ctx context.Context, q queryer, w http.ResponseWriter,
	r *http.Request, event *votingEvent, companyID, userID string) (bool, error) {
	ok, err := s.companyInEvent(ctx, q, companyID, event.ID)
	if err != nil {
		return false, err
	}
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "公司不存在")
		return false, nil
	}
	var one int
	err = q.QueryRow(ctx, `
		SELECT 1 FROM memberships WHERE company_id = $1 AND user_id = $2 LIMIT 1`,
		companyID, userID).Scan(&one)
	if err == nil {
		writeError(w, r, http.StatusForbidden, "forbidden", "不能投给自己所在的公司")
		return false, nil
	}
	if !isNoRows(err) {
		return false, err
	}
	return true, nil
}

type votesIn struct {
	CompanyIDs *[]string `json:"company_ids"`
}

func (s *Server) replaceMyVotes(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in votesIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.CompanyIDs == nil || len(*in.CompanyIDs) > 20 {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	event, err := s.votingEvent(r)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if !event.Open {
		writeError(w, r, http.StatusForbidden, "forbidden", "投票已关闭")
		return
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(*in.CompanyIDs))
	for _, companyID := range *in.CompanyIDs {
		if !seen[companyID] {
			seen[companyID] = true
			unique = append(unique, companyID)
		}
	}
	if len(unique) > s.cfg.WalletMaxVotes {
		writeError(w, r, http.StatusConflict, "conflict",
			"最多只能投给 "+strconvItoa(s.cfg.WalletMaxVotes)+" 家公司")
		return
	}
	tx, err := s.writePool().Begin(r.Context())
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	// 锁住用户行，让同一用户的并发投票串行化，避免绕过钱包上限
	if _, err := tx.Exec(r.Context(),
		`SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, principal.User.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}

	for _, companyID := range unique {
		ok, err := s.assertVotable(r.Context(), tx, w, r, event, companyID, principal.User.ID)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		if !ok {
			return
		}
	}

	current, err := s.votesOfUser(r.Context(), tx, event.ID, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	now := time.Now().UTC()
	for companyID, voteID := range current {
		if seen[companyID] {
			continue
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE votes SET status = 'withdrawn', updated_at = $2 WHERE id = $1`,
			voteID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	for _, companyID := range unique {
		if _, exists := current[companyID]; exists {
			continue
		}
		id, err := auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO votes (id, event_id, voter_id, company_id, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'live', $5, $5)`,
			id, event.ID, principal.User.ID, companyID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	s.writeMyVotes(w, r, principal.User.ID)
}

func (s *Server) castVote(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	event, err := s.votingEvent(r)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if !event.Open {
		writeError(w, r, http.StatusForbidden, "forbidden", "投票已关闭")
		return
	}
	companyID := r.PathValue("company_id")
	tx, err := s.writePool().Begin(r.Context())
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())
	// 锁住用户行，让同一用户的并发投票串行化，避免绕过钱包上限
	if _, err := tx.Exec(r.Context(),
		`SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, principal.User.ID); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	ok, err = s.assertVotable(r.Context(), tx, w, r, event, companyID, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if !ok {
		return
	}

	var existingID, existingStatus string
	existingErr := tx.QueryRow(r.Context(), `
		SELECT id, status FROM votes
		WHERE event_id = $1 AND voter_id = $2 AND company_id = $3 LIMIT 1`,
		event.ID, principal.User.ID, companyID).Scan(&existingID, &existingStatus)
	if existingErr != nil && !isNoRows(existingErr) {
		s.writeQueryError(w, r, existingErr)
		return
	}
	hasExisting := existingErr == nil
	if hasExisting && existingStatus == "live" {
		s.writeMyVotes(w, r, principal.User.ID)
		return
	}
	live, err := s.votesOfUser(r.Context(), tx, event.ID, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	if len(live) >= s.cfg.WalletMaxVotes {
		writeError(w, r, http.StatusConflict, "conflict",
			"最多只能投给 "+strconvItoa(s.cfg.WalletMaxVotes)+" 家公司")
		return
	}
	now := time.Now().UTC()
	if hasExisting {
		if _, err := tx.Exec(r.Context(), `
			UPDATE votes SET status = 'live', updated_at = $2 WHERE id = $1`,
			existingID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	} else {
		id, err := auth.NewID()
		if err != nil {
			WriteError(w, r, errInternal)
			return
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO votes (id, event_id, voter_id, company_id, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'live', $5, $5)`,
			id, event.ID, principal.User.ID, companyID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	s.writeMyVotes(w, r, principal.User.ID)
}

func (s *Server) withdrawVote(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	event, err := s.votingEvent(r)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	companyID := r.PathValue("company_id")
	var voteID, status string
	err = s.writePool().QueryRow(r.Context(), `
		SELECT id, status FROM votes
		WHERE event_id = $1 AND voter_id = $2 AND company_id = $3 LIMIT 1`,
		event.ID, principal.User.ID, companyID).Scan(&voteID, &status)
	if err != nil && !isNoRows(err) {
		s.writeQueryError(w, r, err)
		return
	}
	if err == nil && status == "live" {
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE votes SET status = 'withdrawn', updated_at = $2 WHERE id = $1`,
			voteID, time.Now().UTC()); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	s.writeMyVotes(w, r, principal.User.ID)
}

// --- 公司关系与参与方式 -------------------------------------------------------

type myMembershipRow struct {
	CompanyID   string
	CompanyName string
	CompanySlug string
	Role        string
}

// myMembership 复刻 services/companies.my_membership：赛事不存在时返回 errNoCurrentEvent。
func (s *Server) myMembership(ctx context.Context, userID string) (*myMembershipRow, error) {
	var eventID string
	err := s.writePool().QueryRow(ctx,
		`SELECT id FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).Scan(&eventID)
	if err != nil {
		if isNoRows(err) {
			return nil, errNoCurrentEvent
		}
		return nil, err
	}
	var row myMembershipRow
	err = s.writePool().QueryRow(ctx, `
		SELECT m.company_id, c.name, c.slug, m.role
		FROM memberships m JOIN companies c ON c.id = m.company_id
		WHERE m.user_id = $1 AND c.event_id = $2 LIMIT 1`, userID, eventID).
		Scan(&row.CompanyID, &row.CompanyName, &row.CompanySlug, &row.Role)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

type membershipOut struct {
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	CompanySlug string `json:"company_slug"`
	Role        string `json:"role"`
	Captain     bool   `json:"captain"`
}

func (s *Server) myMembershipHandler(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	row, err := s.myMembership(r.Context(), principal.User.ID)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if row == nil {
		writeJSON(w, http.StatusOK, map[string]any{"membership": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"membership": membershipOut{
		CompanyID:   row.CompanyID,
		CompanyName: row.CompanyName,
		CompanySlug: row.CompanySlug,
		Role:        row.Role,
		Captain:     row.Role == "captain",
	}})
}

type participationIn struct {
	Mode *string `json:"mode"`
}

func (s *Server) setParticipation(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in participationIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Mode == nil || (*in.Mode != "audience" && *in.Mode != "participant") {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	now := time.Now().UTC()
	if *in.Mode == "participant" {
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE users SET role = 'member', updated_at = $2
			WHERE id = $1 AND role = 'participant'`,
			principal.User.ID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	} else {
		row, err := s.myMembership(r.Context(), principal.User.ID)
		if err != nil {
			if errors.Is(err, errNoCurrentEvent) {
				writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
				return
			}
			s.writeQueryError(w, r, err)
			return
		}
		if row != nil {
			writeError(w, r, http.StatusConflict, "conflict", "还在公司里，先退出再切换")
			return
		}
		if _, err := s.writePool().Exec(r.Context(), `
			UPDATE users SET role = 'participant', updated_at = $2 WHERE id = $1`,
			principal.User.ID, now); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

// --- 邀请、入队申请 -----------------------------------------------------------

type invitationRowOut struct {
	InvitationID string `json:"invitation_id"`
	CompanyID    string `json:"company_id"`
	CompanyName  string `json:"company_name"`
	Email        string `json:"email"`
	Status       string `json:"status"`
}

func (s *Server) myInvitations(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT i.id, i.company_id, COALESCE(c.name, ''), i.email, i.status
		FROM invitations i LEFT JOIN companies c ON c.id = i.company_id
		WHERE i.email = $1 AND i.status = 'pending'`,
		strings.ToLower(principal.User.Email))
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]invitationRowOut, 0, 4)
	for rows.Next() {
		var item invitationRowOut
		if err := rows.Scan(&item.InvitationID, &item.CompanyID, &item.CompanyName,
			&item.Email, &item.Status); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// acceptInvitation 复刻 me_extra 与 finder 中重复的接受逻辑。
func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var companyID string
	var email string
	var status string
	var expiresAt pgtype.Timestamp
	err := s.writePool().QueryRow(r.Context(), `
		SELECT company_id, email, status, expires_at FROM invitations WHERE id = $1`,
		r.PathValue("invitation_id")).
		Scan(&companyID, &email, &status, &expiresAt)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "邀请不存在或已处理")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if status != "pending" {
		writeError(w, r, http.StatusNotFound, "not_found", "邀请不存在或已处理")
		return
	}
	if email != strings.ToLower(principal.User.Email) {
		writeError(w, r, http.StatusNotFound, "not_found", "邀请不属于这个账号")
		return
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now().UTC()) {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "邀请已过期")
		return
	}
	var seats int
	err = s.writePool().QueryRow(r.Context(), `
		SELECT e.company_seats FROM companies c JOIN events e ON e.id = c.event_id
		WHERE c.id = $1`, companyID).Scan(&seats)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "公司不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if ok, err := s.addMember(w, r, principal.User.ID, companyID, seats); err != nil {
		s.writeQueryError(w, r, err)
		return
	} else if !ok {
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE invitations SET status = 'accepted', updated_at = $2 WHERE id = $1`,
		r.PathValue("invitation_id"), now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

func (s *Server) declineInvitation(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var email, status string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT email, status FROM invitations WHERE id = $1`, r.PathValue("invitation_id")).
		Scan(&email, &status)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "邀请不存在或已处理")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if status != "pending" {
		writeError(w, r, http.StatusNotFound, "not_found", "邀请不存在或已处理")
		return
	}
	if email != strings.ToLower(principal.User.Email) {
		writeError(w, r, http.StatusNotFound, "not_found", "邀请不属于这个账号")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE invitations SET status = 'declined', updated_at = $2 WHERE id = $1`,
		r.PathValue("invitation_id"), time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type myJoinRequestOut struct {
	ID          string `json:"id"`
	CompanyID   string `json:"company_id"`
	CompanyName string `json:"company_name"`
	Status      string `json:"status"`
}

func (s *Server) myJoinRequests(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT j.id, j.company_id, COALESCE(c.name, ''), j.status
		FROM join_requests j LEFT JOIN companies c ON c.id = j.company_id
		WHERE j.user_id = $1 AND j.status = 'pending'`, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]myJoinRequestOut, 0, 4)
	for rows.Next() {
		var item myJoinRequestOut
		if err := rows.Scan(&item.ID, &item.CompanyID, &item.CompanyName,
			&item.Status); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) withdrawJoinRequest(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	requestID := r.PathValue("request_id")
	var userID, status string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT user_id, status FROM join_requests WHERE id = $1`, requestID).
		Scan(&userID, &status)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "申请不存在")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if userID != principal.User.ID || status != "pending" {
		writeError(w, r, http.StatusNotFound, "not_found", "申请不存在")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE join_requests SET status = 'withdrawn', updated_at = $2 WHERE id = $1`,
		requestID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

// --- 通知、摘要、参观、注销、介绍任务 -------------------------------------------

type notificationOut struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	Body      *string `json:"body"`
	ReadAt    *string `json:"read_at"`
	CreatedAt string  `json:"created_at"`
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, kind, title, body, read_at, created_at
		FROM notifications WHERE user_id = $1
		ORDER BY created_at DESC LIMIT 100`, principal.User.ID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	out := make([]notificationOut, 0, 16)
	for rows.Next() {
		var item notificationOut
		var body pgtype.Text
		var readAtTime pgtype.Timestamp
		var createdAt time.Time
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &body, &readAtTime,
			&createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.Body = textPtr(body)
		item.ReadAt = formatTimePtr(timestampPtr(readAtTime))
		item.CreatedAt = formatTime(createdAt)
		out = append(out, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) markNotificationsRead(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE notifications SET read_at = $2, updated_at = $2
		WHERE user_id = $1 AND read_at IS NULL`, principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, okOut{OK: true})
}

type digestOut struct {
	Comments bool `json:"comments"`
}

func (s *Server) getDigestPreferences(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var comments bool
	if err := s.writePool().QueryRow(r.Context(),
		`SELECT digest_comments FROM users WHERE id = $1`, principal.User.ID).
		Scan(&comments); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, digestOut{Comments: comments})
}

type digestIn struct {
	Comments *bool `json:"comments"`
}

func (s *Server) setDigestPreferences(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var in digestIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Comments == nil {
		writeError(w, r, http.StatusUnprocessableEntity, "unprocessable", "请求不合法")
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE users SET digest_comments = $2, updated_at = $3 WHERE id = $1`,
		principal.User.ID, *in.Comments, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, digestOut{Comments: *in.Comments})
}

type visitItemOut struct {
	ProjectID    string  `json:"project_id"`
	CompanyName  string  `json:"company_name"`
	ProjectTitle string  `json:"project_title"`
	Note         *string `json:"note"`
	CreatedAt    string  `json:"created_at"`
}

func (s *Server) myVisits(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var eventID string
	err := s.writePool().QueryRow(r.Context(),
		`SELECT id FROM events WHERE slug = $1`, s.cfg.CurrentEventSlug).Scan(&eventID)
	if err != nil {
		if isNoRows(err) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT p.id, c.name, p.title, v.note, v.created_at
		FROM visits v
		JOIN projects p ON p.id = v.project_id
		JOIN companies c ON c.id = p.company_id
		WHERE v.visitor_id = $1 AND v.event_id = $2
		ORDER BY v.created_at`, principal.User.ID, eventID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	defer rows.Close()
	items := make([]visitItemOut, 0, 8)
	companies := map[string]bool{}
	for rows.Next() {
		var item visitItemOut
		var note pgtype.Text
		var createdAt time.Time
		if err := rows.Scan(&item.ProjectID, &item.CompanyName, &item.ProjectTitle, &note,
			&createdAt); err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		item.Note = textPtr(note)
		item.CreatedAt = formatTimeOffset(createdAt)
		companies[item.CompanyName] = true
		items = append(items, item)
	}
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items    []visitItemOut `json:"items"`
		Needed   int            `json:"needed"`
		Complete bool           `json:"complete"`
	}{Items: items, Needed: 3, Complete: len(companies) >= 3})
}

type deletionOut struct {
	RequestedAt *string `json:"requested_at"`
}

func (s *Server) requestDeletion(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE users SET deletion_requested_at = $2, updated_at = $2 WHERE id = $1`,
		principal.User.ID, now); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	formatted := formatTime(now)
	writeJSON(w, http.StatusOK, deletionOut{RequestedAt: &formatted})
}

func (s *Server) cancelDeletion(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if _, err := s.writePool().Exec(r.Context(), `
		UPDATE users SET deletion_requested_at = NULL, updated_at = $2 WHERE id = $1`,
		principal.User.ID, time.Now().UTC()); err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deletionOut{RequestedAt: nil})
}

func (s *Server) exhibitionAssignment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	membership, err := s.myMembership(r.Context(), principal.User.ID)
	if err != nil {
		if errors.Is(err, errNoCurrentEvent) {
			writeError(w, r, http.StatusNotFound, "not_found", "当前赛事尚未配置")
			return
		}
		s.writeQueryError(w, r, err)
		return
	}
	if membership == nil {
		writeJSON(w, http.StatusOK, []introductionOut{})
		return
	}
	rows, err := s.writePool().Query(r.Context(), `
		SELECT id, introducer_company_id, target_company_id, text, reply, status
		FROM introductions
		WHERE introducer_company_id = $1 OR target_company_id = $1`,
		membership.CompanyID)
	if err != nil {
		s.writeQueryError(w, r, err)
		return
	}
	type introRow struct {
		ID           string
		IntroducerID string
		TargetID     string
		Text         pgtype.Text
		Reply        pgtype.Text
		Status       string
	}
	collected := make([]introRow, 0, 8)
	companyIDs := map[string]bool{}
	for rows.Next() {
		var item introRow
		if err := rows.Scan(&item.ID, &item.IntroducerID, &item.TargetID, &item.Text,
			&item.Reply, &item.Status); err != nil {
			rows.Close()
			s.writeQueryError(w, r, err)
			return
		}
		collected = append(collected, item)
		companyIDs[item.IntroducerID] = true
		companyIDs[item.TargetID] = true
	}
	rows.Close()
	if rows.Err() != nil {
		s.writeQueryError(w, r, rows.Err())
		return
	}

	// 与 Python 的 db.get 等价：名称缺失时输出空串。
	names := map[string]string{}
	if len(companyIDs) > 0 {
		ids := make([]string, 0, len(companyIDs))
		for id := range companyIDs {
			ids = append(ids, id)
		}
		nameRows, err := s.writePool().Query(r.Context(),
			`SELECT id, name FROM companies WHERE id = ANY($1)`, ids)
		if err != nil {
			s.writeQueryError(w, r, err)
			return
		}
		for nameRows.Next() {
			var id, name string
			if err := nameRows.Scan(&id, &name); err != nil {
				nameRows.Close()
				s.writeQueryError(w, r, err)
				return
			}
			names[id] = name
		}
		nameRows.Close()
		if nameRows.Err() != nil {
			s.writeQueryError(w, r, nameRows.Err())
			return
		}
	}

	out := make([]introductionOut, 0, len(collected))
	for _, item := range collected {
		role := "target"
		if item.IntroducerID == membership.CompanyID {
			role = "introducer"
		}
		out = append(out, introductionOut{
			ID:                item.ID,
			Role:              role,
			IntroducerCompany: names[item.IntroducerID],
			TargetCompany:     names[item.TargetID],
			Text:              textPtr(item.Text),
			Reply:             textPtr(item.Reply),
			Status:            item.Status,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
