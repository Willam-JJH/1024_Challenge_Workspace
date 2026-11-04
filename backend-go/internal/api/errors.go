package api

import (
	"encoding/json"
	"net/http"
)

// APIError 与 Python 实现的错误信封一一对应：
//
//	{"error": {"code": "...", "message": "...", "details": {...}, "request_id": "..."}}
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

var (
	ErrNotFound       = &APIError{Status: http.StatusNotFound, Code: "not_found", Message: "资源不存在"}
	ErrUnauthorized   = &APIError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "需要登录"}
	ErrForbidden      = &APIError{Status: http.StatusForbidden, Code: "forbidden", Message: "没有权限"}
	ErrConflict       = &APIError{Status: http.StatusConflict, Code: "conflict", Message: "操作冲突"}
	ErrUnprocessable  = &APIError{Status: http.StatusUnprocessableEntity, Code: "unprocessable", Message: "请求不合法"}
	ErrNotImplemented = &APIError{Status: http.StatusNotImplemented, Code: "not_implemented", Message: "接口契约已就位、实现尚未迁移。"}
)

var errInternal = &APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "服务器内部错误"}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	WriteError(w, r, &APIError{Status: status, Code: code, Message: message})
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	RequestID string         `json:"request_id"`
}

func WriteError(w http.ResponseWriter, r *http.Request, apiErr *APIError) {
	details := apiErr.Details
	if details == nil {
		details = map[string]any{}
	}
	writeJSON(w, apiErr.Status, errorEnvelope{Error: errorBody{
		Code:      apiErr.Code,
		Message:   apiErr.Message,
		Details:   details,
		RequestID: RequestID(r),
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
