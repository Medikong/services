package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsExposeUserHTTPContract(t *testing.T) {
	metrics, err := NewMetrics("user-service", "test-version", "test")
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	t.Cleanup(func() { _ = metrics.Shutdown(t.Context()) })

	metrics.SetReady(true)
	metrics.HTTP().Begin("PATCH", "/api/v1/users/me", "api")
	metrics.HTTP().End("PATCH", "/api/v1/users/me", "api", "/api/v1/users/me", "api", "200", 10*time.Millisecond)

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expected := range []string{
		`service_ready{service_environment="test",service_name="user-service",service_version="test-version"} 1`,
		"# TYPE http_server_request_duration_seconds histogram",
		`http_route="/api/v1/users/me"`,
		`http_route_kind="api"`,
		`http_request_method="PATCH"`,
		`http_response_status_code="200"`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("metrics output is missing %q", expected)
		}
	}
	for _, forbidden := range []string{"request_id", "trace_id", "span_id", "user_id"} {
		if strings.Contains(response.Body.String(), forbidden+"=") {
			t.Fatalf("metrics output contains high-cardinality label %q", forbidden)
		}
	}
}
