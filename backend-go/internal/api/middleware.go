package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type ctxKey int

const requestIDKey ctxKey = iota

func RequestID(r *http.Request) string {
	if id, ok := r.Context().Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(buf[:])
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newRequestID()
		}
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

func withRecover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("panic", "request_id", RequestID(r), "path", r.URL.Path,
					"error", recovered)
				WriteError(w, r, &APIError{
					Status:  http.StatusInternalServerError,
					Code:    "internal_error",
					Message: "服务器内部错误",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func withAccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"latency_ms", float64(time.Since(started).Microseconds())/1000,
			"request_id", RequestID(r),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

// Unwrap 让 http.ResponseController 能找到底层的 Flusher（SSE 需要）。
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// withBodyBuffer 先把小块请求体读完再交给 handler。
// 否则 handler 不读 body 时，连接关闭阶段可能触发 RST，导致客户端丢响应
// （在 Windows 上实测会间歇出现 ConnectionResetError）。
func withBodyBuffer(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.ContentLength > 0 && r.ContentLength <= limit {
			data, err := io.ReadAll(io.LimitReader(r.Body, limit))
			_ = r.Body.Close()
			if err != nil {
				writeError(w, r, http.StatusBadRequest, "bad_request", "请求体读取失败")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
			r.ContentLength = int64(len(data))
		}
		next.ServeHTTP(w, r)
	})
}

func withCORS(origins []string, next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Add("Vary", "Origin")
			}
		}
		if r.Method == http.MethodOptions {
			if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
				w.Header().Set("Access-Control-Allow-Headers", requested)
			}
			w.Header().Set("Access-Control-Allow-Methods",
				"GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withErrorEnvelope 把 net/http 默认的 404/405 纯文本改写成统一错误信封，
// 前端只认这一个形状。
func withErrorEnvelope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rewriter := &envelopeWriter{ResponseWriter: w, request: r}
		next.ServeHTTP(rewriter, r)
	})
}

type envelopeWriter struct {
	http.ResponseWriter
	request    *http.Request
	swallowed  bool
	wroteJSON  bool
	statusCode int
}

func (e *envelopeWriter) WriteHeader(status int) {
	if e.wroteJSON {
		return
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		// 只有 net/http 默认的纯文本 404/405 才改写；handler 自己写好的信封不碰。
		if strings.HasPrefix(e.Header().Get("Content-Type"), "application/json") {
			e.statusCode = status
			e.ResponseWriter.WriteHeader(status)
			return
		}
		code := "not_found"
		message := "资源不存在"
		if status == http.StatusMethodNotAllowed {
			code = "method_not_allowed"
			message = "方法不允许"
		}
		e.swallowed = true
		e.wroteJSON = true
		e.Header().Del("X-Content-Type-Options")
		body, _ := json.Marshal(errorEnvelope{Error: errorBody{
			Code:      code,
			Message:   message,
			Details:   map[string]any{},
			RequestID: RequestID(e.request),
		}})
		e.Header().Set("Content-Type", "application/json")
		e.ResponseWriter.WriteHeader(status)
		_, _ = e.ResponseWriter.Write(body)
		return
	}
	e.statusCode = status
	e.ResponseWriter.WriteHeader(status)
}

func (e *envelopeWriter) Write(body []byte) (int, error) {
	if e.swallowed {
		return len(body), nil
	}
	return e.ResponseWriter.Write(body)
}

func (e *envelopeWriter) Unwrap() http.ResponseWriter { return e.ResponseWriter }
