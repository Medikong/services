package httpmiddleware

import (
	"log/slog"
	"net/http"

	"github.com/samber/oops"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func ErrorLog(config Config) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reporter := httpapi.ErrorReporter(func(err error, statusCode int, code string) {
				level := slog.LevelWarn
				severity := "WARN"
				if statusCode >= http.StatusInternalServerError {
					level = slog.LevelError
					severity = "ERROR"
				}
				requestID := requestcontext.RequestID(r.Context())
				traceID, spanID := logger.TraceIDs(r.Context())
				route := routePattern(config.RoutePattern, r)
				logger.Default().Log(r.Context(), level, "http.request.failed",
					"event", "http.request.failed",
					"service.name", config.ServiceName,
					"service.version", config.ServiceVersion,
					"service.environment", config.ServiceEnvironment,
					"severity", severity,
					"severity_text", severity,
					"request_id", requestID,
					"correlation_id", requestID,
					"trace_id", traceID,
					"span_id", spanID,
					"http.method", r.Method,
					"http.route", route,
					"http.route.kind", routeKind(route),
					"http.status_code", statusCode,
					"http.error_code", code,
					"log.kind", "error",
					"log.policy", "keep",
					logger.Err(err),
				)
				if statusCode < http.StatusInternalServerError {
					return
				}
				span := trace.SpanFromContext(r.Context())
				span.RecordError(
					oops.Code(code).New("http request failed"),
					trace.WithAttributes(attribute.String("error.code", code)),
				)
				span.SetStatus(codes.Error, code)
			})
			ctx := httpapi.WithErrorReporter(r.Context(), reporter)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
