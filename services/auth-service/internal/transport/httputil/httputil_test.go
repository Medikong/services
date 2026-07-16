package httputil

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/google/uuid"
	"github.com/samber/oops"
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
			var target requestBody
			err := DecodeJSON(httptest.NewRecorder(), request, &target)
			if err == nil {
				t.Fatal("DecodeJSON() error = nil")
			}
			code, context := oopsMetadata(t, err)
			if code != "AUTH_INPUT_INVALID" || context["reason"] != test.reason {
				t.Fatalf("error = %#v", err)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"ok"}`))
	request.Header.Set("Content-Type", "text/plain")
	if err := DecodeJSON(httptest.NewRecorder(), request, &requestBody{}); err == nil {
		t.Fatalf("unsupported content type error = %#v", err)
	} else if _, context := oopsMetadata(t, err); context["reason"] != "unsupported_media_type" {
		t.Fatalf("unsupported content type context = %#v", context)
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
	if err := csrf.Verify(request, func(string) bool { return false }); err == nil || oopsCode(t, err) != "AUTH_CSRF_INVALID" {
		t.Fatalf("invalid verifier error = %#v", err)
	}
	request.Header.Set("Origin", "https://app.example.test/path")
	if err := csrf.Verify(request, func(string) bool { return true }); err == nil || oopsCode(t, err) != "AUTH_CSRF_INVALID" {
		t.Fatalf("invalid origin error = %#v", err)
	}
}

func oopsCode(t *testing.T, err error) string {
	t.Helper()
	code, _ := oopsMetadata(t, err)
	return code
}

func oopsMetadata(t *testing.T, err error) (string, map[string]any) {
	t.Helper()
	oopsErr, ok := oops.AsOops(err)
	if !ok {
		t.Fatalf("error is not oops: %v", err)
	}
	code, _ := oopsErr.Code().(string)
	return code, oopsErr.Context()
}
