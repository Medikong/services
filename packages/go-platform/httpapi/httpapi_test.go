package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func TestWriteErrorUsesOopsMetadata(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	request.Header.Set(requestcontext.RequestIDHeader, "req-1")
	request, _, err := requestcontext.Ensure(request)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	response := httptest.NewRecorder()

	WriteError(response, request, NewError(http.StatusConflict, "auth.email_already_exists", "이미 가입된 이메일입니다.", OopsDetails("email", "a@example.com")))

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["requestId"] != "req-1" {
		t.Fatalf("requestId = %v, want req-1", body["requestId"])
	}
	errorBody := body["error"].(map[string]any)
	if errorBody["code"] != "auth.email_already_exists" {
		t.Fatalf("code = %v", errorBody["code"])
	}
	if errorBody["message"] != "이미 가입된 이메일입니다." {
		t.Fatalf("message = %v", errorBody["message"])
	}
	details := errorBody["details"].(map[string]any)
	if details["email"] != "a@example.com" {
		t.Fatalf("details.email = %v", details["email"])
	}
}
