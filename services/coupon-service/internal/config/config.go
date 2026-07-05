package config

import platformconfig "github.com/Medikong/services/packages/go-platform/config"

const ServiceName = "coupon-service"

type Config struct {
	HTTPAddr                string
	DatabaseURL             string
	RedisGateEnabled        string
	RedisURL                string
	RedisGateFailureMode    string
	RedisGatePendingTTL     string
	RedisGateIdempotencyTTL string
}

func Load() Config {
	return Config{
		HTTPAddr:                platformconfig.String("HTTP_ADDR", ":8080"),
		DatabaseURL:             platformconfig.String("DATABASE_URL", ""),
		RedisGateEnabled:        platformconfig.String("COUPON_REDIS_GATE_ENABLED", "false"),
		RedisURL:                platformconfig.String("REDIS_URL", ""),
		RedisGateFailureMode:    platformconfig.String("COUPON_REDIS_GATE_FAILURE_MODE", "db_fallback"),
		RedisGatePendingTTL:     platformconfig.String("COUPON_REDIS_GATE_PENDING_TTL", "30s"),
		RedisGateIdempotencyTTL: platformconfig.String("COUPON_REDIS_GATE_IDEMPOTENCY_TTL", "24h"),
	}
}
