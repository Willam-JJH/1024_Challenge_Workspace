package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// 与 Python 实现 app/core/security.py 保持完全一致。
const (
	scryptN = 1 << 14
	scryptR = 8
	scryptP = 1
	keyLen  = 32
)

func VerifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[0] != "scrypt" {
		return false
	}
	n, errN := strconv.Atoi(parts[1])
	r, errR := strconv.Atoi(parts[2])
	p, errP := strconv.Atoi(parts[3])
	if errN != nil || errR != nil || errP != nil {
		return false
	}
	salt, errSalt := hex.DecodeString(parts[4])
	want, errWant := hex.DecodeString(parts[5])
	if errSalt != nil || errWant != nil || len(salt) == 0 || len(want) == 0 {
		return false
	}
	got, err := scrypt.Key([]byte(password), salt, n, r, p, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// HashPassword 生成与 Python 相同的 scrypt$N$r$p$salt$digest 格式。
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return "", err
	}
	return "scrypt$" + strconv.Itoa(scryptN) + "$" + strconv.Itoa(scryptR) + "$" +
		strconv.Itoa(scryptP) + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(digest), nil
}

func hashToken(token string) string {
	sum := sha256Sum(token)
	return hex.EncodeToString(sum[:])
}
