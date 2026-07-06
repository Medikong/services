package app

import (
	"context"
	nethttp "net/http"

	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
	"github.com/Medikong/services/services/user-service/internal/platform/config"
	userhttp "github.com/Medikong/services/services/user-service/internal/transport/http"
)

type App struct {
	server *nethttp.Server
}

func New(ctx context.Context, cfg config.Config) (App, error) {
	var store user.Repository
	if cfg.DatabaseURL != "" {
		postgres, err := user.OpenPostgresRepository(ctx, cfg.Postgres)
		if err != nil {
			return App{}, err
		}
		store = postgres
	} else {
		store = user.NewMemoryRepository()
	}

	mux := nethttp.NewServeMux()
	userhttp.RegisterRoutes(mux, user.NewService(store))

	return App{
		server: httpserver.New(cfg.HTTPAddr, telemetry.Middleware(config.ServiceName, mux)),
	}, nil
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}
