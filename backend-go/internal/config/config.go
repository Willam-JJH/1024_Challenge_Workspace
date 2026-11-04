// Package config 读取 APP_ 前缀的环境变量，与 Python 实现的配置名保持一致。
package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Environment      string
	Port             string
	APIBasePath      string
	DatabaseURL      string
	ReadDatabaseURL  string
	DBMaxConns       int32
	CORSOrigins      []string
	SessionCookie    string
	SessionTTLDays   int
	CookieSecure     bool
	CookieDomain     string
	PublicDomain     string
	BehindCloudflare bool
	TrustedProxies   []string
	CurrentEventSlug string
	RequireBotCheck  bool
	BotCheckSecret   string
	WalletMaxVotes   int
	UploadDir        string
	MaxUploadBytes   int64
}

func Load() Config {
	return Config{
		Environment:      env("APP_ENVIRONMENT", "dev"),
		Port:             env("APP_PORT", "8000"),
		APIBasePath:      env("APP_API_PREFIX", "/api/v1"),
		DatabaseURL:      normalizeDSN(env("APP_DATABASE_URL", "postgres://app:app@127.0.0.1:5432/challenge")),
		ReadDatabaseURL:  normalizeDSN(env("APP_READ_DATABASE_URL", "")),
		DBMaxConns:       int32(envInt("APP_DB_MAX_CONNS", 20)),
		CORSOrigins:      envList("APP_CORS_ORIGINS", []string{"http://localhost:5173"}),
		SessionCookie:    env("APP_SESSION_COOKIE", "sid"),
		SessionTTLDays:   envInt("APP_SESSION_TTL_DAYS", 14),
		CookieSecure:     envBool("APP_COOKIE_SECURE", false),
		CookieDomain:     env("APP_COOKIE_DOMAIN", ""),
		PublicDomain:     env("APP_PUBLIC_DOMAIN", "example.com"),
		BehindCloudflare: envBool("APP_BEHIND_CLOUDFLARE", false),
		TrustedProxies:   envList("APP_TRUSTED_PROXIES", nil),
		CurrentEventSlug: env("APP_CURRENT_EVENT_SLUG", "1024-challenge"),
		RequireBotCheck:  envBool("APP_REQUIRE_BOT_CHECK", false),
		BotCheckSecret:   env("APP_BOT_CHECK_SECRET", "dev-only-change-me"),
		WalletMaxVotes:   envInt("APP_WALLET_MAX_VOTES", 3),
		UploadDir:        env("APP_UPLOAD_DIR", "uploads"),
		MaxUploadBytes:   envInt64("APP_MAX_UPLOAD_BYTES", 100*1024*1024),
	}
}

// normalizeDSN 让 Go 服务能直接复用 Python 侧的 APP_DATABASE_URL，
// 其中带的是 SQLAlchemy 的驱动前缀。
func normalizeDSN(dsn string) string {
	dsn = strings.Replace(dsn, "postgresql+psycopg://", "postgres://", 1)
	dsn = strings.Replace(dsn, "postgresql+asyncpg://", "postgres://", 1)
	return dsn
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// envList 接受 JSON 数组（与 Python 的 pydantic-settings 写法一致）或逗号分隔。
func envList(key string, fallback []string) []string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	if strings.HasPrefix(strings.TrimSpace(value), "[") {
		var parsed []string
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			return parsed
		}
		return fallback
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
