package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/auth-service/internal/app"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := logger.Configure(os.Stdout, config.ServiceName)
	cfg, err := config.Load()
	if err != nil {
		log.ErrorContext(ctx, "config load failed", logger.Err(err))
		os.Exit(1)
	}
	shutdownTelemetry, err := telemetry.Init(ctx, config.ServiceName)
	if err != nil {
		log.ErrorContext(ctx, "telemetry init failed", logger.Err(err))
		os.Exit(1)
	}
	defer func() {
		if err := shutdownTelemetry(context.Background()); err != nil {
			log.ErrorContext(context.Background(), "telemetry shutdown failed", logger.Err(err))
		}
	}()

	application, err := app.New(ctx, cfg)
	if err != nil {
		log.ErrorContext(ctx, "service init failed", logger.Err(err))
		os.Exit(1)
	}
	log.InfoContext(ctx, "service starting", "addr", cfg.HTTPAddr)
	if err := application.Run(ctx); err != nil {
		log.ErrorContext(ctx, "service stopped with error", logger.Err(err))
		os.Exit(1)
	}
	log.InfoContext(ctx, "service stopped")
}
