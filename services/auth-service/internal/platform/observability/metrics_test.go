package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionProjectionMetricUsesOnlyOutcomeLabels(t *testing.T) {
	metrics, err := NewMetrics("auth-service-worker-test")
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	defer func() { _ = metrics.Shutdown(t.Context()) }()

	metrics.RecordSessionProjectionAttempt("delivered")
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/metrics", nil)
	metrics.Handler().ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if !strings.Contains(body, `auth_session_projection_attempts_total{result="delivered",service_name="auth-service-worker-test"} 1`) {
		t.Fatalf("session projection metric missing from scrape")
	}
	for _, forbidden := range []string{"session_id", "user_id", "token", "redis_key"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("session projection metric exposes forbidden label %q", forbidden)
		}
	}
}
