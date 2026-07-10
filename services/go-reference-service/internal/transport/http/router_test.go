package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpmiddleware"
	"github.com/Medikong/services/packages/go-platform/logger"
)

func TestStackInsideChiLogsRouteTraceAndErrorLevel(t *testing.T) {
	var logs bytes.Buffer
	originalLogger := logger.Default()
	logger.Configure(&logs, "test-service")
	t.Cleanup(func() { logger.SetDefault(originalLogger) })

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(exporter),
	)
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(originalProvider)
	})

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return httpmiddleware.Stack(httpmiddleware.Config{
			ServiceName:  "test-service",
			RoutePattern: RoutePattern,
		}, next)
	})
	router.Get("/v1/resources/{resourceID}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/resources/resource-1", nil))

	if response.Header().Get("X-Trace-Id") == "" {
		t.Fatal("response X-Trace-Id is empty")
	}
	var event map[string]any
	if err := json.NewDecoder(&logs).Decode(&event); err != nil {
		t.Fatalf("decode access log: %v", err)
	}
	if event["http.route"] != "/v1/resources/{resourceID}" {
		t.Fatalf("http.route = %v", event["http.route"])
	}
	if event["trace_id"] != response.Header().Get("X-Trace-Id") {
		t.Fatalf("trace_id = %v, response = %s", event["trace_id"], response.Header().Get("X-Trace-Id"))
	}
	if event["level"] != "ERROR" {
		t.Fatalf("level = %v, want ERROR", event["level"])
	}
	if _, ok := event["span_id"]; !ok {
		t.Fatal("span_id is missing")
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if spans[0].Name != "GET /v1/resources/{resourceID}" {
		t.Fatalf("span name = %q", spans[0].Name)
	}
	for _, attribute := range spans[0].Attributes {
		if string(attribute.Key) == "http.route" && attribute.Value.AsString() == "/v1/resources/{resourceID}" {
			return
		}
	}
	t.Fatalf("span is missing route pattern: %#v", spans[0].Attributes)
}

func TestRequireIdempotencyKey(t *testing.T) {
	handler := RequireIdempotencyKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(headers.IdempotencyKey); got != "operation-1" {
			t.Fatalf("Idempotency-Key = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/", nil))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing key status = %d", missing.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set(headers.IdempotencyKey, "  operation-1  ")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("valid key status = %d", response.Code)
	}
}

func TestLockKeyUsesRedisClusterHashTag(t *testing.T) {
	router := chi.NewRouter()
	var got httpmiddleware.RedisLockKey
	router.Post("/v1/reference/resources/{resourceID}/audit", func(w http.ResponseWriter, r *http.Request) {
		var err error
		got, err = lockKey(r)
		if err != nil {
			t.Fatalf("lockKey() error = %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/reference/resources/resource-1/audit", nil))

	if got.Lock != "reference:{resource-1}:lock" || got.Fence != "reference:{resource-1}:fence" {
		t.Fatalf("lock key = %#v", got)
	}
}
