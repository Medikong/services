//go:build integration

package integration_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Medikong/services/packages/go-platform/operational"
)

func TestOperationalHandlerIntegration(t *testing.T) {
	mux := http.NewServeMux()
	operational.New("platform-test", nil).Register(mux)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `"status":"ready"`) {
		t.Fatalf("body %q does not contain ready status", body)
	}
}
