package app

import (
	"context"
	"strings"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type Resources struct {
	DB        *pgxpool.Pool
	AuditSink *pgxpool.Pool
}

func openServerResources(ctx context.Context, cfg config.ServerConfig) (Resources, error) {
	db, err := openDatabase(ctx, cfg.Postgres)
	if err != nil {
		return Resources{}, err
	}
	return Resources{DB: db}, nil
}

func openWorkerResources(ctx context.Context, cfg config.WorkerConfig) (Resources, error) {
	db, err := openDatabase(ctx, cfg.Postgres)
	if err != nil {
		return Resources{}, err
	}
	sinkURL := strings.TrimSpace(cfg.Audit.SinkDatabaseURL)
	if sinkURL == "" || sinkURL == strings.TrimSpace(cfg.Postgres.DatabaseURL) {
		return Resources{DB: db, AuditSink: db}, nil
	}
	sinkConfig := cfg.Postgres
	sinkConfig.DatabaseURL = sinkURL
	sink, err := platformdb.OpenPostgres(ctx, sinkConfig)
	if err != nil {
		db.Close()
		return Resources{}, oops.In("auth_resources").Code("audit_sink.open_failed").Wrap(err)
	}
	return Resources{DB: db, AuditSink: sink}, nil
}

func openDatabase(ctx context.Context, cfg platformdb.PostgresConfig) (*pgxpool.Pool, error) {
	db, err := platformdb.OpenPostgres(ctx, cfg)
	if err != nil {
		return nil, oops.In("auth_resources").Code("database.open_failed").Wrap(err)
	}
	return db, nil
}

func (r *Resources) Close() error {
	if r.AuditSink != nil && r.AuditSink != r.DB {
		r.AuditSink.Close()
	}
	r.AuditSink = nil
	if r.DB != nil {
		r.DB.Close()
		r.DB = nil
	}
	return nil
}
