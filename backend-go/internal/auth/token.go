package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// NewToken 生成对外可见的令牌：32 字节随机数的 URL-safe base64（无填充），
// 与 Python 的 secrets.token_urlsafe(32) 形态一致（43 个字符）。
func NewToken(prefix string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken 与 Python 的 hash_token 一致：SHA-256 小写十六进制。
func HashToken(token string) string { return hashToken(token) }

func sha256Sum(value string) [32]byte { return sha256.Sum256([]byte(value)) }

// NewID 生成 32 位十六进制主键，与 Python 的 uuid4().hex 一致。
func NewID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
