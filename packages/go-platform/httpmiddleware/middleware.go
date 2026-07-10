package httpmiddleware

import (
	"net/http"

	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/telemetry"
)

type Middleware func(http.Handler) http.Handler

type Config struct {
	ServiceName  string
	Metrics      *metrics.Registry
	RoutePattern func(*http.Request) string
}

func Stack(config Config, next http.Handler) http.Handler {
	return Chain(
		RequestContext,
		ResponseHeaders,
		func(handler http.Handler) http.Handler {
			return telemetry.MiddlewareWithRoute(config.ServiceName, handler, config.RoutePattern)
		},
		Metrics(config),
		AccessLog(config),
		Recovery,
	)(next)
}

func Chain(middlewares ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}
