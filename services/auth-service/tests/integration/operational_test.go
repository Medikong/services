//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Medikong/services/packages/go-platform/operational"
	authhttp "github.com/Medikong/services/services/auth-service/internal/http"
)

func TestReadyzChecksDatabase(t *testing.T) {
	services := newTestAuthServices(t, context.Background())
	mux := http.NewServeMux()
	authhttp.RegisterRoutes(mux, authhttp.Services{
		Accounts: services.accounts,
		Sessions: services.sessions,
		Dev:      services.dev,
	}, map[string]operational.Check{
		"database": services.db.Ping,
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `"database":"ok"`) {
		t.Fatalf("body %q does not contain database readiness", body)
	}
}
