package operational

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestHandlerReadinessDrainAndRegistration(t *testing.T) {
	ready := false
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("custom_metric 1\n"))
	})
	handler := NewHandler(Config{
		Service:          "test-service",
		ReadinessTimeout: time.Second,
		Checks: map[string]Check{
			"database": func(context.Context) error { return nil },
		},
		Metrics:  metrics,
		SetReady: func(value bool) { ready = value },
	})
	mux := http.NewServeMux()
	handler.RegisterAll(mux, true)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK || !ready {
		t.Fatalf("ready status=%d metric=%v", response.Code, ready)
	}

	metricResponse := httptest.NewRecorder()
	mux.ServeHTTP(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricResponse.Body.String(), "custom_metric 1") {
		t.Fatalf("metrics body = %q", metricResponse.Body.String())
	}

	pprofResponse := httptest.NewRecorder()
	mux.ServeHTTP(pprofResponse, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if pprofResponse.Code != http.StatusOK {
		t.Fatalf("pprof status = %d", pprofResponse.Code)
	}

	handler.BeginDrain()
	draining := httptest.NewRecorder()
	mux.ServeHTTP(draining, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if draining.Code != http.StatusServiceUnavailable || ready {
		t.Fatalf("draining status=%d metric=%v", draining.Code, ready)
	}

	rejected := httptest.NewRecorder()
	handler.RejectWhileDraining(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler was called while draining")
	})).ServeHTTP(rejected, httptest.NewRequest(http.MethodGet, "/", nil))
	if rejected.Code != http.StatusServiceUnavailable || rejected.Header().Get("Retry-After") != "1" {
		t.Fatalf("rejected status=%d retry-after=%q", rejected.Code, rejected.Header().Get("Retry-After"))
	}
}

func TestReadyzFailsClosedWhenCheckFails(t *testing.T) {
	handler := NewHandler(Config{
		Service: "test-service",
		Checks: map[string]Check{
			"database": func(context.Context) error { return context.Canceled },
		},
	})
	response := httptest.NewRecorder()
	handler.Readyz(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
