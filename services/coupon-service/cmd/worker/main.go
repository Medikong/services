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
	bootstrapLog := logger.Configure(os.Stdout, config.ServiceName+"-worker", redaction)
	cfg, err := config.LoadWorker()
	if err != nil {
		bootstrapLog.ErrorContext(ctx, "config load failed", logger.Err(err))
		return 1
	}
	log := logger.Configure(os.Stdout, cfg.Service.Name+"-worker", redaction)
	shutdownTelemetry, err := telemetry.Init(ctx, cfg.Service.Name+"-worker")
	if err != nil {
		log.ErrorContext(ctx, "telemetry init failed", logger.Err(err))
		return 1
	}
	worker, err := app.NewWorker(ctx, cfg)
	if err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout)
		shutdownErr := shutdownTelemetry(shutdownCtx)
		cancel()
		log.ErrorContext(ctx, "worker init failed", logger.Err(oops.Join(err, shutdownErr)))
		return 1
	}
	log.InfoContext(ctx, "worker starting", "admin_addr", cfg.AdminAddr)
	runErr := worker.Run(ctx)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout)
	telemetryErr := shutdownTelemetry(shutdownCtx)
	cancel()
	if err := oops.Join(runErr, telemetryErr); err != nil {
		log.ErrorContext(context.Background(), "worker stopped with error", logger.Err(err))
		return 1
	}
	log.InfoContext(context.Background(), "worker stopped")
	return 0
}
