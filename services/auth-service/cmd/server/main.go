package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/auth-service/internal/app"
	"github.com/Medikong/services/services/auth-service/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	log := logger.Configure(os.Stdout, config.ServiceName)

	application := app.New(cfg)
	log.InfoContext(ctx, "service starting", "addr", cfg.HTTPAddr)
	if err := application.Run(ctx); err != nil {
		log.ErrorContext(ctx, "service stopped with error", logger.Err(err))
		os.Exit(1)
	}
	log.InfoContext(ctx, "service stopped")
}
