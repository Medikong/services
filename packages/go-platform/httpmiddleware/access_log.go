package httpmiddleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/requestcontext"
)

func AccessLog(config Config) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startedAt := time.Now()
			recorder := newResponseRecorder(w)
			defer func() {
				recovered := recover()
				statusCode := recorder.StatusCode()
				duration := time.Since(startedAt)
				route := routePattern(config.RoutePattern, r)
				severity, level := requestSeverity(statusCode, duration)
				policy := logPolicy(routeKind(route), statusCode, duration)
				if recovered != nil {
					severity = "ERROR"
					level = slog.LevelError
					policy = "keep"
				}
				requestID := requestcontext.RequestID(r.Context())
				traceID, spanID := logger.TraceIDs(r.Context())
				logger.Default().Log(r.Context(), level, "http.request.completed",
					"event", "http.request.completed",
					"service.name", config.ServiceName,
					"service.version", config.ServiceVersion,
					"service.environment", config.ServiceEnvironment,
					"severity", severity,
					"severity_text", severity,
					"request_id", requestID,
					"correlation_id", requestID,
					"trace_id", traceID,
					"span_id", spanID,
					"client_action_id", requestcontext.ClientActionID(r.Context()),
					"http.method", r.Method,
					"http.route", route,
					"http.route.kind", routeKind(route),
					"http.status_code", statusCode,
					"duration_ms", duration.Milliseconds(),
					"http.request.is_probe", routeKind(route) == "probe",
					"http.response_started", recorder.WroteHeader(),
					"http.handler_panicked", recovered != nil,
					"log.kind", "access",
					"log.policy", policy,
				)
				if recovered != nil {
					panic(recovered)
				}
			}()
			next.ServeHTTP(recorder.Writer(), r)
		})
	}
}
