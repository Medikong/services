package httpcontract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

func TestWriteJSONUsesEnvelopeNoStoreAndRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/context", nil)
	request.Header.Set(requestIDHeader, "30d9fa85-0a18-4263-98b6-231dca5a6fb8")
	response := httptest.NewRecorder()

	WriteJSON(response, request, http.StatusOK, map[string]string{"status": "anonymous"})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get(cacheControlHeader); got != cacheControlNoStore {
		t.Fatalf("Cache-Control = %q, want %q", got, cacheControlNoStore)
	}
	if got := response.Header().Get(requestIDHeader); got != "30d9fa85-0a18-4263-98b6-231dca5a6fb8" {
		t.Fatalf("X-Request-Id = %q", got)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}

	var envelope Envelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Meta.RequestID != response.Header().Get(requestIDHeader) {
		t.Fatalf("meta requestId = %q", envelope.Meta.RequestID)
	}
}

func TestRequestIDMiddlewareReplacesInvalidInput(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIDFor(r) == "not-a-uuid" {
			t.Fatal("invalid request ID was preserved")
		}
		WriteNoContent(w, r)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(requestIDHeader, "not-a-uuid")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if _, err := uuid.Parse(response.Header().Get(requestIDHeader)); err != nil {
		t.Fatalf("response request ID is not UUID: %v", err)
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestWriteProblemUsesProblemDetailsContract(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	response := httptest.NewRecorder()

	WriteProblem(response, request, inputInvalid("additional_property"))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if got := response.Header().Get("Content-Type"); got != problemContentType {
		t.Fatalf("Content-Type = %q, want %q", got, problemContentType)
	}
	if got := response.Header().Get(cacheControlHeader); got != cacheControlNoStore {
		t.Fatalf("Cache-Control = %q", got)
	}
	var problem ProblemDetails
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != "AUTH_INPUT_INVALID" || problem.Type != "https://api.dropmong.example/problems/auth-input-invalid" {
		t.Fatalf("problem = %#v", problem)
	}
	if problem.RequestID != response.Header().Get(requestIDHeader) {
		t.Fatalf("request IDs differ: %#v", problem)
	}
	if len(problem.Violations) != 1 || problem.Violations[0].Reason != "additional_property" {
		t.Fatalf("violations = %#v", problem.Violations)
	}
}

func TestDecodeJSONIsStrict(t *testing.T) {
	type requestBody struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name   string
		body   string
		reason string
	}{
		{name: "unknown field", body: `{"name":"ok","extra":true}`, reason: "additional_property"},
		{name: "trailing value", body: `{"name":"ok"} {}`, reason: "trailing_data"},
		{name: "wrong type", body: `{"name":1}`, reason: "invalid_type"},
		{name: "empty body", body: "", reason: "missing_body"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
			response := httptest.NewRecorder()
			var target requestBody

			err := DecodeJSON(response, request, &target)
			if err == nil {
				t.Fatal("DecodeJSON() error = nil")
			}
			if err.Code != "AUTH_INPUT_INVALID" || len(err.Violations) != 1 || err.Violations[0].Reason != test.reason {
				t.Fatalf("error = %#v", err)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"ok"}`))
	request.Header.Set("Content-Type", "text/plain")
	if err := DecodeJSON(httptest.NewRecorder(), request, &requestBody{}); err == nil || err.Violations[0].Reason != "unsupported_media_type" {
		t.Fatalf("unsupported content type error = %#v", err)
	}
}

func TestCredentialExtractionRejectsAmbiguousCredentials(t *testing.T) {
	contract := testContract()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-dm_auth", Value: "web-flow"})
	request.Header.Set(authFlowTokenHeader, "mobile-flow")

	_, err := contract.PreAuthCredential(request)
	if err == nil || err.Kind != CredentialMultiple {
		t.Fatalf("pre-auth error = %#v", err)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "__Host-dm_session", Value: "web-session"})
	request.Header.Set("Authorization", "Bearer mobile-jwt")
	_, err = contract.SessionCredential(request)
	if err == nil || err.Kind != CredentialMultiple {
		t.Fatalf("session error = %#v", err)
	}
}

func TestCredentialExtractionKeepsChannelAndRejectsMalformedBearer(t *testing.T) {
	contract := testContract()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set(authFlowTokenHeader, "mobile-flow")
	preAuth, err := contract.PreAuthCredential(request)
	if err != nil || preAuth.Channel != CredentialChannelMobile || preAuth.Token != "mobile-flow" {
		t.Fatalf("pre-auth = %#v, error = %#v", preAuth, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Basic token")
	_, err = contract.SessionCredential(request)
	if err == nil || err.Kind != CredentialMalformed {
		t.Fatalf("malformed bearer error = %#v", err)
	}
}

func TestWebCSRFRequiresExactAllowedOriginAndVerifier(t *testing.T) {
	contract := testContract()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set(csrfHeader, "csrf-token")

	if err := contract.VerifyWebCSRF(request, func(token string) bool { return token == "csrf-token" }); err != nil {
		t.Fatalf("VerifyWebCSRF() error = %#v", err)
	}
	if err := contract.VerifyWebCSRF(request, func(string) bool { return false }); err == nil || err.Code != "AUTH_CSRF_INVALID" {
		t.Fatalf("invalid verifier error = %#v", err)
	}

	request.Header.Set("Origin", "https://app.example.test/path")
	if err := contract.VerifyWebCSRF(request, func(string) bool { return true }); err == nil || err.Code != "AUTH_CSRF_INVALID" {
		t.Fatalf("invalid origin error = %#v", err)
	}
}

func TestSessionCookieDeliveryAndClear(t *testing.T) {
	contract := testContract()
	response := httptest.NewRecorder()
	contract.IssueSessionCookie(response, "session-value", 3600)
	contract.ClearAuthFlowCookie(response)

	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookie count = %d", len(cookies))
	}
	if cookies[0].Name != "__Host-dm_session" || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].MaxAge != 3600 {
		t.Fatal("session cookie attributes do not match the contract")
	}
	if cookies[1].Name != "__Host-dm_auth" || cookies[1].MaxAge >= 0 {
		t.Fatal("auth-flow cookie was not cleared")
	}
}

func TestDevelopmentAccessTokenIsGated(t *testing.T) {
	contract := testContract()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(developmentAccessTokenHeader, "development-token")
	if err := contract.DevelopmentAccessToken(request); err != nil {
		t.Fatalf("DevelopmentAccessToken() error = %#v", err)
	}
	request.Header.Set(developmentAccessTokenHeader, "wrong-token")
	if err := contract.DevelopmentAccessToken(request); err == nil || err.Kind != CredentialRejected {
		t.Fatalf("rejected development token = %#v", err)
	}
}

func testContract() Contract {
	return NewContract(config.AuthConfig{
		SessionCookieName:  "__Host-dm_session",
		AuthFlowCookieName: "__Host-dm_auth",
		CookieSecure:       true,
		AllowedOrigins:     []string{"https://app.example.test"},
	}, config.DevelopmentConfig{
		Enabled:      true,
		RouteEnabled: true,
		AccessToken:  "development-token",
	})
}
