package operational

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOperationalEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	New("test-service", nil).Register(mux)

	tests := []struct {
		name        string
		path        string
		contentType string
		wantStatus  int
		wantBody    string
	}{
		{
			name:        "healthz",
			path:        "/healthz",
			contentType: "application/json",
			wantStatus:  http.StatusOK,
			wantBody:    `"status":"ok"`,
		},
		{
			name:        "readyz",
			path:        "/readyz",
			contentType: "application/json",
			wantStatus:  http.StatusOK,
			wantBody:    `"status":"ready"`,
		},
		{
			name:        "metrics",
			path:        "/metrics",
			contentType: metricsContentType,
			wantStatus:  http.StatusOK,
			wantBody:    `service_ready{service="test-service"} 1`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()

			mux.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if got := response.Header().Get("Content-Type"); got != tt.contentType {
				t.Fatalf("content type = %q, want %q", got, tt.contentType)
			}
			if body := response.Body.String(); !strings.Contains(body, tt.wantBody) {
				t.Fatalf("body %q does not contain %q", body, tt.wantBody)
			}
		})
	}
}
