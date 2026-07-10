package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
)

func TestRequirePrincipalAndRole(t *testing.T) {
	header, err := principal.EncodeHeader(principal.Principal{
		Type:   principal.TypeUser,
		UserID: "user-1",
		Roles:  []string{"CUSTOMER"},
	})
	if err != nil {
		t.Fatalf("EncodeHeader() error = %v", err)
	}

	handler := RequirePrincipal(RequireRole("customer")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := Principal(r.Context()).UserID; got != "user-1" {
			t.Fatalf("Principal().UserID = %q, want user-1", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(headers.Principal, header)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestRequirePrincipalRejectsInvalidUser(t *testing.T) {
	for _, header := range []string{"", "not-base64", encodedPrincipal(t, principal.Principal{Type: principal.TypeUser})} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set(headers.Principal, header)
		RequirePrincipal(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("handler was called for invalid principal")
		})).ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("header %q status = %d, want %d", header, response.Code, http.StatusUnauthorized)
		}
	}
}

func TestRequireRoleRejectsMissingRole(t *testing.T) {
	header := encodedPrincipal(t, principal.Principal{Type: principal.TypeUser, UserID: "user-1"})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(headers.Principal, header)
	response := httptest.NewRecorder()
	RequirePrincipal(RequireRole("customer")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler was called without required role")
	}))).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func encodedPrincipal(t *testing.T, value principal.Principal) string {
	t.Helper()
	header, err := principal.EncodeHeader(value)
	if err != nil {
		t.Fatalf("EncodeHeader() error = %v", err)
	}
	return header
}
