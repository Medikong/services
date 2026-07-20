package httpinterface

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
)

func TestRouterPreservesNotFoundAndMethodNotAllowedResponses(t *testing.T) {
	router := NewRouter("auth-service", time.Second, operational.New("auth-service", nil))
	router.Get("/known", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name   string
		method string
		path   string
		status int
		code   string
	}{
		{name: "not found", method: http.MethodGet, path: "/missing", status: http.StatusNotFound, code: "AUTH_ROUTE_NOT_FOUND"},
		{name: "method not allowed", method: http.MethodPost, path: "/known", status: http.StatusMethodNotAllowed, code: "AUTH_METHOD_NOT_ALLOWED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := serveRouterRequest(router, test.method, test.path, "")
			assertRouterError(t, response, test.status, test.code)
		})
	}
}

func TestRouterPreservesRequestIDWhenDraining(t *testing.T) {
	health := operational.New("auth-service", nil)
	router := NewRouter("auth-service", time.Second, health)
	router.Get("/work", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	health.BeginDrain()

	requestID := "30d9fa85-0a18-4263-98b6-231dca5a6fb8"
	response := serveRouterRequest(router, http.MethodGet, "/work", requestID)

	assertRouterError(t, response, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE")
	if got := response.Header().Get(httputil.IDHeader); got != requestID {
		t.Fatalf("request ID = %q, want %q", got, requestID)
	}
	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}

func TestRouterPreservesRecoveryAndTimeoutMiddleware(t *testing.T) {
	router := NewRouter("auth-service", time.Millisecond, operational.New("auth-service", nil))
	router.Get("/panic", func(http.ResponseWriter, *http.Request) {
		panic("router test panic")
	})
	router.Get("/timeout", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	for _, path := range []string{"/panic", "/timeout"} {
		response := serveRouterRequest(router, http.MethodGet, path, "")
		assertRouterError(t, response, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE")
	}
}

func serveRouterRequest(handler http.Handler, method, path, requestID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if requestID != "" {
		request.Header.Set(httputil.IDHeader, requestID)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertRouterError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d", response.Code, status)
	}
	if response.Header().Get(httputil.IDHeader) == "" {
		t.Fatal("request ID middleware was not applied")
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	var apiError httputil.Error
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if apiError.Status != status || apiError.Code != code {
		t.Fatalf("error response = (%d, %q), want (%d, %q)", apiError.Status, apiError.Code, status, code)
	}
	if apiError.RequestID != response.Header().Get(httputil.IDHeader) {
		t.Fatalf("body request ID = %q, header = %q", apiError.RequestID, response.Header().Get(httputil.IDHeader))
	}
}
