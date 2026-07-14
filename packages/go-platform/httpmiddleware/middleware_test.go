package httpmiddleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func TestStackGeneratesRequestIDAndReturnsHeader(t *testing.T) {
	logger.Configure(io.Discard, "test-service")
	handler := Stack(Config{ServiceName: "test-service"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestcontext.RequestID(r.Context()) == "" {
			t.Fatal("request id was not stored in context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if response.Header().Get(requestcontext.RequestIDHeader) == "" {
		t.Fatal("missing response request id")
	}
}

func TestStackRequestIDPolicy(t *testing.T) {
	logger.Configure(io.Discard, "test-service")
	uuidV4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	tests := []struct {
		name           string
		headerValue    string
		want           string
		wantGenerated  bool
		clientActionID string
	}{
		{name: "preserves external request id", headerValue: "req-duplicate-seat", want: "req-duplicate-seat"},
		{name: "trims external request id", headerValue: "  obs-e2e-123  ", want: "obs-e2e-123", clientActionID: "checkout-click"},
		{name: "generates uuid for blank request id", headerValue: "   ", wantGenerated: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var contextRequestID string
			var normalizedHeader string
			var contextClientActionID string
			handler := Stack(Config{ServiceName: "test-service"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				contextRequestID = requestcontext.RequestID(r.Context())
				normalizedHeader = r.Header.Get(requestcontext.RequestIDHeader)
				contextClientActionID = requestcontext.ClientActionID(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
			request.Header.Set(requestcontext.RequestIDHeader, tt.headerValue)
			if tt.clientActionID != "" {
				request.Header.Set(requestcontext.ClientActionIDHeader, tt.clientActionID)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			got := response.Header().Get(requestcontext.RequestIDHeader)
			if tt.wantGenerated {
				if !uuidV4.MatchString(got) {
					t.Fatalf("generated request id = %q, want UUID v4", got)
				}
			} else if got != tt.want {
				t.Fatalf("response request id = %q, want %q", got, tt.want)
			}
			if contextRequestID != got {
				t.Fatalf("context request id = %q, want response request id %q", contextRequestID, got)
			}
			if normalizedHeader != got {
				t.Fatalf("normalized request header = %q, want %q", normalizedHeader, got)
			}
			if contextClientActionID != tt.clientActionID {
				t.Fatalf("context client action id = %q, want %q", contextClientActionID, tt.clientActionID)
			}
		})
	}
}

func TestStackUsesRequestIDPolicyForTraceAttribute(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
	})

	logger.Configure(io.Discard, "test-service")
	handler := Stack(Config{
		ServiceName: "test-service",
		RoutePattern: func(*http.Request) string {
			return "/v1/auth/login"
		},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	request.Header.Set(requestcontext.RequestIDHeader, "  obs-e2e-123  ")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	for _, attr := range spans[0].Attributes {
		if string(attr.Key) == "request_id" && attr.Value.AsString() == "obs-e2e-123" {
			return
		}
	}
	t.Fatalf("trace span does not include normalized request_id: %#v", spans[0].Attributes)
}

func TestStackPreservesRequestIDInErrorResponse(t *testing.T) {
	logger.Configure(io.Discard, "test-service")
	handler := Stack(Config{ServiceName: "test-service"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	request.Header.Set(requestcontext.RequestIDHeader, "req-boom")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if got := response.Header().Get(requestcontext.RequestIDHeader); got != "req-boom" {
		t.Fatalf("response request id = %q, want req-boom", got)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if body["requestId"] != "req-boom" {
		t.Fatalf("body requestId = %v, want req-boom", body["requestId"])
	}
}

func TestRecoveryReraisesAfterResponseStarted(t *testing.T) {
	var logs bytes.Buffer
	logger.Configure(&logs, "test-service")
	handler := Stack(Config{ServiceName: "test-service"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		panic("late boom")
	}))

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("panic was not re-raised")
		}
		decoder := json.NewDecoder(&logs)
		errorLogs := 0
		accessLogs := 0
		for {
			var event map[string]any
			if err := decoder.Decode(&event); err != nil {
				break
			}
			if event["msg"] == "http.request.failed" {
				errorLogs++
				if event["level"] != "ERROR" {
					t.Fatalf("late panic error log = %#v", event)
				}
			}
			if event["msg"] == "http.request.completed" {
				accessLogs++
				if event["level"] != "ERROR" || event["http.handler_panicked"] != true {
					t.Fatalf("late panic access log = %#v", event)
				}
			}
		}
		if errorLogs != 1 || accessLogs != 1 {
			t.Fatalf("late panic logs = error:%d access:%d; logs=%s", errorLogs, accessLogs, logs.String())
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil))
}

func TestMetricsUseRoutePatternAndAvoidHighCardinalityLabels(t *testing.T) {
	logger.Configure(io.Discard, "test-service")
	registry := metrics.NewRegistry()
	handler := Stack(Config{
		ServiceName: "test-service",
		Metrics:     registry,
		RoutePattern: func(*http.Request) string {
			return "/v1/auth/{sessionId}/revoke"
		},
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/auth/session-123/revoke", nil)
	request.Header.Set(requestcontext.RequestIDHeader, "req-123")

	handler.ServeHTTP(httptest.NewRecorder(), request)
	var metricsText strings.Builder
	registry.WritePrometheus(&metricsText)
	output := metricsText.String()

	if !strings.Contains(output, `http_route="/v1/auth/{sessionId}/revoke"`) {
		t.Fatalf("metrics do not include route pattern: %s", output)
	}
	for _, forbidden := range []string{"session-123", "req-123", "request_id", "user_id"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("metrics include high-cardinality value %q: %s", forbidden, output)
		}
	}
}
