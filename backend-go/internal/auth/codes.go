package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
)

// Problem 是业务层可预期的错误，API 层负责转成统一错误信封。
type Problem struct {
	Status  int
	Code    string
	Message string
}

func (p *Problem) Error() string { return p.Message }

var (
	ErrNoPendingCode = &Problem{Status: 422, Code: "unprocessable", Message: "没有待使用的验证码"}
	ErrCodeExpired   = &Problem{Status: 422, Code: "unprocessable", Message: "验证码已过期，请重新获取"}
	ErrTooManyTries  = &Problem{Status: 422, Code: "unprocessable", Message: "尝试次数过多，请重新获取验证码"}
	ErrCodeWrong     = &Problem{Status: 422, Code: "unprocessable", Message: "验证码不正确"}
	ErrGrantInvalid  = &Problem{Status: 422, Code: "unprocessable", Message: "授权无效或已使用"}
	ErrGrantExpired  = &Problem{Status: 422, Code: "unprocessable", Message: "授权已过期，请重新申请"}
)

const (
	CodeTTL  = 15 * time.Minute
	GrantTTL = 30 * time.Minute
	maxTries = 5
)

func (s *Store) issueVerification(ctx context.Context, email, purpose, raw string,
	ttl time.Duration, now time.Time) error {
	id, err := NewID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO verification_codes
			(id, email, purpose, code_hash, expires_at, attempts, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 0, $6, $6)`,
		id, email, purpose, HashToken(raw), now.Add(ttl), now)
	return err
}

// IssueCode 生成 6 位数字验证码并入库，返回明文（调用方负责投递）。
func (s *Store) IssueCode(ctx context.Context, email, purpose string, now time.Time) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	code := leftPad6(n.Int64())
	if err := s.issueVerification(ctx, email, purpose, code, CodeTTL, now); err != nil {
		return "", err
	}
	return code, nil
}

func leftPad6(value int64) string {
	digits := [6]byte{'0', '0', '0', '0', '0', '0'}
	for index := 5; index >= 0 && value > 0; index-- {
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[:])
}

// IssueGrant 生成一次性授权令牌，返回明文（库中只存哈希）。
func (s *Store) IssueGrant(ctx context.Context, email, purpose string, now time.Time) (string, error) {
	token, err := NewToken("")
	if err != nil {
		return "", err
	}
	if err := s.issueVerification(ctx, email, purpose, token, GrantTTL, now); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeCode 校验并消费验证码，语义与 Python 的 codes.consume_code 一致。
func (s *Store) ConsumeCode(ctx context.Context, email, purpose, code string,
	now time.Time) error {
	var id, codeHash string
	var expiresAt time.Time
	var attempts int
	err := s.pool.QueryRow(ctx, `
		SELECT id, code_hash, expires_at, attempts
		FROM verification_codes
		WHERE email = $1 AND purpose = $2 AND consumed_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1`, email, purpose).Scan(&id, &codeHash, &expiresAt, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoPendingCode
	}
	if err != nil {
		return err
	}
	if expiresAt.Before(now) {
		return ErrCodeExpired
	}
	if attempts >= maxTries {
		return ErrTooManyTries
	}
	if subtle.ConstantTimeCompare([]byte(codeHash), []byte(HashToken(code))) != 1 {
		_, _ = s.pool.Exec(ctx, `UPDATE verification_codes SET attempts = attempts + 1,
			updated_at = $2 WHERE id = $1`, id, now)
		return ErrCodeWrong
	}
	_, err = s.pool.Exec(ctx, `UPDATE verification_codes
		SET attempts = attempts + 1, consumed_at = $2, updated_at = $2 WHERE id = $1`, id, now)
	return err
}

// ConsumeGrant 消费一次性授权令牌，返回它绑定的邮箱。
func (s *Store) ConsumeGrant(ctx context.Context, token, purpose string,
	now time.Time) (string, error) {
	var id, email string
	var expiresAt time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, expires_at
		FROM verification_codes
		WHERE purpose = $1 AND code_hash = $2 AND consumed_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1`, purpose, HashToken(token)).Scan(&id, &email, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrGrantInvalid
	}
	if err != nil {
		return "", err
	}
	if expiresAt.Before(now) {
		return "", ErrGrantExpired
	}
	if _, err := s.pool.Exec(ctx, `UPDATE verification_codes
		SET consumed_at = $2, updated_at = $2 WHERE id = $1`, id, now); err != nil {
		return "", err
	}
	return email, nil
}
