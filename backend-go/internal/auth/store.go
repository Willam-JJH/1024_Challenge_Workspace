package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID           string
	Email        string
	PasswordHash string
	DisplayName  string
	Locale       string
	Role         string
	Status       string
	CreatedAt    time.Time
}

type Principal struct {
	User      User
	Via       string // cookie | api_key
	SessionID string
	ApiKeyID  string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const userColumns = `id, email, password_hash, display_name, locale, role, status, created_at`

func scanUser(row pgx.Row) (*User, error) {
	var user User
	err := row.Scan(&user.ID, &user.Email, &user.PasswordHash, &user.DisplayName,
		&user.Locale, &user.Role, &user.Status, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = $1`, email))
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// CreateUser 插入账号；状态由调用方决定（pending 或 active）。
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, displayName, locale,
	status, role string, now time.Time) (*User, error) {
	id, err := NewID()
	if err != nil {
		return nil, err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO users
			(id, email, password_hash, display_name, locale, role, status, mode,
			 llm_blocked, digest_comments, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'both', false, true, $8, $8)`,
		id, email, passwordHash, displayName, locale, role, status, now)
	if err != nil {
		return nil, err
	}
	return s.UserByID(ctx, id)
}

func (s *Store) UpdateRegistration(ctx context.Context, userID, passwordHash, displayName,
	locale string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users SET password_hash = $2, display_name = $3, locale = $4, updated_at = $5
		WHERE id = $1`, userID, passwordHash, displayName, locale, now)
	return err
}

func (s *Store) SetUserStatus(ctx context.Context, userID, status string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET status = $2, updated_at = $3 WHERE id = $1`,
		userID, status, now)
	return err
}

func (s *Store) SetUserPassword(ctx context.Context, userID, passwordHash string,
	now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = $3
		WHERE id = $1`, userID, passwordHash, now)
	return err
}

func (s *Store) SessionPrincipal(ctx context.Context, tokenHash string) (*Principal, error) {
	var principal Principal
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, u.id, u.email, u.password_hash, u.display_name, u.locale, u.role,
			u.status, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL`, tokenHash).
		Scan(&principal.SessionID, &principal.User.ID, &principal.User.Email,
			&principal.User.PasswordHash, &principal.User.DisplayName, &principal.User.Locale,
			&principal.User.Role, &principal.User.Status, &principal.User.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	principal.Via = "cookie"
	return &principal, nil
}

func (s *Store) ApiKeyPrincipal(ctx context.Context, keyHash string) (*Principal, error) {
	var principal Principal
	err := s.pool.QueryRow(ctx, `
		SELECT k.id, u.id, u.email, u.password_hash, u.display_name, u.locale, u.role,
			u.status, u.created_at
		FROM api_keys k
		JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = $1 AND k.revoked_at IS NULL`, keyHash).
		Scan(&principal.ApiKeyID, &principal.User.ID, &principal.User.Email,
			&principal.User.PasswordHash, &principal.User.DisplayName, &principal.User.Locale,
			&principal.User.Role, &principal.User.Status, &principal.User.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	principal.Via = "api_key"
	return &principal, nil
}

func (s *Store) CreateSession(ctx context.Context, userID, tokenHash string, userAgent *string,
	clientIP *string, now time.Time) (string, error) {
	id, err := NewID()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, token_hash, user_agent, client_ip,
			last_seen_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $6)`,
		id, userID, tokenHash, userAgent, clientIP, now)
	if err != nil {
		return "", err
	}
	return id, nil
}

// TouchSession 与 Python 侧一致：一分钟内不重复写，避免每次请求都产生写放大。
func (s *Store) TouchSession(ctx context.Context, sessionID string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET last_seen_at = $2, updated_at = $2
		WHERE id = $1 AND (last_seen_at IS NULL OR last_seen_at <= $2::timestamp - interval '60 seconds')`,
		sessionID, now)
	return err
}

func (s *Store) TouchApiKey(ctx context.Context, keyID string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE api_keys SET last_used_at = $2, updated_at = $2
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at <= $2::timestamp - interval '60 seconds')`,
		keyID, now)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, sessionID string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = $2, updated_at = $2 WHERE id = $1`, sessionID, now)
	return err
}

func (s *Store) RevokeAllSessions(ctx context.Context, userID string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = $2, updated_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL`, userID, now)
	return err
}

// RevokeOtherSessions 保留当前会话，其余全部失效（改密后使用）。
func (s *Store) RevokeOtherSessions(ctx context.Context, userID, keepSessionID string,
	now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = $2, updated_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL AND id <> $3`,
		userID, now, keepSessionID)
	return err
}

type SessionRow struct {
	ID         string
	UserAgent  *string
	ClientIP   *string
	LastSeenAt time.Time
	CreatedAt  time.Time
}

func (s *Store) SessionsForUser(ctx context.Context, userID string) ([]SessionRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_agent, client_ip, last_seen_at, created_at
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]SessionRow, 0, 8)
	for rows.Next() {
		var row SessionRow
		if err := rows.Scan(&row.ID, &row.UserAgent, &row.ClientIP, &row.LastSeenAt,
			&row.CreatedAt); err != nil {
			return nil, err
		}
		sessions = append(sessions, row)
	}
	return sessions, rows.Err()
}

func (s *Store) RevokeSessionOfUser(ctx context.Context, userID, sessionID string,
	now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = $3, updated_at = $3
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, sessionID, userID, now)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
