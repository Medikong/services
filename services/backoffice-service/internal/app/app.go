package app

import (
	"context"
	nethttp "net/http"

	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/backoffice-service/internal/domain/drop"
	"github.com/Medikong/services/services/backoffice-service/internal/platform/config"
	backofficehttp "github.com/Medikong/services/services/backoffice-service/internal/transport/http"
)

type App struct {
	server *nethttp.Server
}

func New(ctx context.Context, cfg config.Config) (App, error) {
	var store drop.Repository
	if cfg.DatabaseURL != "" {
		postgres, err := drop.OpenPostgresRepository(ctx, cfg.Postgres)
		if err != nil {
			return App{}, err
		}
		store = postgres
	} else {
		store = drop.NewMemoryRepository()
	}
	mux := nethttp.NewServeMux()
	backofficehttp.RegisterRoutes(mux, drop.NewService(store, drop.NewHTTPCouponClient(cfg.CouponServiceURL)))
	return App{server: httpserver.New(cfg.HTTPAddr, telemetry.Middleware(config.ServiceName, mux))}, nil
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}
