package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsExposeCouponRuntimeSignals(t *testing.T) {
	metrics, err := NewMetrics("coupon-service", "test", "test")
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	t.Cleanup(func() { _ = metrics.Shutdown(context.Background()) })
	metrics.RecordWorker("issue", "success")
	metrics.RecordRedisGate("db_fallback")
	metrics.RecordDBFinalize("success")
	metrics.SetReady(true)
	metrics.HTTP().Begin("POST", "/api/v1/coupon-campaigns/{campaignId}/issues", "api")
	metrics.HTTP().End(
		"POST",
		"/api/v1/coupon-campaigns/{campaignId}/issues",
		"api",
		"/api/v1/coupon-campaigns/{campaignId}/issues",
		"api",
		"202",
		50*time.Millisecond,
	)

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expected := range []string{
		"coupon_worker_attempts_total",
		"coupon_redis_gate_total",
		"coupon_db_finalize_total",
		`service_ready{service_environment="test",service_name="coupon-service",service_version="test"} 1`,
		"# TYPE http_server_request_duration_seconds histogram",
		`http_route="/api/v1/coupon-campaigns/{campaignId}/issues"`,
		`http_response_status_code="202"`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("metrics output is missing %q", expected)
		}
	}
}
