package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"challenge-api/internal/config"
)

type openapiSpec struct {
	Paths map[string]map[string]json.RawMessage `json:"paths"`
}

var paramPattern = regexp.MustCompile(`\{[^}]+\}`)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.Config{
		Environment:   "test",
		CORSOrigins:   []string{"http://localhost:5173"},
		SessionCookie: "sid",
	}
	return New(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func loadSpec(t *testing.T) openapiSpec {
	t.Helper()
	raw, err := os.ReadFile("../../../docs/api/openapi.json")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var parsed openapiSpec
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse openapi: %v", err)
	}
	return parsed
}

// TestEverySpecOperationIsRouted 保证 OpenAPI 里的每个 operation 都有路由，
// 不会出现前端按契约调用却 404 的情况。
func TestEverySpecOperationIsRouted(t *testing.T) {
	spec := loadSpec(t)
	handler := testHandler(t)

	total := 0
	for path, operations := range spec.Paths {
		for method := range operations {
			switch method {
			case "get", "post", "put", "patch", "delete", "head", "options":
			default:
				continue
			}
			total++
			concrete := paramPattern.ReplaceAllString(path, "test")
			request := httptest.NewRequest(strings.ToUpper(method), concrete, nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code == http.StatusNotFound || recorder.Code == http.StatusMethodNotAllowed {
				t.Errorf("%s %s not routed (status %d)", strings.ToUpper(method), path, recorder.Code)
			}
		}
	}
	if total != len(stubOps) {
		t.Errorf("spec has %d operations, stubOps has %d", total, len(stubOps))
	}
}

func TestUnimplementedRouteReturns501Envelope(t *testing.T) {
	handler := testHandler(t)
	// 动态挑一个仍未实现的 operation，避免每迁移一个模块就要改测试。
	implemented := (&Server{}).implemented()
	pattern := ""
	for _, candidate := range stubOps {
		if _, ok := implemented[candidate]; ok {
			continue
		}
		if strings.Contains(candidate, "{") {
			continue
		}
		pattern = candidate
		break
	}
	if pattern == "" {
		t.Skip("所有 operation 都已实现")
	}
	method, path, _ := strings.Cut(pattern, " ")
	request := httptest.NewRequest(method, path, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", recorder.Code)
	}
	var envelope struct {
		Error struct {
			Code      string         `json:"code"`
			Message   string         `json:"message"`
			Details   map[string]any `json:"details"`
			RequestID string         `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if envelope.Error.Code != "not_implemented" {
		t.Errorf("code = %q, want not_implemented", envelope.Error.Code)
	}
	if envelope.Error.RequestID == "" {
		t.Error("request_id should be set")
	}
}

func TestUnknownPathReturnsEnvelope(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"not_found"`) {
		t.Errorf("body = %s", recorder.Body.String())
	}
}

func TestHealthWithoutDatabase(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var payload healthOut
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if payload.Status != "ok" || payload.Database != "unavailable" {
		t.Errorf("payload = %+v", payload)
	}
}
