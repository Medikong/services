package telemetry

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/samber/oops"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

func Init(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_SDK_DISABLED")), "true") {
		return func(context.Context) error { return nil }, nil
	}
	exporter := strings.ToLower(strings.TrimSpace(getenv("OTEL_TRACES_EXPORTER", "")))
	if exporter == "" || exporter == "none" {
		return func(context.Context) error { return nil }, nil
	}
	if exporter != "otlp" {
		return nil, oops.
			In("telemetry").
			Code("telemetry.unsupported_exporter").
			With("exporter", exporter).
			New("unsupported trace exporter")
	}

	// The exporter reads the standard OTEL_EXPORTER_OTLP_* variables, including TLS.
	exp, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, oops.In("telemetry").Code("telemetry.exporter_init_failed").Wrap(err)
	}
	res, err := Resource(serviceName)
	if err != nil {
		return nil, oops.In("telemetry").Code("telemetry.resource_init_failed").Wrap(err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func Resource(serviceName string) (*resource.Resource, error) {
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(getenv("SERVICE_VERSION", "dev")),
		semconv.DeploymentEnvironmentNameKey.String(getenv("SERVICE_ENVIRONMENT", "dev")),
	))
	if err != nil {
		return nil, oops.In("telemetry").Code("telemetry.resource_init_failed").Wrap(err)
	}
	return res, nil
}

func Middleware(serviceName string, next http.Handler) http.Handler {
	return MiddlewareWithRoute(serviceName, next, nil)
}

func MiddlewareWithRoute(serviceName string, next http.Handler, routePattern func(*http.Request) string) http.Handler {
	instrumented := otelhttp.NewMiddleware(serviceName,
		otelhttp.WithFilter(func(r *http.Request) bool { return !isOperationalPath(r.URL.Path) }),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + routeFromRequest(routePattern, r)
		}),
	)
	return instrumented(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		requestID := r.Header.Get("X-Request-Id")
		span.SetAttributes(attribute.String("request_id", requestID))
		defer func() {
			route := routeFromRequest(routePattern, r)
			span.SetName(r.Method + " " + route)
			span.SetAttributes(attribute.String("http.route", route))
			if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
				labeler.Add(attribute.String("http.route", route))
			}
		}()
		traceID := span.SpanContext().TraceID().String()
		if span.SpanContext().IsValid() {
			w.Header().Set("X-Trace-Id", traceID)
		}
		next.ServeHTTP(w, r)
	}))
}

func StartSpan(ctx context.Context, serviceName string, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(serviceName).Start(ctx, name, trace.WithAttributes(attrs...))
}

func Inject(ctx context.Context, header http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
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
