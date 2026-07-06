package httpmiddleware

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func AccessLog(config Config) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			recorder := ensureRecorder(w)
			next.ServeHTTP(recorder, r)

			statusCode := recorder.StatusCode()
			duration := time.Since(startedAt)
			route := routePattern(config.RoutePattern, r)
			traceID := trace.SpanContextFromContext(r.Context()).TraceID().String()
			if traceID == "00000000000000000000000000000000" {
				traceID = ""
			}
			severity := requestSeverity(statusCode, duration)
			logger.Info(r.Context(), "http.request.completed",
				"service.name", config.ServiceName,
				"severity", severity,
				"severity_text", severity,
				"trace_id", traceID,
				"request_id", requestcontext.RequestID(r.Context()),
				"client_action_id", requestcontext.ClientActionID(r.Context()),
				"http.method", r.Method,
				"http.route", route,
				"http.route.kind", routeKind(route),
				"http.status_code", statusCode,
				"duration_ms", duration.Milliseconds(),
				"http.request.is_probe", routeKind(route) == "probe",
				"log.kind", "access",
				"log.policy", logPolicy(routeKind(route), statusCode, duration),
			)
		})
	}
}
