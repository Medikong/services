package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	platformmiddleware "github.com/Medikong/services/packages/go-platform/httpmiddleware"
	platformmetrics "github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestRouterExposesActiveGaugeWithPreMatchedAPITemplate(t *testing.T) {
	router, err := NewRouter(&recordingBackend{}, Options{AllowedOrigins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	registry := prometheus.NewRegistry()
	httpMetrics, err := platformmetrics.NewHTTP(registry, platformmetrics.ServiceIdentity{
		Name: "coupon-service", Version: "test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	router.Get("/api/v1/internal/metrics-test/{operationId}", func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		w.WriteHeader(http.StatusNoContent)
	})
	handler := platformmiddleware.Stack(platformmiddleware.Config{
		ServiceName: "coupon-service", ServiceVersion: "test", ServiceEnvironment: "test",
		Metrics: httpMetrics, RoutePattern: platformmiddleware.ChiRoutePattern(router),
	}, router)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/api/v1/internal/metrics-test/private-operation", nil),
		)
	}()
	<-requestStarted

	response := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/metrics", nil),
	)
	output := response.Body.String()
	for _, expected := range []string{
		`http_server_active_requests{`,
		`http_route="/api/v1/internal/metrics-test/{operationId}"`,
		`http_route_kind="api"`,
		`} 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("active metric is missing %q: %s", expected, output)
		}
	}
	if strings.Contains(output, "private-operation") {
		t.Fatalf("active metric contains raw path: %s", output)
	}

	close(releaseRequest)
	<-done
}
