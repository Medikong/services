package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
	"github.com/Medikong/services/services/user-service/internal/platform/config"
)

func main() {
	os.Exit(run())
}

func run() int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := logger.Configure(os.Stdout, config.ServiceName+"-migrate", logger.WithReplaceAttr(logger.RedactKeys("database_url", "secret", "key")))
	cfg, err := config.LoadMigration()
	if err != nil {
		log.ErrorContext(signalCtx, "config load failed", logger.Err(safeMigrationError(err)))
		return 1
	}
	ctx, cancel := context.WithTimeout(signalCtx, cfg.Timeout)
	defer cancel()
	db, err := platformdb.OpenPostgres(ctx, cfg.Postgres)
	if err != nil {
		log.ErrorContext(ctx, "database open failed", logger.Err(safeMigrationError(err)))
		return 1
	}
	defer db.Close()
	if err := user.Migrate(ctx, db); err != nil {
		log.ErrorContext(ctx, "user migration failed", logger.Err(safeMigrationError(err)))
		return 1
	}
	log.InfoContext(ctx, "migration completed")
	return 0
}

func safeMigrationError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if value := os.Getenv("DATABASE_URL"); value != "" {
		message = strings.ReplaceAll(message, value, "[REDACTED]")
	}
	return errors.New(message)
}
