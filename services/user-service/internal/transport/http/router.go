package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	platformmiddleware "github.com/Medikong/services/packages/go-platform/httpmiddleware"
	platformmetrics "github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/user-service/internal/development"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
)

type RouterConfig struct {
	ServiceName        string
	ServiceVersion     string
	ServiceEnvironment string
	RequestTimeout     time.Duration
	Metrics            *platformmetrics.HTTP
}

func NewRouter(
	cfg RouterConfig,
	userHandler *user.UserHandler,
	proofHandler *development.ProofHandler,
	health *operational.Handler,
) (*chi.Mux, error) {
	if userHandler == nil || health == nil || cfg.ServiceName == "" || cfg.RequestTimeout <= 0 {
		return nil, oops.In("user_router").Code("router.dependencies_required").
			New("router dependencies and request timeout are required")
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return platformmiddleware.Stack(platformmiddleware.Config{
			ServiceName:        cfg.ServiceName,
			ServiceVersion:     cfg.ServiceVersion,
			ServiceEnvironment: cfg.ServiceEnvironment,
			Metrics:            cfg.Metrics,
			RoutePattern:       platformmiddleware.ChiRoutePattern(router),
		}, next)
	})
	router.Use(platformmiddleware.Timeout(cfg.RequestTimeout))
	router.Use(health.RejectWhileDraining)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, r, httpapi.NotFound("common.not_found").
			Public("요청한 API를 찾을 수 없습니다.").
			New("API route not found"))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, r, httpapi.MethodNotAllowed("common.method_not_allowed").
			Public("허용되지 않은 HTTP 메서드입니다.").
			New("HTTP method not allowed"))
	})

	router.Route("/api/v1", func(api chi.Router) {
		user.RegisterRoutes(api, userHandler)
	})
	if proofHandler != nil {
		development.RegisterRoutes(router, proofHandler)
	}
	return router, nil
}
