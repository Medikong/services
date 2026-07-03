package app

import (
	"context"
	"net/http"

	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/services/user-service/internal/config"
	"github.com/Medikong/services/services/user-service/internal/handler"
)

type App struct {
	server *http.Server
}

func New(cfg config.Config) App {
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	return App{
		server: httpserver.New(cfg.HTTPAddr, mux),
	}
}

func (a App) Run(ctx context.Context) error {
	return httpserver.ListenAndServe(ctx, a.server)
}
