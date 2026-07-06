package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials/insecure"
)

func Init(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	exporter := strings.ToLower(strings.TrimSpace(getenv("OTEL_TRACES_EXPORTER", "")))
	if exporter == "" || exporter == "none" {
		return func(context.Context) error { return nil }, nil
	}
	if exporter != "otlp" {
		return nil, fmt.Errorf("unsupported OTEL_TRACES_EXPORTER=%s", exporter)
	}

	endpoint := strings.TrimPrefix(getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel-collector:4317")), "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")
	exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithTLSCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		"",
		attribute.String("service.name", serviceName),
		attribute.String("service.version", getenv("SERVICE_VERSION", "dev")),
		attribute.String("service.environment", getenv("SERVICE_ENVIRONMENT", "dev")),
	))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func Middleware(serviceName string, next http.Handler) http.Handler {
	return MiddlewareWithRoute(serviceName, next, nil)
}

func MiddlewareWithRoute(serviceName string, next http.Handler, routePattern func(*http.Request) string) http.Handler {
	tracer := otel.Tracer(serviceName)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isOperationalPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		requestID := r.Header.Get("X-Request-Id")
		ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("http.route", r.URL.Path),
				attribute.String("request_id", requestID),
			),
		)
		defer func() {
			route := routeFromRequest(routePattern, r)
			span.SetName(r.Method + " " + route)
			span.SetAttributes(attribute.String("http.route", route))
			span.End()
		}()

		traceID := span.SpanContext().TraceID().String()
		if span.SpanContext().IsValid() {
			w.Header().Set("X-Trace-Id", traceID)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func StartSpan(ctx context.Context, serviceName string, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(serviceName).Start(ctx, name, trace.WithAttributes(attrs...))
}

func Inject(ctx context.Context, header http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
}

func SleepFlush() {
	time.Sleep(50 * time.Millisecond)
}

func isOperationalPath(path string) bool {
	return path == "/healthz" || path == "/readyz" || path == "/metrics"
}

func routeFromRequest(routePattern func(*http.Request) string, r *http.Request) string {
	if routePattern == nil {
		return "unmatched"
	}
	route := routePattern(r)
	if route == "" {
		return "unmatched"
	}
	return route
}

func getenv(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
