package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-audit"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/auth-service/internal/auth"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
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
	if err := auth.Migrate(ctx, db); err != nil {
		log.ErrorContext(ctx, "authentication migration failed", logger.Err(err))
		return 1
	}
	if cfg.Development.VirtualAdaptersEnabled {
		if err := auth.MigrateDevelopment(ctx, db); err != nil {
			log.ErrorContext(ctx, "development authentication migration failed", logger.Err(err))
			return 1
		}
	}
	if err := migrateAuditSink(ctx, cfg); err != nil {
		log.ErrorContext(ctx, "audit sink migration failed", logger.Err(err))
		return 1
	}
	log.InfoContext(ctx, "migration completed")
	return 0
}

func migrateAuditSink(ctx context.Context, cfg config.MigrationConfig) error {
	if cfg.AuditSinkDatabaseURL == "" || cfg.AuditSinkDatabaseURL == cfg.Postgres.DatabaseURL {
		return nil
	}
	sinkConfig := cfg.Postgres
	sinkConfig.DatabaseURL = cfg.AuditSinkDatabaseURL
	sink, err := platformdb.OpenPostgres(ctx, sinkConfig)
	if err != nil {
		return oops.In("auth_migrate").Code("audit_sink.open_failed").Wrap(err)
	}
	defer sink.Close()
	return audit.Migrate(ctx, sink)
}
