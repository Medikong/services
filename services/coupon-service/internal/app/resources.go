package app

import (
	"context"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	platformredis "github.com/Medikong/services/packages/go-platform/redis"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/jackc/pgx/v5/pgxpool"
	redis "github.com/redis/go-redis/v9"
	"github.com/samber/oops"
)

type Resources struct {
	DB                *pgxpool.Pool
	Redis             *redis.Client
	RedisStartupError error
}

func openResources(ctx context.Context, postgres platformdb.PostgresConfig, redisConfig config.RedisConfig) (Resources, error) {
	db, err := platformdb.OpenPostgres(ctx, postgres)
	if err != nil {
		return Resources{}, oops.In("coupon_resources").Code("coupon.database_open_failed").Wrap(err)
	}
	resources := Resources{DB: db}
	if !redisConfig.Enabled {
		return resources, nil
	}
	client, err := platformredis.Open(ctx, redisConfig.Client)
	if err != nil {
		wrapped := oops.In("coupon_resources").Code("coupon.redis_open_failed").Wrap(err)
		if redisConfig.FailureMode == config.RedisFailureDBFallback {
			resources.RedisStartupError = wrapped
			return resources, nil
		}
		db.Close()
		return Resources{}, wrapped
	}
	resources.Redis = client
	return resources, nil
}

func (r *Resources) Close() error {
	var redisErr error
	if r.Redis != nil {
		if err := r.Redis.Close(); err != nil {
			redisErr = oops.In("coupon_resources").Code("coupon.redis_close_failed").Wrap(err)
		}
		r.Redis = nil
	}
	if r.DB != nil {
		r.DB.Close()
		r.DB = nil
	}
	return redisErr
}
