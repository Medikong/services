package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/coupon-service/internal/app"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	redaction := logger.WithReplaceAttr(logger.RedactKeys(
		"authorization", "cookie", "password", "secret", "token", "coupon_code", "raw_code", "code",
		"payload", "external_payload", "approval_payload", "profile", "snapshot", "database_url", "redis_url",
	))
	bootstrapLog := logger.Configure(os.Stdout, config.ServiceName, redaction)
	cfg, err := config.LoadServer()
	if err != nil {
		bootstrapLog.ErrorContext(ctx, "config load failed", logger.Err(err))
		return 1
	}
	log := logger.Configure(os.Stdout, cfg.Service.Name, redaction)
	shutdownTelemetry, err := telemetry.Init(ctx, cfg.Service.Name)
	if err != nil {
		log.ErrorContext(ctx, "telemetry init failed", logger.Err(err))
		return 1
	}
	server, err := app.NewServer(ctx, cfg)
	if err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout)
		shutdownErr := shutdownTelemetry(shutdownCtx)
		cancel()
		log.ErrorContext(ctx, "server init failed", logger.Err(oops.Join(err, shutdownErr)))
		return 1
	}
	log.InfoContext(ctx, "server starting", "http_addr", cfg.HTTP.PublicAddr, "admin_addr", cfg.HTTP.AdminAddr)
	runErr := server.Run(ctx)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout)
	telemetryErr := shutdownTelemetry(shutdownCtx)
	cancel()
	if err := oops.Join(runErr, telemetryErr); err != nil {
		log.ErrorContext(context.Background(), "server stopped with error", logger.Err(err))
		return 1
	}
	log.InfoContext(context.Background(), "server stopped")
	return 0
}
