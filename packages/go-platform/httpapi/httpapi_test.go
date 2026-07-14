package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func TestWriteErrorUsesOutermostHTTPLayerOnly(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	request.Header.Set(requestcontext.RequestIDHeader, "req-1")
	request, _, err := requestcontext.Ensure(request)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	response := httptest.NewRecorder()

	inner := oops.
		In("repository").
		Code("repository.private_code").
		Public("내부 공개 메시지").
		With("token", "raw-token", "query", "SELECT secret").
		New("private database failure")
	err = Error(http.StatusConflict, "auth.email_already_exists").
		In("auth").
		Public("이미 가입된 이메일입니다.").
		With("handler_context", "must-not-leak").
		Wrap(inner)
	WriteError(response, request, err)

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
	if _, ok := errorBody["details"]; ok {
		t.Fatalf("details must not be exposed: %v", errorBody["details"])
	}
	for _, forbidden := range []string{
		"repository.private_code", "내부 공개 메시지", "raw-token", "SELECT secret",
		"private database failure", "must-not-leak", "handler_context",
	} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestWriteErrorSkipsInvalidHTTPStatusLayer(t *testing.T) {
	for _, invalidStatus := range []any{http.StatusOK, 499.5, "409"} {
		t.Run(fmt.Sprint(invalidStatus), func(t *testing.T) {
			inner := NotFound("user.not_found").
				Public("사용자를 찾을 수 없습니다.").
				New("user missing")
			err := oops.
				Code("invalid.outer").
				Public("잘못된 외부 계층").
				With(OopsHTTPStatusCodeKey, invalidStatus).
				Wrap(inner)
			response := httptest.NewRecorder()

			WriteError(response, httptest.NewRequest(http.MethodGet, "/", nil), err)

			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("json decode: %v", err)
			}
			errorBody := body["error"].(map[string]any)
			if errorBody["code"] != "user.not_found" || errorBody["message"] != "사용자를 찾을 수 없습니다." {
				t.Fatalf("error = %#v", errorBody)
			}
		})
	}
}

func TestWriteErrorMasksErrorsWithoutHTTPStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "oops error",
			err: oops.In("repository").
				Code("repository.secret_code").
				Public("내부 메시지").
				With("secret", "value").
				New("database query failed"),
		},
		{name: "standard error", err: errors.New("plain internal error")},
		{name: "nil error", err: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			response := httptest.NewRecorder()

			WriteError(response, request, test.err)

			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("json decode: %v", err)
			}
			errorBody := body["error"].(map[string]any)
			if errorBody["code"] != internalErrorCode {
				t.Fatalf("code = %v, want %s", errorBody["code"], internalErrorCode)
			}
			if errorBody["message"] != internalErrorMessage {
				t.Fatalf("message = %v, want %s", errorBody["message"], internalErrorMessage)
			}
			if _, ok := errorBody["details"]; ok {
				t.Fatalf("details must not be exposed: %v", errorBody["details"])
			}
		})
	}
}

func TestWriteErrorReportsOriginalErrorOnceBeforeWriting(t *testing.T) {
	err := Conflict("user.version_conflict").
		Public("사용자 정보가 먼저 변경되었습니다.").
		New("version conflict")
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	reportCount := 0
	request = request.WithContext(WithErrorReporter(request.Context(), func(got error, statusCode int, code string) {
		reportCount++
		if got == nil || got.Error() != err.Error() {
			t.Fatalf("reported error = %v, want %v", got, err)
		}
		if statusCode != http.StatusConflict || code != "user.version_conflict" {
			t.Fatalf("report = status:%d code:%q", statusCode, code)
		}
		if response.Code != http.StatusOK || response.Body.Len() != 0 {
			t.Fatal("reporter must run before the response is written")
		}
	}))

	WriteError(response, request, err)

	if reportCount != 1 {
		t.Fatalf("report count = %d, want 1", reportCount)
	}
}
