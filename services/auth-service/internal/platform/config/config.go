package config

import (
	"strings"
	"time"

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
	JWTSecret         string
	JWTIssuer         string
	AccessTokenTTL    time.Duration
	RefreshTokenTTL   time.Duration
	DevTestToken      bool
}

func Load() (Config, error) {
	databaseURL := platformconfig.String("DATABASE_URL", "")
	postgres, err := platformdb.LoadPostgresConfigFromEnv(databaseURL)
	if err != nil {
		return Config{}, err
	}
	accessTokenTTLSeconds, err := platformconfig.Int("AUTH_TOKEN_TTL_SECONDS", 900)
	if err != nil {
		return Config{}, err
	}
	refreshTokenTTLSeconds, err := platformconfig.Int("AUTH_REFRESH_TOKEN_TTL_SECONDS", 604800)
	if err != nil {
		return Config{}, err
	}
	return Config{
		HTTPAddr:          platformconfig.String("HTTP_ADDR", ":8080"),
		DatabaseURL:       databaseURL,
		Postgres:          postgres,
		AuthzCacheEnabled: strings.EqualFold(platformconfig.String("AUTHZ_CACHE_ENABLED", "false"), "true"),
		RedisURL:          platformconfig.String("REDIS_URL", ""),
		JWTSecret:         platformconfig.String("JWT_SECRET", ""),
		JWTIssuer:         platformconfig.String("JWT_ISSUER", ServiceName),
		AccessTokenTTL:    time.Duration(accessTokenTTLSeconds) * time.Second,
		RefreshTokenTTL:   time.Duration(refreshTokenTTLSeconds) * time.Second,
		DevTestToken:      strings.EqualFold(platformconfig.String("AUTH_DEV_TEST_TOKEN_ENABLED", "false"), "true"),
	}, nil
}
