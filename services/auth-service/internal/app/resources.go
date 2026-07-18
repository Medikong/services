package app

import (
	"context"
	"strings"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

type Resources struct {
	DB                 *pgxpool.Pool
	AuditSink          *pgxpool.Pool
	SessionStatusRedis *redis.Client
}

func openServerResources(ctx context.Context, cfg config.ServerConfig) (Resources, error) {
	db, err := openDatabase(ctx, cfg.Postgres)
	if err != nil {
		return Resources{}, err
	}
	cache, err := openSessionStatusRedis(cfg.Auth.SessionStatusRedisURL)
	if err != nil {
		db.Close()
		return Resources{}, err
	}
	return Resources{DB: db, SessionStatusRedis: cache}, nil
}

func openWorkerResources(ctx context.Context, cfg config.WorkerConfig) (Resources, error) {
	db, err := openDatabase(ctx, cfg.Postgres)
	if err != nil {
		return Resources{}, err
	}
	cache, err := openSessionStatusRedis(cfg.Auth.SessionStatusRedisURL)
	if err != nil {
		db.Close()
		return Resources{}, err
	}
	sinkURL := strings.TrimSpace(cfg.Audit.SinkDatabaseURL)
	if sinkURL == "" || sinkURL == strings.TrimSpace(cfg.Postgres.DatabaseURL) {
		return Resources{DB: db, AuditSink: db, SessionStatusRedis: cache}, nil
	}
	sinkConfig := cfg.Postgres
	sinkConfig.DatabaseURL = sinkURL
	sink, err := platformdb.OpenPostgres(ctx, sinkConfig)
	if err != nil {
		_ = cache.Close()
		db.Close()
		return Resources{}, oops.In("auth_resources").Code("audit_sink.open_failed").Wrap(err)
	}
	return Resources{DB: db, AuditSink: sink, SessionStatusRedis: cache}, nil
}

func openSessionStatusRedis(rawURL string) (*redis.Client, error) {
	options, err := redis.ParseURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, oops.In("auth_resources").Code("session_status_redis.invalid_url").Wrap(err)
	}
	return redis.NewClient(options), nil
}

func openDatabase(ctx context.Context, cfg platformdb.PostgresConfig) (*pgxpool.Pool, error) {
	db, err := platformdb.OpenPostgres(ctx, cfg)
	if err != nil {
		return nil, oops.In("auth_resources").Code("database.open_failed").Wrap(err)
	}
	return db, nil
}

func (r *Resources) Close() error {
	var cacheErr error
	if r.SessionStatusRedis != nil {
		cacheErr = r.SessionStatusRedis.Close()
		r.SessionStatusRedis = nil
	}
	if r.AuditSink != nil && r.AuditSink != r.DB {
		r.AuditSink.Close()
	}
	r.AuditSink = nil
	if r.DB != nil {
		r.DB.Close()
		r.DB = nil
	}
	return cacheErr
}
