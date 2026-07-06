package httpmiddleware

import (
	"fmt"
	"net/http"
	"time"
)

func Metrics(config Config) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if config.Metrics == nil {
				next.ServeHTTP(w, r)
				return
			}
			startedAt := time.Now()
			recorder := ensureRecorder(w)
			activeLabels := map[string]string{
				"service_name":        config.ServiceName,
				"http_request_method": r.Method,
			}
			config.Metrics.Add("http_server_active_requests", activeLabels, 1)
			defer func() {
				route := routePattern(config.RoutePattern, r)
				routeKind := routeKind(route)
				statusCode := fmt.Sprintf("%d", recorder.StatusCode())
				requestLabels := map[string]string{
					"service_name":              config.ServiceName,
					"http_request_method":       r.Method,
					"http_route":                route,
					"http_route_kind":           routeKind,
					"http_response_status_code": statusCode,
				}
				config.Metrics.Add("http_server_active_requests", activeLabels, -1)
				config.Metrics.Add("http_server_request_duration_seconds", requestLabels, time.Since(startedAt).Seconds())
				config.Metrics.Inc("http_server_requests_total", requestLabels)
			}()
			next.ServeHTTP(recorder, r)
		})
	}
}
