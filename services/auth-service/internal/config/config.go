package config

import (
	"strings"

	platformconfig "github.com/Medikong/services/packages/go-platform/config"
)

const ServiceName = "auth-service"

type Config struct {
	HTTPAddr          string
	DatabaseURL       string
	AuthzCacheEnabled bool
	RedisURL          string
}

func Load() Config {
	return Config{
		HTTPAddr:          platformconfig.String("HTTP_ADDR", ":8080"),
		DatabaseURL:       platformconfig.String("DATABASE_URL", ""),
		AuthzCacheEnabled: strings.EqualFold(platformconfig.String("AUTHZ_CACHE_ENABLED", "false"), "true"),
		RedisURL:          platformconfig.String("REDIS_URL", ""),
	}
}
