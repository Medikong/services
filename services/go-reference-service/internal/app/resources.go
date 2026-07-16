package app

import (
	"context"
	"strings"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	platformredis "github.com/Medikong/services/packages/go-platform/redisutil"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

type Resources struct {
	DB        *pgxpool.Pool
	AuditSink *pgxpool.Pool
	Redis     *redis.Client
}

func openServerResources(ctx context.Context, cfg config.ServerConfig) (Resources, error) {
	db, err := openDatabase(ctx, cfg.Postgres)
	if err != nil {
		return Resources{}, err
	}
	client, err := platformredis.Open(ctx, cfg.Redis)
	if err != nil {
		db.Close()
		return Resources{}, err
	}
	return Resources{DB: db, Redis: client}, nil
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
		return Resources{}, oops.In("reference_resources").Code("audit_sink.open_failed").Wrap(err)
	}
	return Resources{DB: db, AuditSink: sink}, nil
}

func openDatabase(ctx context.Context, cfg platformdb.PostgresConfig) (*pgxpool.Pool, error) {
	db, err := platformdb.OpenPostgres(ctx, cfg)
	if err != nil {
		return nil, oops.In("reference_resources").Code("database.open_failed").Wrap(err)
	}
	return db, nil
}

func (r *Resources) Close() error {
	var errs []error
	if r.Redis != nil {
		if err := r.Redis.Close(); err != nil {
			errs = append(errs, oops.In("reference_resources").Code("redis.close_failed").Wrap(err))
		}
		r.Redis = nil
	}
	if r.AuditSink != nil && r.AuditSink != r.DB {
		r.AuditSink.Close()
	}
	r.AuditSink = nil
	if r.DB != nil {
		r.DB.Close()
		r.DB = nil
	}
	return oops.Join(errs...)
}
