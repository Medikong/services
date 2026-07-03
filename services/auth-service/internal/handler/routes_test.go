package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterRoutesIncludesOperationalEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `"service":"auth-service"`) {
		t.Fatalf("body %q does not contain auth-service", body)
	}
}
