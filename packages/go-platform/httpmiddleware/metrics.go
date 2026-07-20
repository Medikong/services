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
			recorder := newResponseRecorder(w)
			activeRoute := routePattern(config.RoutePattern, r)
			activeRouteKind := routeKind(activeRoute)
			config.Metrics.Begin(r.Method, activeRoute, activeRouteKind)
			defer func() {
				route := routePattern(config.RoutePattern, r)
				routeKind := routeKind(route)
				statusCode := fmt.Sprintf("%d", recorder.StatusCode())
				config.Metrics.End(
					r.Method,
					activeRoute,
					activeRouteKind,
					route,
					routeKind,
					statusCode,
					time.Since(startedAt),
				)
			}()
			next.ServeHTTP(recorder.Writer(), r)
		})
	}
}
