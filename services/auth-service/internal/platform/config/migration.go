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
	Development          DevelopmentConfig
	Timeout              time.Duration
}

func LoadMigration() (MigrationConfig, error) {
	service := loadService()
	postgres, err := platformdb.LoadPostgresConfigFromEnv(strings.TrimSpace(os.Getenv("DATABASE_URL")))
	if err != nil {
		return MigrationConfig{}, configErr.With("config", "migration", "setting", "postgres").Wrap(err)
	}
	timeout, err := durationEnv("MIGRATION_TIMEOUT", 5*time.Minute)
	if err != nil {
		return MigrationConfig{}, err
	}
	development, err := loadDevelopment()
	if err != nil {
		return MigrationConfig{}, err
	}
	cfg := MigrationConfig{
		Postgres:             postgres,
		AuditSinkDatabaseURL: strings.TrimSpace(os.Getenv("AUDIT_SINK_DATABASE_URL")),
		Development:          development,
		Timeout:              timeout,
	}
	if err := cfg.Validate(service.Environment); err != nil {
		return MigrationConfig{}, err
	}
	return cfg, nil
}

func (c MigrationConfig) Validate(environment string) error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.Postgres, validation.By(func(any) error {
			return validation.Validate(c.Postgres.DatabaseURL, validation.Required)
		})),
		validation.Field(&c.Timeout, validation.Min(time.Nanosecond)),
	)
	if err != nil {
		return configErr.With("config", "migration").Wrap(err)
	}
	if err := c.Development.Validate(AllowsDevelopmentFeatures(environment)); err != nil {
		return configErr.With("config", "migration", "section", "development").Wrap(err)
	}
	return nil
}
