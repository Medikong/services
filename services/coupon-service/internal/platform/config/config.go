package config

import (
	platformconfig "github.com/Medikong/services/packages/go-platform/config"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
)

const ServiceName = "coupon-service"

type Config struct {
	HTTPAddr                string
	DatabaseURL             string
	Postgres                platformdb.PostgresConfig
	RedisGateEnabled        string
	RedisURL                string
	RedisGateFailureMode    string
	RedisGatePendingTTL     string
	RedisGateIdempotencyTTL string
}

func Load() (Config, error) {
	databaseURL := platformconfig.String("DATABASE_URL", "")
	postgres, err := platformdb.LoadPostgresConfigFromEnv(databaseURL)
	if err != nil {
		return Config{}, err
	}
	return Config{
		HTTPAddr:                platformconfig.String("HTTP_ADDR", ":8080"),
		DatabaseURL:             databaseURL,
		Postgres:                postgres,
		RedisGateEnabled:        platformconfig.String("COUPON_REDIS_GATE_ENABLED", "false"),
		RedisURL:                platformconfig.String("REDIS_URL", ""),
		RedisGateFailureMode:    platformconfig.String("COUPON_REDIS_GATE_FAILURE_MODE", "db_fallback"),
		RedisGatePendingTTL:     platformconfig.String("COUPON_REDIS_GATE_PENDING_TTL", "30s"),
		RedisGateIdempotencyTTL: platformconfig.String("COUPON_REDIS_GATE_IDEMPOTENCY_TTL", "24h"),
	}, nil
}
