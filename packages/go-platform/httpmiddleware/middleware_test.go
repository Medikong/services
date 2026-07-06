package httpmiddleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func TestStackGeneratesRequestIDAndReturnsHeader(t *testing.T) {
	logger.Configure(io.Discard, "test-service")
	handler := Stack(Config{ServiceName: "test-service"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestcontext.RequestID(r.Context()) == "" {
			t.Fatal("request id was not stored in context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if response.Header().Get(requestcontext.RequestIDHeader) == "" {
		t.Fatal("missing response request id")
	}
}

func TestStackPreservesRequestIDInErrorResponse(t *testing.T) {
	logger.Configure(io.Discard, "test-service")
	handler := Stack(Config{ServiceName: "test-service"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	request.Header.Set(requestcontext.RequestIDHeader, "req-boom")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get(requestcontext.RequestIDHeader); got != "req-boom" {
		t.Fatalf("response request id = %q, want req-boom", got)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["requestId"] != "req-boom" {
		t.Fatalf("body requestId = %v, want req-boom", body["requestId"])
	}
}

func TestRecoveryReraisesAfterResponseStarted(t *testing.T) {
	logger.Configure(io.Discard, "test-service")
	handler := Stack(Config{ServiceName: "test-service"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		panic("late boom")
	}))

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("panic was not re-raised")
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))
}

func TestMetricsUseRoutePatternAndAvoidHighCardinalityLabels(t *testing.T) {
	logger.Configure(io.Discard, "test-service")
	registry := metrics.NewRegistry()
	handler := Stack(Config{
		ServiceName: "test-service",
		Metrics:     registry,
		RoutePattern: func(*http.Request) string {
			return "/v1/auth/{sessionId}/revoke"
		},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/session-123/revoke", nil)
	request.Header.Set(requestcontext.RequestIDHeader, "req-123")

	handler.ServeHTTP(httptest.NewRecorder(), request)
	var metricsText strings.Builder
	registry.WritePrometheus(&metricsText)
	output := metricsText.String()

	if !strings.Contains(output, `http_route="/v1/auth/{sessionId}/revoke"`) {
		t.Fatalf("metrics do not include route pattern: %s", output)
	}
	for _, forbidden := range []string{"session-123", "req-123", "request_id", "user_id"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("metrics include high-cardinality value %q: %s", forbidden, output)
		}
	}
}
