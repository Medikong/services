package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/telemetry"
	"github.com/Medikong/services/services/user-service/internal/app"
	"github.com/Medikong/services/services/user-service/internal/platform/config"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.LoadServer()
	if err != nil {
		logger.Configure(os.Stdout, config.ServiceName).ErrorContext(ctx, "config load failed", logger.Err(safeError(err)))
		return 1
	}
	log := logger.Configure(os.Stdout, cfg.Service.Name, logger.WithReplaceAttr(logger.RedactKeys(
		"authorization", "cookie", "token", "proof", "registration_completion_proof", "media_asset_proof",
		"user_creation_proof", "user_status_change_proof", "private_name", "database_url", "secret", "key",
	)))
	shutdownTelemetry, err := telemetry.Init(ctx, cfg.Service.Name)
	if err != nil {
		log.ErrorContext(ctx, "telemetry init failed", logger.Err(safeError(err)))
		return 1
	}
	server, err := app.NewServer(ctx, cfg)
	if err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout)
		shutdownErr := shutdownTelemetry(shutdownCtx)
		cancel()
		log.ErrorContext(ctx, "server init failed", logger.Err(safeError(oops.Join(err, shutdownErr))))
		return 1
	}
	log.InfoContext(ctx, "server starting", "http_addr", cfg.HTTP.PublicAddr, "admin_addr", cfg.HTTP.AdminAddr)
	runErr := server.Run(ctx)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout)
	telemetryErr := shutdownTelemetry(shutdownCtx)
	cancel()
	if err := oops.Join(runErr, telemetryErr); err != nil {
		log.ErrorContext(context.Background(), "server stopped with error", logger.Err(safeError(err)))
		return 1
	}
	log.InfoContext(context.Background(), "server stopped")
	return 0
}

func safeError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for _, name := range []string{
		"DATABASE_URL", "USER_SIGNING_PRIVATE_KEY", "AUTH_PROOF_PUBLIC_KEY", "MEDIA_PROOF_PUBLIC_KEY",
		"USER_PRIVATE_NAME_ENCRYPTION_KEY", "USER_DEV_ACCESS_TOKEN", "USER_DEV_AUTH_SIGNING_PRIVATE_KEY", "USER_DEV_MEDIA_SIGNING_PRIVATE_KEY",
	} {
		if value := os.Getenv(name); value != "" {
			message = strings.ReplaceAll(message, value, "[REDACTED]")
		}
	}
	return errors.New(message)
}
