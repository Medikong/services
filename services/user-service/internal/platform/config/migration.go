package config

import (
	"errors"
	"os"
	"strings"
	"time"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
)

type MigrationConfig struct {
	Postgres platformdb.PostgresConfig
	Timeout  time.Duration
}

func LoadMigration() (MigrationConfig, error) {
	postgres, err := platformdb.LoadPostgresConfigFromEnv(strings.TrimSpace(os.Getenv("DATABASE_URL")))
	if err != nil {
		return MigrationConfig{}, err
	}
	timeout, err := durationEnv("MIGRATION_TIMEOUT", 5*time.Minute)
	if err != nil {
		return MigrationConfig{}, err
	}
	cfg := MigrationConfig{Postgres: postgres, Timeout: timeout}
	if strings.TrimSpace(cfg.Postgres.DatabaseURL) == "" || cfg.Timeout <= 0 {
		return MigrationConfig{}, errors.New("DATABASE_URL and a positive MIGRATION_TIMEOUT are required")
	}
	return cfg, nil
}
