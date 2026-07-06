package http

import (
	"io"
	nethttp "net/http"

	"github.com/go-chi/chi/v5"

	platformmetrics "github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/domain/account"
	"github.com/Medikong/services/services/auth-service/internal/domain/dev"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

type Services struct {
	Accounts account.Service
	Sessions session.Service
	Dev      dev.Service
}

func RegisterRoutes(mux *nethttp.ServeMux, services Services, checks map[string]operational.Check) {
	mux.Handle("/", NewRouter(services, checks, platformmetrics.NewRegistry()))
}

func NewRouter(services Services, checks map[string]operational.Check, registry *platformmetrics.Registry) nethttp.Handler {
	r := chi.NewRouter()
	ops := operational.NewWithMetrics(config.ServiceName, checks, metricCollectors(registry))
	r.Get("/healthz", ops.Healthz)
	r.Get("/readyz", ops.Readyz)
	r.Get("/metrics", ops.Metrics)
	account.NewController(services.Accounts).RegisterRoutes(r)
	session.NewController(services.Sessions).RegisterRoutes(r)
	dev.NewController(services.Dev).RegisterRoutes(r)
	return r
}

func RoutePattern(r *nethttp.Request) string {
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

func metricCollectors(registry *platformmetrics.Registry) []func(io.Writer) {
	if registry == nil {
		return nil
	}
	return []func(io.Writer){registry.WritePrometheus}
}
