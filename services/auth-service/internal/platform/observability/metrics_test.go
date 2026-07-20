package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionProjectionMetricUsesOnlyOutcomeLabels(t *testing.T) {
	metrics, err := NewMetrics("auth-service-worker-test", "test", "test")
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	defer func() { _ = metrics.Shutdown(t.Context()) }()

	metrics.RecordSessionProjectionAttempt("delivered")
	metrics.HTTP().Begin("POST", "/api/v1/auth/intents", "api")
	metrics.HTTP().End("POST", "/api/v1/auth/intents", "api", "/api/v1/auth/intents", "api", "201", 25*time.Millisecond)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if !strings.Contains(body, `auth_session_projection_attempts_total{result="delivered",service_name="auth-service-worker-test"} 1`) {
		t.Fatalf("session projection metric missing from scrape")
	}
	for _, expected := range []string{
		"# TYPE http_server_request_duration_seconds histogram",
		`http_route="/api/v1/auth/intents"`,
		`http_route_kind="api"`,
		`http_request_method="POST"`,
		`http_response_status_code="201"`,
		`service_version="test"`,
		`service_environment="test"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("HTTP metric contract is missing %q", expected)
		}
	}
	for _, forbidden := range []string{"session_id", "user_id", "token", "redis_key"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("session projection metric exposes forbidden label %q", forbidden)
		}
	}
}
