package config

import (
	platformconfig "github.com/Medikong/services/packages/go-platform/config"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
)

const ServiceName = "user-service"

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	Postgres    platformdb.PostgresConfig
}

func Load() (Config, error) {
	databaseURL := platformconfig.String("DATABASE_URL", "")
	postgres, err := platformdb.LoadPostgresConfigFromEnv(databaseURL)
	if err != nil {
		return Config{}, err
	}
	return Config{
		HTTPAddr:    platformconfig.String("HTTP_ADDR", ":8080"),
		DatabaseURL: databaseURL,
		Postgres:    postgres,
	}, nil
}
