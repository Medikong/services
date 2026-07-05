package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authservice "github.com/Medikong/services/services/auth-service/internal/service"
	"github.com/Medikong/services/services/auth-service/internal/store/memory"
)

func TestRegisterRoutesIncludesOperationalEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, authservice.New(memory.New()))

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

func TestIntrospectMissingTokenReturnsUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, authservice.New(memory.New()))

	request := httptest.NewRequest(http.MethodPost, "/auth/introspect", nil)
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "auth.missing_token" {
		t.Fatalf("code = %q", body.Error.Code)
	}
}
