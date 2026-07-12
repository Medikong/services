package config

import (
	"os"
	"strings"
	"time"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type MigrationConfig struct {
	Postgres             platformdb.PostgresConfig
	AuditSinkDatabaseURL string
	Timeout              time.Duration
}

func LoadMigration() (MigrationConfig, error) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	postgres, err := platformdb.LoadPostgresConfigFromEnv(databaseURL)
	if err != nil {
		return MigrationConfig{}, configErr.With("config", "migration", "setting", "postgres").Wrap(err)
	}
	timeout, err := durationEnv("MIGRATION_TIMEOUT", 5*time.Minute)
	if err != nil {
		return MigrationConfig{}, err
	}
	cfg := MigrationConfig{
		Postgres:             postgres,
		AuditSinkDatabaseURL: strings.TrimSpace(os.Getenv("AUDIT_SINK_DATABASE_URL")),
		Timeout:              timeout,
	}
	if err := cfg.Validate(); err != nil {
		return MigrationConfig{}, err
	}
	return cfg, nil
}

func (c MigrationConfig) Validate() error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.Postgres, validation.By(func(any) error {
			return validation.Validate(c.Postgres.DatabaseURL, validation.Required)
		})),
		validation.Field(&c.Timeout, validation.Min(time.Nanosecond)),
	)
	if err != nil {
		return configErr.With("config", "migration").Wrap(err)
	}
	return nil
}
