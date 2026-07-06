package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
	"github.com/Medikong/services/services/user-service/internal/platform/config"
)

const serviceName = config.ServiceName

func RegisterRoutes(mux *http.ServeMux, service user.Service) {
	r := chi.NewRouter()
	ops := operational.New(serviceName, nil)
	r.Get("/healthz", ops.Healthz)
	r.Get("/readyz", ops.Readyz)
	r.Get("/metrics", ops.Metrics)
	user.NewController(service).RegisterRoutes(r)
	mux.Handle("/", r)
}
