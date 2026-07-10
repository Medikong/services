package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Medikong/services/packages/go-audit"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/go-reference-service/internal/domain/sample"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/config"
)

func main() {
	os.Exit(run())
}

func run() int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := logger.Configure(os.Stdout, config.ServiceName+"-migrate")
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
	if err := audit.Migrate(ctx, db); err != nil {
		log.ErrorContext(ctx, "audit source migration failed", logger.Err(err))
		return 1
	}
	if err := sample.Migrate(ctx, db); err != nil {
		log.ErrorContext(ctx, "source migration failed", logger.Err(err))
		return 1
	}

	if cfg.AuditSinkDatabaseURL != "" && cfg.AuditSinkDatabaseURL != cfg.Postgres.DatabaseURL {
		sinkConfig := cfg.Postgres
		sinkConfig.DatabaseURL = cfg.AuditSinkDatabaseURL
		sink, err := platformdb.OpenPostgres(ctx, sinkConfig)
		if err != nil {
			log.ErrorContext(ctx, "audit sink open failed", logger.Err(err))
			return 1
		}
		defer sink.Close()
		if err := audit.Migrate(ctx, sink); err != nil {
			log.ErrorContext(ctx, "audit sink migration failed", logger.Err(err))
			return 1
		}
	}

	log.InfoContext(ctx, "migration completed")
	return 0
}
