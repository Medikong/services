package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platformmetrics "github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestRouterExposesActiveGaugeWithPreMatchedAPITemplate(t *testing.T) {
	registry := prometheus.NewRegistry()
	httpMetrics, err := platformmetrics.NewHTTP(registry, platformmetrics.ServiceIdentity{
		Name: "user-service", Version: "test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewHTTP() error = %v", err)
	}
	router, err := NewRouter(
		RouterConfig{
			ServiceName: "user-service", ServiceVersion: "test", ServiceEnvironment: "test",
			RequestTimeout: time.Second, Metrics: httpMetrics,
		},
		&user.UserHandler{},
		nil,
		operational.New("user-service", nil),
	)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	router.Get("/api/v1/users/{userId}/metrics-test", func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		w.WriteHeader(http.StatusNoContent)
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/api/v1/users/private-user/metrics-test", nil),
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
		`http_route="/api/v1/users/{userId}/metrics-test"`,
		`http_route_kind="api"`,
		`} 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("active metric is missing %q: %s", expected, output)
		}
	}
	if strings.Contains(output, "private-user") {
		t.Fatalf("active metric contains raw path: %s", output)
	}

	close(releaseRequest)
	<-done
}
