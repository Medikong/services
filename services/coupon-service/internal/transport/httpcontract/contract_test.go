package httpcontract

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-authz/principal"
	contractheaders "github.com/Medikong/services/packages/go-contracts/headers"
)

func TestDecodeJSONIsStrict(t *testing.T) {
	type requestBody struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name   string
		body   string
		typeID string
		reason string
	}{
		{name: "unknown field", body: `{"name":"ok","extra":true}`, typeID: "application/json", reason: "additional_property"},
		{name: "trailing JSON", body: `{"name":"ok"} {}`, typeID: "application/json", reason: "trailing_data"},
		{name: "wrong type", body: `{"name":1}`, typeID: "application/json", reason: "invalid_type"},
		{name: "empty body", body: "", typeID: "application/json", reason: "missing_body"},
		{name: "wrong content type", body: `{"name":"ok"}`, typeID: "text/plain", reason: "unsupported_media_type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", test.typeID)
			var target requestBody
			problem := DecodeJSON(httptest.NewRecorder(), request, &target)
			if problem == nil || len(problem.Violations) != 1 || problem.Violations[0].Reason != test.reason {
				t.Fatalf("DecodeJSON() problem = %#v", problem)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`"`+strings.Repeat("x", MaxJSONBodyBytes+1)+`"`))
	request.Header.Set("Content-Type", "application/json")
	if problem := DecodeJSON(httptest.NewRecorder(), request, &requestBody{}); problem == nil || problem.Violations[0].Reason != "body_too_large" {
		t.Fatalf("oversize body problem = %#v", problem)
	}
}

func TestAuthenticateSeparatesPublicAndWorkloadPrincipals(t *testing.T) {
	contract, err := New([]string{"https://app.example.test"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		name       string
		boundary   Boundary
		mutation   bool
		principal  principal.Principal
		origin     string
		csrf       string
		wantStatus int
	}{
		{name: "mobile user mutation", boundary: BoundaryPublic, mutation: true, principal: principal.Principal{Type: principal.TypeUser, UserID: "user-1", ClientType: "mobile"}, wantStatus: http.StatusNoContent},
		{name: "web user mutation", boundary: BoundaryPublic, mutation: true, principal: principal.Principal{Type: principal.TypeUser, UserID: "user-1", ClientType: "web"}, origin: "https://app.example.test", csrf: "csrf-token", wantStatus: http.StatusNoContent},
		{name: "web user missing origin", boundary: BoundaryPublic, mutation: true, principal: principal.Principal{Type: principal.TypeUser, UserID: "user-1", ClientType: "web"}, csrf: "csrf-token", wantStatus: http.StatusForbidden},
		{name: "service on public route", boundary: BoundaryPublic, principal: principal.Principal{Type: principal.TypeService, ServiceID: "checkout-service"}, wantStatus: http.StatusUnauthorized},
		{name: "user on workload route", boundary: BoundaryWorkload, principal: principal.Principal{Type: principal.TypeUser, UserID: "user-1"}, wantStatus: http.StatusUnauthorized},
		{name: "service without identity", boundary: BoundaryWorkload, principal: principal.Principal{Type: principal.TypeService}, wantStatus: http.StatusUnauthorized},
		{name: "mixed workload identity", boundary: BoundaryWorkload, principal: principal.Principal{Type: principal.TypeService, ServiceID: "checkout-service", UserID: "user-1"}, wantStatus: http.StatusUnauthorized},
		{name: "mixed public identity", boundary: BoundaryPublic, principal: principal.Principal{Type: principal.TypeUser, UserID: "user-1", ServiceID: "checkout-service"}, wantStatus: http.StatusUnauthorized},
		{name: "identified service", boundary: BoundaryWorkload, principal: principal.Principal{Type: principal.TypeService, ServiceID: "checkout-service"}, wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Header.Set(contractheaders.Principal, encodePrincipal(t, test.principal))
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.csrf != "" {
				request.Header.Set(CSRFHeader, test.csrf)
			}
			response := httptest.NewRecorder()
			handler := RequestIDMiddleware(contract.Authenticate(test.boundary, test.mutation)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if Principal(r.Context()).Type == "" {
					t.Fatal("principal is missing from context")
				}
				w.WriteHeader(http.StatusNoContent)
			})))
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestAuthenticateDoesNotTrustRawCookieOrBearer(t *testing.T) {
	contract, err := New(nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer raw-jwt")
	request.AddCookie(&http.Cookie{Name: "__Host-dm_session", Value: "raw-session"})
	response := httptest.NewRecorder()
	RequestIDMiddleware(contract.Authenticate(BoundaryPublic, false)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler called without X-Principal")
	}))).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestReadHeadersValidatesOperationHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set(contractheaders.IdempotencyKey, "operation-key-1234")
	request.Header.Set(ApprovalRefHeader, "approval:123")
	request.Header.Set(CaseRefHeader, "case:123")
	request.Header.Set(TraceparentHeader, "00-12345678901234567890123456789012-1234567890123456-01")
	headers, problem := ReadHeaders(request, HeaderRules{Idempotency: true, Approval: true, Case: true})
	if problem != nil {
		t.Fatalf("ReadHeaders() problem = %#v", problem)
	}
	if headers.IdempotencyKey != "operation-key-1234" || headers.ApprovalRef != "approval:123" || headers.CaseRef != "case:123" {
		t.Fatalf("headers = %#v", headers)
	}

	query := httptest.NewRequest(http.MethodGet, "/", nil)
	query.Header.Set(contractheaders.IdempotencyKey, "operation-key-1234")
	if _, problem := ReadHeaders(query, HeaderRules{}); problem == nil || problem.Violations[0].Reason != "unexpected_header" {
		t.Fatalf("query Idempotency-Key problem = %#v", problem)
	}
}

func TestWriteProblemUsesProblemDetails(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	response := httptest.NewRecorder()
	RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, InputInvalid("body", "invalid_json"))
	})).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != ProblemContentType {
		t.Fatalf("response = %d %#v", response.Code, response.Header())
	}
	var problem ProblemDetails
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatalf("decode ProblemDetails: %v", err)
	}
	if problem.Code != "COUPON_INPUT_INVALID" || problem.RequestID != response.Header().Get(RequestIDHeader) {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestWriteProblemAddsRetryAfterForRetryableErrors(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	WriteProblem(response, request, internalProblem(context.DeadlineExceeded))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("response = %d %#v", response.Code, response.Header())
	}
}

func TestTimeoutUsesCouponProblemContract(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler := Timeout(time.Millisecond)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Type") != ProblemContentType || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("response = %d %#v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var problem ProblemDetails
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "COUPON_DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestWriteJSONUsesOpenAPIAsOfMeta(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"}, "2026-07-12T04:00:00Z")
	})).ServeHTTP(response, request)

	var envelope map[string]any
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response envelope: %v", err)
	}
	meta, ok := envelope["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta = %#v", envelope["meta"])
	}
	if meta["asOf"] != "2026-07-12T04:00:00Z" {
		t.Fatalf("meta.asOf = %#v", meta["asOf"])
	}
	if _, exists := meta["evaluationAsOf"]; exists {
		t.Fatalf("meta contains non-contract evaluationAsOf: %#v", meta)
	}
}

func encodePrincipal(t *testing.T, value principal.Principal) string {
	t.Helper()
	header, err := principal.EncodeHeader(value)
	if err != nil {
		t.Fatalf("EncodeHeader() error = %v", err)
	}
	return header
}
