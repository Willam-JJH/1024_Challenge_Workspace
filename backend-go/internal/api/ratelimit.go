package api

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 与 Python 的 app/core/ratelimit.py 语义一致：按 key 记录事件时刻，
// 窗口内事件数达到 limit 就拒绝。计数只在进程内。

const (
	loginWindow    = 15 * time.Minute
	codeWindow     = time.Hour
	verifyWindow   = time.Hour
	sweepInterval  = time.Minute
	staleAfter     = time.Hour
	sweepThreshold = 4096
)

type slidingWindow struct {
	mu        sync.Mutex
	events    map[string][]time.Time
	lastSweep time.Time
}

func newSlidingWindow() *slidingWindow {
	return &slidingWindow{events: make(map[string][]time.Time), lastSweep: time.Now()}
}

// allow 记录一次尝试，返回 (是否放行, 拒绝时建议等待的秒数)。
func (s *slidingWindow) allow(key string, limit int, window time.Duration) (bool, int) {
	if limit <= 0 || window <= 0 {
		return true, 0
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maybeSweep(now)

	cutoff := now.Add(-window)
	events := s.events[key]
	kept := events[:0]
	for _, at := range events {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= limit {
		s.events[key] = kept
		retry := int(math.Ceil(kept[0].Add(window).Sub(now).Seconds()))
		if retry < 1 {
			retry = 1
		}
		return false, retry
	}
	s.events[key] = append(kept, now)
	return true, 0
}

func (s *slidingWindow) maybeSweep(now time.Time) {
	if now.Sub(s.lastSweep) < sweepInterval && len(s.events) < sweepThreshold {
		return
	}
	s.lastSweep = now
	stale := now.Add(-staleAfter)
	for key, events := range s.events {
		if len(events) == 0 || !events[len(events)-1].After(stale) {
			delete(s.events, key)
		}
	}
}

func (s *Server) denyRateLimited(w http.ResponseWriter, r *http.Request, retry int) {
	if retry < 1 {
		retry = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	WriteError(w, r, &APIError{
		Status:  http.StatusTooManyRequests,
		Code:    "rate_limited",
		Message: "请求过于频繁，请稍后再试",
		Details: map[string]any{"retry_after": retry},
	})
}

// requestLimit 是全域每 IP 请求速率的上限；RateLimitRPS <= 0 时关闭。
func (s *Server) requestLimit(w http.ResponseWriter, r *http.Request) bool {
	if !s.cfg.RateLimitEnabled || s.cfg.RateLimitRPS <= 0 {
		return true
	}
	ok, retry := s.limiter.allow("rps:"+s.clientIP(r), s.cfg.RateLimitRPS, time.Second)
	if !ok {
		s.denyRateLimited(w, r, retry)
		return false
	}
	return true
}

// recordLoginFailure 只统计失败登录；返回 false 表示已写出 429。
func (s *Server) recordLoginFailure(w http.ResponseWriter, r *http.Request, email string) bool {
	if !s.cfg.RateLimitEnabled {
		return true
	}
	ok, retry := s.limiter.allow("login:ip:"+s.clientIP(r), s.cfg.RateLimitLoginIP, loginWindow)
	if ok {
		ok, retry = s.limiter.allow("login:pair:"+s.clientIP(r)+"|"+email,
			s.cfg.RateLimitLoginPair, loginWindow)
	}
	if !ok {
		s.denyRateLimited(w, r, retry)
		return false
	}
	return true
}

// allowCodeSend 是发验证码前的频控：两次间隔、每邮箱每小时、每 IP 每小时。
func (s *Server) allowCodeSend(w http.ResponseWriter, r *http.Request, email string) bool {
	if !s.cfg.RateLimitEnabled {
		return true
	}
	cooldown := time.Duration(s.cfg.RateLimitCodeCooldown) * time.Second
	ok, retry := s.limiter.allow("code:cooldown:"+email, 1, cooldown)
	if ok {
		ok, retry = s.limiter.allow("code:email:"+email, s.cfg.RateLimitCodeEmail, codeWindow)
	}
	if ok {
		ok, retry = s.limiter.allow("code:ip:"+s.clientIP(r), s.cfg.RateLimitCodeIP, codeWindow)
	}
	if !ok {
		s.denyRateLimited(w, r, retry)
		return false
	}
	return true
}

// allowVerify 限制同一邮箱的验证码校验次数，防止换着新码反复猜。
func (s *Server) allowVerify(w http.ResponseWriter, r *http.Request, email string) bool {
	if !s.cfg.RateLimitEnabled {
		return true
	}
	ok, retry := s.limiter.allow("verify:email:"+email, s.cfg.RateLimitVerifyEmail, verifyWindow)
	if !ok {
		s.denyRateLimited(w, r, retry)
		return false
	}
	return true
}

// withRateLimit 是全域每 IP 请求速率中间件；健康检查始终放行。
func withRateLimit(s *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/health") {
			next.ServeHTTP(w, r)
			return
		}
		if !s.requestLimit(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}
