package httputil

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httpauth"
	"github.com/google/uuid"
)

func TestWriteJSONUsesEnvelopeNoStoreAndRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/context", nil)
	request.Header.Set(IDHeader, "30d9fa85-0a18-4263-98b6-231dca5a6fb8")
	response := httptest.NewRecorder()

	WriteJSON(response, request, http.StatusOK, map[string]string{"status": "anonymous"})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get(cacheControlHeader); got != cacheControlNoStore {
		t.Fatalf("Cache-Control = %q, want %q", got, cacheControlNoStore)
	}
	if got := response.Header().Get(IDHeader); got != "30d9fa85-0a18-4263-98b6-231dca5a6fb8" {
		t.Fatalf("X-Request-Id = %q", got)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var envelope Envelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Meta.RequestID != response.Header().Get(IDHeader) {
		t.Fatalf("meta requestId = %q", envelope.Meta.RequestID)
	}
}

func TestIDMiddlewareReplacesInvalidInput(t *testing.T) {
	handler := IDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ID(r) == "not-a-uuid" {
			t.Fatal("invalid request ID was preserved")
		}
		WriteNoContent(w, r)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(IDHeader, "not-a-uuid")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if _, err := uuid.Parse(response.Header().Get(IDHeader)); err != nil {
		t.Fatalf("response request ID is not UUID: %v", err)
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestWriteErrorUsesPublicErrorShape(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	response := httptest.NewRecorder()

	WriteError(response, request, inputInvalid("additional_property"))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := response.Header().Get(cacheControlHeader); got != cacheControlNoStore {
		t.Fatalf("Cache-Control = %q", got)
	}
	body := response.Body.Bytes()
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode error fields: %v", err)
	}
	if len(fields) != 4 {
		t.Fatalf("error fields = %#v", fields)
	}
	var apiError Error
	if err := json.Unmarshal(body, &apiError); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if apiError.Status != http.StatusBadRequest || apiError.Code != "AUTH_INPUT_INVALID" || apiError.Message != "입력값을 확인한 뒤 다시 시도해주세요." {
		t.Fatalf("error = %#v", apiError)
	}
	if apiError.RequestID != response.Header().Get(IDHeader) {
		t.Fatalf("request IDs differ: %#v", apiError)
	}
	for _, private := range []string{"additional_property", "invalid request", "reason"} {
		if strings.Contains(string(body), private) {
			t.Fatalf("response exposed internal value %q: %s", private, body)
		}
	}
}

func TestWriteErrorMapsFailureContract(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int
		code    string
		message string
	}{
		{name: "invalid", err: failure.Invalid("AUTH_INPUT_INVALID", "입력값을 확인해주세요."), status: http.StatusBadRequest, code: "AUTH_INPUT_INVALID", message: "입력값을 확인해주세요."},
		{name: "unauthenticated", err: failure.Unauthenticated("AUTH_SESSION_REQUIRED", "인증 정보가 필요합니다."), status: http.StatusUnauthorized, code: "AUTH_SESSION_REQUIRED", message: "인증 정보가 필요합니다."},
		{name: "forbidden", err: failure.Forbidden("AUTH_FORBIDDEN", "권한이 없습니다."), status: http.StatusForbidden, code: "AUTH_FORBIDDEN", message: "권한이 없습니다."},
		{name: "not found", err: failure.NotFound("AUTH_NOT_FOUND", "대상을 찾을 수 없습니다."), status: http.StatusNotFound, code: "AUTH_NOT_FOUND", message: "대상을 찾을 수 없습니다."},
		{name: "conflict", err: failure.Conflict("AUTH_CONFLICT", "현재 상태와 충돌합니다."), status: http.StatusConflict, code: "AUTH_CONFLICT", message: "현재 상태와 충돌합니다."},
		{name: "unavailable", err: failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", "잠시 뒤 다시 시도해주세요."), status: http.StatusServiceUnavailable, code: "AUTH_SERVICE_UNAVAILABLE", message: "잠시 뒤 다시 시도해주세요."},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			response := httptest.NewRecorder()

			WriteError(response, request, test.err)

			var got Error
			if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if response.Code != test.status || got.Status != test.status || got.Code != test.code || got.Message != test.message {
				t.Fatalf("response = (%d, %#v), want status=%d code=%q message=%q", response.Code, got, test.status, test.code, test.message)
			}
		})
	}
}

func TestWriteErrorPreservesCodeSpecificStatus(t *testing.T) {
	tests := []struct {
		code   string
		status int
	}{
		{code: "AUTH_INTENT_EXPIRED", status: http.StatusGone},
		{code: "AUTH_CHALLENGE_EXPIRED", status: http.StatusGone},
		{code: "AUTH_REFRESH_RETRY_EXPIRED", status: http.StatusGone},
		{code: "AUTH_SESSION_DELIVERY_EXPIRED", status: http.StatusGone},
		{code: "AUTH_REGISTRATION_EXPIRED", status: http.StatusGone},
		{code: "AUTH_PASSWORD_RESET_GRANT_EXPIRED", status: http.StatusGone},
		{code: "AUTH_IDENTITY_LINK_INTENT_EXPIRED", status: http.StatusGone},
		{code: "AUTH_VIRTUAL_MESSAGE_UNAVAILABLE", status: http.StatusGone},
		{code: "AUTH_REAUTHENTICATION_PROOF_INVALID", status: http.StatusGone},
		{code: "AUTH_POLICY_PRECONDITION_FAILED", status: http.StatusPreconditionFailed},
		{code: "AUTH_RESOURCE_PRECONDITION_FAILED", status: http.StatusPreconditionFailed},
		{code: "AUTH_PASSWORD_POLICY_NOT_MET", status: http.StatusUnprocessableEntity},
		{code: "AUTH_ACCOUNT_LOCKED", status: http.StatusLocked},
	}

	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			response := httptest.NewRecorder()
			WriteError(response, request, failure.Conflict(test.code, "공개 메시지"))

			var got Error
			if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if response.Code != test.status || got.Status != test.status || got.Code != test.code || got.Message != "공개 메시지" {
				t.Fatalf("response = (%d, %#v), want status=%d code=%q", response.Code, got, test.status, test.code)
			}
		})
	}
}

func TestWriteCredentialErrorUsesFailureContract(t *testing.T) {
	tests := []struct {
		name   string
		err    *httpauth.Error
		status int
		code   string
	}{
		{name: "missing", err: nil, status: http.StatusUnauthorized, code: "AUTH_SESSION_REQUIRED"},
		{name: "malformed", err: &httpauth.Error{Kind: httpauth.Malformed}, status: http.StatusUnauthorized, code: "AUTH_SESSION_REQUIRED"},
		{name: "multiple", err: &httpauth.Error{Kind: httpauth.Multiple}, status: http.StatusBadRequest, code: "AUTH_MULTIPLE_CREDENTIALS"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			response := httptest.NewRecorder()
			WriteCredentialError(response, request, test.err)

			var got Error
			if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if response.Code != test.status || got.Status != test.status || got.Code != test.code {
				t.Fatalf("response = (%d, %#v), want status=%d code=%q", response.Code, got, test.status, test.code)
			}
		})
	}
}

func TestWriteErrorExposesOnlyOopsPublicMessage(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	err := httpapi.BadRequest("AUTH_INPUT_INVALID").
		Public("공개 메시지").
		With("secret", "raw-token").
		New("private failure")

	WriteError(response, request, err)

	body := response.Body.String()
	if !strings.Contains(body, "공개 메시지") {
		t.Fatalf("public message is missing: %s", body)
	}
	for _, private := range []string{"raw-token", "private failure", "secret"} {
		if strings.Contains(body, private) {
			t.Fatalf("response exposed %q: %s", private, body)
		}
	}
}

func TestDecodeJSONIsStrict(t *testing.T) {
	type requestBody struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"name":"ok","extra":true}`},
		{name: "trailing value", body: `{"name":"ok"} {}`},
		{name: "wrong type", body: `{"name":1}`},
		{name: "empty body", body: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
			var target requestBody
			err := DecodeJSON(httptest.NewRecorder(), request, &target)
			if err == nil {
				t.Fatal("DecodeJSON() error = nil")
			}
			var failureErr *failure.Error
			if !errors.As(err, &failureErr) || failureErr.Kind != failure.KindInvalid || failureErr.Code != "AUTH_INPUT_INVALID" {
				t.Fatalf("error = %#v", err)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"ok"}`))
	request.Header.Set("Content-Type", "text/plain")
	if err := DecodeJSON(httptest.NewRecorder(), request, &requestBody{}); err == nil {
		t.Fatalf("unsupported content type error = %#v", err)
	} else {
		var failureErr *failure.Error
		if !errors.As(err, &failureErr) || failureErr.Kind != failure.KindInvalid || failureErr.Code != "AUTH_INPUT_INVALID" {
			t.Fatalf("unsupported content type error = %#v", err)
		}
	}
}

func TestCSRFRequiresExactAllowedOriginAndVerifier(t *testing.T) {
	csrf := NewCSRF([]string{"https://app.example.test"})
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set(csrfHeader, "csrf-token")

	if err := csrf.Verify(request, func(token string) bool { return token == "csrf-token" }); err != nil {
		t.Fatalf("Verify() error = %#v", err)
	}
	if err := csrf.Verify(request, func(string) bool { return false }); err == nil || failureCode(t, err) != "AUTH_CSRF_INVALID" {
		t.Fatalf("invalid verifier error = %#v", err)
	}
	request.Header.Set("Origin", "https://app.example.test/path")
	if err := csrf.Verify(request, func(string) bool { return true }); err == nil || failureCode(t, err) != "AUTH_CSRF_INVALID" {
		t.Fatalf("invalid origin error = %#v", err)
	}
}

func failureCode(t *testing.T, err error) string {
	t.Helper()
	var failureErr *failure.Error
	if !errors.As(err, &failureErr) {
		t.Fatalf("error is not failure: %v", err)
	}
	return failureErr.Code
}
