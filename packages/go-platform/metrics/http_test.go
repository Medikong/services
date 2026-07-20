package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestHTTPMetricsExposeSharedHistogramContract(t *testing.T) {
	registry := prometheus.NewRegistry()
	httpMetrics, err := NewHTTP(registry, ServiceIdentity{
		Name:        "auth-service",
		Version:     "test-version",
		Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}

	httpMetrics.Begin("POST", "/api/v1/auth/sign-in/email", "api")
	httpMetrics.End(
		"POST",
		"/api/v1/auth/sign-in/email",
		"api",
		"/api/v1/auth/sign-in/email",
		"api",
		"401",
		250*time.Millisecond,
	)

	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	text := recorder.Body.String()
	for _, expected := range []string{
		"http_server_request_duration_seconds",
		`service_name="auth-service"`,
		`service_version="test-version"`,
		`service_environment="test"`,
		`http_route="/api/v1/auth/sign-in/email"`,
		`http_route_kind="api"`,
		`http_request_method="POST"`,
		`http_response_status_code="401"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("metric contract is missing %q: %s", expected, text)
		}
	}
	for _, forbidden := range []string{"request_id", "trace_id", "span_id", "user_id"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("metric contract contains high-cardinality label %q: %s", forbidden, text)
		}
	}
}

func TestServiceIdentityRejectsEmptyValues(t *testing.T) {
	_, err := NewHTTP(prometheus.NewRegistry(), ServiceIdentity{Name: "auth-service", Version: "", Environment: "test"})
	if err == nil || !strings.Contains(err.Error(), "service_version") {
		t.Fatalf("NewHTTP() error = %v, want service_version validation", err)
	}
}

func TestHTTPMetricsBoundArbitraryRequestMethods(t *testing.T) {
	registry := prometheus.NewRegistry()
	httpMetrics, err := NewHTTP(registry, ServiceIdentity{
		Name: "auth-service", Version: "test-version", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}

	for _, method := range []string{"X-CARDINALITY-1", "X-CARDINALITY-2"} {
		httpMetrics.Begin(method, "unmatched", "unmatched")
		httpMetrics.End(method, "unmatched", "unmatched", "unmatched", "unmatched", "405", time.Millisecond)
	}

	recorder := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	text := recorder.Body.String()
	if !strings.Contains(text, `http_request_method="OTHER"`) {
		t.Fatalf("metrics do not contain bounded method: %s", text)
	}
	for _, forbidden := range []string{"X-CARDINALITY-1", "X-CARDINALITY-2"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("metrics contain arbitrary method %q: %s", forbidden, text)
		}
	}
}
