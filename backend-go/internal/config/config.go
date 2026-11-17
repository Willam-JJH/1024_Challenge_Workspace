// Package config 读取 APP_ 前缀的环境变量，与 Python 实现的配置名保持一致。
package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
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

	// 限流（进程内滑动窗口；多副本部署时每个副本各算一份）
	RateLimitEnabled      bool
	RateLimitRPS          int
	RateLimitLoginPair    int
	RateLimitLoginIP      int
	RateLimitCodeEmail    int
	RateLimitCodeIP       int
	RateLimitCodeCooldown int
	RateLimitVerifyEmail  int

	// 定时广播 worker 的轮询间隔（秒）；0 = 关闭
	BroadcastPollSeconds int
	// SeasonCloseAfterDays 是比赛结束后多少天正式收口这一届：
	// 作品进前序作品、报名与投票关闭、参赛身份回落为普通用户。
	SeasonCloseAfterDays int
	// SeasonPollSeconds 是赛季任务的轮询间隔（秒）；0 = 关闭自动收口。
	SeasonPollSeconds int
	UploadDir         string
	MaxUploadBytes    int64
	// UploadQuotaBytes 是素材总占用上限；0 = 不限制。
	UploadQuotaBytes int64
	// JanitorIntervalSeconds 是清理任务间隔；0 = 关闭。
	JanitorIntervalSeconds int
	// UploadGraceHours 是中断上传与未完成素材的保留时长。
	UploadGraceHours int
	MailDriver       string
	MailFrom         string
	MailFromName     string
	SMTPHost         string
	SMTPPort         int
	SMTPUser         string
	SMTPPassword     string
	SMTPTLS          string
	SMTPTimeout      time.Duration
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

		RateLimitEnabled:      envBool("APP_RATE_LIMIT_ENABLED", true),
		RateLimitRPS:          envInt("APP_RATE_LIMIT_RPS", 0),
		RateLimitLoginPair:    envInt("APP_RATE_LIMIT_LOGIN_PAIR", 10),
		RateLimitLoginIP:      envInt("APP_RATE_LIMIT_LOGIN_IP", 60),
		RateLimitCodeEmail:    envInt("APP_RATE_LIMIT_CODE_EMAIL", 5),
		RateLimitCodeIP:       envInt("APP_RATE_LIMIT_CODE_IP", 30),
		RateLimitCodeCooldown: envInt("APP_RATE_LIMIT_CODE_COOLDOWN", 60),
		RateLimitVerifyEmail:  envInt("APP_RATE_LIMIT_VERIFY_EMAIL", 20),

		BroadcastPollSeconds: envInt("APP_BROADCAST_POLL_SECONDS", 30),
		SeasonCloseAfterDays: envInt("APP_SEASON_CLOSE_AFTER_DAYS", 30),
		SeasonPollSeconds:    envInt("APP_SEASON_POLL_SECONDS", 3600),

		UploadDir:      env("APP_UPLOAD_DIR", "uploads"),
		MaxUploadBytes: envInt64("APP_MAX_UPLOAD_BYTES", 100*1024*1024),

		UploadQuotaBytes:       envInt64("APP_UPLOAD_QUOTA_BYTES", 0),
		JanitorIntervalSeconds: envInt("APP_JANITOR_INTERVAL_SECONDS", 1800),
		UploadGraceHours:       envInt("APP_UPLOAD_GRACE_HOURS", 24),
		MailDriver:             env("APP_MAIL_DRIVER", "console"),
		MailFrom:               env("APP_MAIL_FROM", ""),
		MailFromName:           env("APP_MAIL_FROM_NAME", "1024 Challenge"),
		SMTPHost:               env("APP_SMTP_HOST", ""),
		SMTPPort:               envInt("APP_SMTP_PORT", 465),
		SMTPUser:               env("APP_SMTP_USER", ""),
		SMTPPassword:           env("APP_SMTP_PASSWORD", ""),
		SMTPTLS:                env("APP_SMTP_TLS", "ssl"),
		SMTPTimeout:            time.Duration(envInt("APP_SMTP_TIMEOUT", 10)) * time.Second,
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
