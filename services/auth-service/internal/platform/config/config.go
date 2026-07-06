package config

import (
	"strings"

	platformconfig "github.com/Medikong/services/packages/go-platform/config"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
)

const ServiceName = "auth-service"

type Config struct {
	HTTPAddr          string
	DatabaseURL       string
	Postgres          platformdb.PostgresConfig
	AuthzCacheEnabled bool
	RedisURL          string
}

func Load() (Config, error) {
	databaseURL := platformconfig.String("DATABASE_URL", "")
	postgres, err := platformdb.LoadPostgresConfigFromEnv(databaseURL)
	if err != nil {
		return Config{}, err
	}
	return Config{
		HTTPAddr:          platformconfig.String("HTTP_ADDR", ":8080"),
		DatabaseURL:       databaseURL,
		Postgres:          postgres,
		AuthzCacheEnabled: strings.EqualFold(platformconfig.String("AUTHZ_CACHE_ENABLED", "false"), "true"),
		RedisURL:          platformconfig.String("REDIS_URL", ""),
	}, nil
}
