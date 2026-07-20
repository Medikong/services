package app

import (
	"context"
	"strings"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/redisutil"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

type Resources struct {
	DB        *pgxpool.Pool
	AuditSink *pgxpool.Pool
	Redis     *goredis.Client
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
	var redisClient *goredis.Client
	if cfg.SessionStatus.Enabled {
		redisClient, err = redisutil.Open(ctx, cfg.SessionStatus.Redis)
		if err != nil {
			db.Close()
			return Resources{}, oops.In("auth_resources").Code("session_projection.redis_open_failed").Wrap(err)
		}
	}
	sinkURL := strings.TrimSpace(cfg.Audit.SinkDatabaseURL)
	if sinkURL == "" || sinkURL == strings.TrimSpace(cfg.Postgres.DatabaseURL) {
		return Resources{DB: db, AuditSink: db, Redis: redisClient}, nil
	}
	sinkConfig := cfg.Postgres
	sinkConfig.DatabaseURL = sinkURL
	sink, err := platformdb.OpenPostgres(ctx, sinkConfig)
	if err != nil {
		if redisClient != nil {
			_ = redisClient.Close()
		}
		db.Close()
		return Resources{}, oops.In("auth_resources").Code("audit_sink.open_failed").Wrap(err)
	}
	return Resources{DB: db, AuditSink: sink, Redis: redisClient}, nil
}

func openDatabase(ctx context.Context, cfg platformdb.PostgresConfig) (*pgxpool.Pool, error) {
	db, err := platformdb.OpenPostgres(ctx, cfg)
	if err != nil {
		return nil, oops.In("auth_resources").Code("database.open_failed").Wrap(err)
	}
	return db, nil
}

func (r *Resources) Close() error {
	var closeErr error
	if r.Redis != nil {
		if err := r.Redis.Close(); err != nil {
			closeErr = oops.In("auth_resources").Code("session_projection.redis_close_failed").Wrap(err)
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
	return closeErr
}
