package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsExposeCouponRuntimeSignals(t *testing.T) {
	metrics, err := NewMetrics("coupon-service")
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	t.Cleanup(func() { _ = metrics.Shutdown(context.Background()) })
	metrics.RecordWorker("issue", "success")
	metrics.RecordRedisGate("db_fallback")
	metrics.RecordDBFinalize("success")
	metrics.SetReady(true)

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, expected := range []string{
		"coupon_worker_attempts_total",
		"coupon_redis_gate_total",
		"coupon_db_finalize_total",
		`service_ready{service_name="coupon-service"} 1`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("metrics output is missing %q", expected)
		}
	}
}
