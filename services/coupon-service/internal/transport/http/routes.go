package http

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/coupon-service/internal/domain/coupon"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
)

const serviceName = config.ServiceName

func RegisterRoutes(mux *http.ServeMux, service coupon.Service, registry *metrics.Registry, checks map[string]operational.Check) {
	r := chi.NewRouter()
	ops := operational.NewWithMetrics(serviceName, checks, []func(io.Writer){registry.WritePrometheus})
	r.Get("/healthz", ops.Healthz)
	r.Get("/readyz", ops.Readyz)
	r.Get("/metrics", ops.Metrics)
	coupon.NewController(service, registry).RegisterRoutes(r)
	mux.Handle("/", r)
}

func RoutePattern(r *http.Request) string {
	routeContext := chi.RouteContext(r.Context())
	if routeContext == nil {
		return "unmatched"
	}
	pattern := routeContext.RoutePattern()
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}
