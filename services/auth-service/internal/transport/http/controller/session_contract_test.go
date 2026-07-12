package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeOptionalLogoutRequest(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantReason  string
		omitBody    bool
	}{
		{name: "omitted body", omitBody: true},
		{name: "empty object", body: `{}`, contentType: "application/json"},
		{name: "unknown field", body: `{"unexpected":true}`, contentType: "application/json", wantReason: "additional_property"},
		{name: "trailing value", body: `{} {}`, contentType: "application/json", wantReason: "trailing_data"},
		{name: "null", body: `null`, contentType: "application/json", wantReason: "invalid_json"},
		{name: "unsupported media type", body: `{}`, contentType: "text/plain", wantReason: "unsupported_media_type"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var request *http.Request
			if test.omitBody {
				request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/sessions/logout", nil)
			} else {
				request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/sessions/logout", strings.NewReader(test.body))
				request.Header.Set("Content-Type", test.contentType)
			}
			problem := decodeOptionalLogoutRequest(httptest.NewRecorder(), request)
			if test.wantReason == "" {
				if problem != nil {
					t.Fatalf("decodeOptionalLogoutRequest() error = %#v", problem)
				}
				return
			}
			if problem == nil || problem.Code != "AUTH_INPUT_INVALID" || len(problem.Violations) != 1 || problem.Violations[0].Reason != test.wantReason {
				t.Fatalf("decodeOptionalLogoutRequest() error = %#v", problem)
			}
		})
	}
}
