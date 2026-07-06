//go:build integration

package integration_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Medikong/services/services/user-service/internal/domain/user"
	userhttp "github.com/Medikong/services/services/user-service/internal/transport/http"
)

func TestOperationalRoutesIntegration(t *testing.T) {
	mux := http.NewServeMux()
	userhttp.RegisterRoutes(mux, user.NewService(user.NewMemoryRepository()))

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `"service":"user-service"`) {
		t.Fatalf("body %q does not contain user-service", body)
	}
}
