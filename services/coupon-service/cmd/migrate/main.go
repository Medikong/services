package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
)

func main() {
	os.Exit(run())
}

func run() int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := logger.Configure(os.Stdout, config.ServiceName+"-migrate", logger.WithReplaceAttr(logger.RedactKeys(
		"password", "secret", "token", "database_url", "redis_url",
	)))
	cfg, err := config.LoadMigration()
	if err != nil {
		log.ErrorContext(signalCtx, "config load failed", logger.Err(err))
		return 1
	}
	ctx, cancel := context.WithTimeout(signalCtx, cfg.Timeout)
	defer cancel()
	db, err := platformdb.OpenPostgres(ctx, cfg.Postgres)
	if err != nil {
		log.ErrorContext(ctx, "database open failed", logger.Err(err))
		return 1
	}
	defer db.Close()
	if err := migration.Migrate(ctx, db); err != nil {
		log.ErrorContext(ctx, "coupon migration failed", logger.Err(err))
		return 1
	}
	log.InfoContext(ctx, "migration completed")
	return 0
}
