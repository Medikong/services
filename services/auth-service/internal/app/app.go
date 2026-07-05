package app

import (
	"context"
	"net/http"

	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/config"
	"github.com/Medikong/services/services/auth-service/internal/handler"
	"github.com/Medikong/services/services/auth-service/internal/service"
	"github.com/Medikong/services/services/auth-service/internal/store/memory"
	postgresstore "github.com/Medikong/services/services/auth-service/internal/store/postgres"
)

type App struct {
	server *http.Server
}

func New(ctx context.Context, cfg config.Config) (App, error) {
	var store service.Store
	if cfg.DatabaseURL != "" {
		postgres, err := postgresstore.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return App{}, err
		}
		store = postgres
	} else {
		store = memory.New()
	}

	options := []service.Option{}
	if cfg.AuthzCacheEnabled {
		options = append(options, service.WithAuthzCache(service.NewMemoryAuthzCache()))
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, service.New(store, options...))

	return App{
		server: httpserver.New(cfg.HTTPAddr, telemetry.Middleware(config.ServiceName, mux)),
	}, nil
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}
