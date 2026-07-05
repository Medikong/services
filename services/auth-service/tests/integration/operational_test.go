//go:build integration

package integration_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Medikong/services/services/auth-service/internal/handler"
	authservice "github.com/Medikong/services/services/auth-service/internal/service"
	"github.com/Medikong/services/services/auth-service/internal/store/memory"
)

func TestOperationalRoutesIntegration(t *testing.T) {
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, authservice.New(memory.New()))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `"service":"auth-service"`) {
		t.Fatalf("body %q does not contain auth-service", body)
	}
}
