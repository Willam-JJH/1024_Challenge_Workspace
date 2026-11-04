package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// 与 Python services/botcheck.py 一致：HMAC 签名挑战 + 客户端找 nonce
// 使 sha256(challenge+nonce) 的前 N 位为零。
const (
	DefaultDifficulty = 18
	BotChallengeTTL   = 600 * time.Second
)

func signPayload(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// IssueBotChallenge 返回 (challenge, difficulty, expiresAt)。
func IssueBotChallenge(secret string, now time.Time) (string, int, int64, error) {
	nonce, err := NewToken("")
	if err != nil {
		return "", 0, 0, err
	}
	expiresAt := now.Add(BotChallengeTTL).Unix()
	payload := nonce + "." + strconv.Itoa(DefaultDifficulty) + "." + strconv.FormatInt(expiresAt, 10)
	raw := payload + "." + signPayload(secret, payload)
	return base64.URLEncoding.EncodeToString([]byte(raw)), DefaultDifficulty, expiresAt, nil
}

// VerifyBotChallenge 校验签名、有效期与工作量；不通过返回 Problem（422）。
func VerifyBotChallenge(secret, challenge, nonce string, now time.Time) error {
	decoded, err := base64.URLEncoding.DecodeString(challenge)
	if err != nil {
		return &Problem{Status: 422, Code: "unprocessable", Message: "人机校验格式不正确"}
	}
	parts := strings.Split(string(decoded), ".")
	if len(parts) != 4 {
		return &Problem{Status: 422, Code: "unprocessable", Message: "人机校验格式不正确"}
	}
	noncePart, difficultyText, expiresText, signature := parts[0], parts[1], parts[2], parts[3]
	payload := noncePart + "." + difficultyText + "." + expiresText
	if !hmac.Equal([]byte(signature), []byte(signPayload(secret, payload))) {
		return &Problem{Status: 422, Code: "unprocessable", Message: "人机校验无效"}
	}
	expiresAt, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil || expiresAt < now.Unix() {
		return &Problem{Status: 422, Code: "unprocessable", Message: "人机校验已过期，请重试"}
	}
	difficulty, err := strconv.Atoi(difficultyText)
	if err != nil {
		return &Problem{Status: 422, Code: "unprocessable", Message: "人机校验格式不正确"}
	}
	digest := sha256.Sum256([]byte(challenge + nonce))
	if leadingZeroBits(digest[:]) < difficulty {
		return &Problem{Status: 422, Code: "unprocessable", Message: "人机校验未通过"}
	}
	return nil
}

func leadingZeroBits(digest []byte) int {
	bits := 0
	for _, b := range digest {
		if b == 0 {
			bits += 8
			continue
		}
		value := int(b)
		zero := 0
		for value > 0 {
			value >>= 1
			zero++
		}
		bits += 8 - zero
		break
	}
	return bits
}
