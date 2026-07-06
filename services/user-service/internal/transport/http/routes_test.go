package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
)

func TestRegisterRoutesIncludesOperationalEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, user.NewService(user.NewMemoryRepository()))

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `"service":"user-service"`) {
		t.Fatalf("body %q does not contain user-service", body)
	}
}

func TestMeInvalidPrincipalReturnsUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, user.NewService(user.NewMemoryRepository()))

	request := httptest.NewRequest(http.MethodGet, "/users/me", nil)
	request.Header.Set(headers.Principal, "not-base64")
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	assertErrorCode(t, response, http.StatusUnauthorized, "auth.invalid_principal")
}

func TestGetOtherUserWithoutRoleReturnsForbidden(t *testing.T) {
	mux := http.NewServeMux()
	store := user.NewMemoryRepository()
	svc := user.NewService(store)
	if _, err := svc.Ensure(context.Background(), user.EnsureInput{UserID: "user-2"}); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	RegisterRoutes(mux, svc)
	header, err := principal.EncodeHeader(principal.Principal{Type: principal.TypeUser, UserID: "user-1", Roles: []string{"customer"}})
	if err != nil {
		t.Fatalf("EncodeHeader() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/users/user-2", nil)
	request.Header.Set(headers.Principal, header)
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	assertErrorCode(t, response, http.StatusForbidden, "auth.forbidden")
}

func TestMeMissingUserIDReturnsUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	RegisterRoutes(mux, user.NewService(user.NewMemoryRepository()))
	header, err := principal.EncodeHeader(principal.Principal{Type: principal.TypeUser})
	if err != nil {
		t.Fatalf("EncodeHeader() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/users/me", nil)
	request.Header.Set(headers.Principal, header)
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	assertErrorCode(t, response, http.StatusUnauthorized, "auth.unauthorized")
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d, body=%s", response.Code, status, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != code {
		t.Fatalf("code = %q, want %q", body.Error.Code, code)
	}
}
