package config

import (
	"os"
	"strings"
	"time"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type WorkerConfig struct {
	Service   ServiceConfig
	AdminAddr string
	Lifecycle LifecycleConfig
	Postgres  platformdb.PostgresConfig
	Audit     AuditConfig
	Profile   ProfileConfig
}

func LoadWorker() (WorkerConfig, error) {
	lifecycle, err := loadLifecycle()
	if err != nil {
		return WorkerConfig{}, err
	}
	profile, err := loadProfile()
	if err != nil {
		return WorkerConfig{}, err
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	postgres, err := platformdb.LoadPostgresConfigFromEnv(databaseURL)
	if err != nil {
		return WorkerConfig{}, configErr.With("config", "worker", "setting", "postgres").Wrap(err)
	}
	auditConfig, err := loadAudit()
	if err != nil {
		return WorkerConfig{}, err
	}
	cfg := WorkerConfig{
		Service:   loadService(),
		AdminAddr: stringEnv("WORKER_ADMIN_ADDR", ":9092"),
		Lifecycle: lifecycle,
		Postgres:  postgres,
		Audit:     auditConfig,
		Profile:   profile,
	}
	if err := cfg.Validate(); err != nil {
		return WorkerConfig{}, err
	}
	return cfg, nil
}

func (c WorkerConfig) Validate() error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.Service),
		validation.Field(&c.AdminAddr, validation.Required),
		validation.Field(&c.Lifecycle),
		validation.Field(&c.Postgres, validation.By(func(any) error {
			return validation.Validate(c.Postgres.DatabaseURL, validation.Required)
		})),
		validation.Field(&c.Audit),
		validation.Field(&c.Profile),
	)
	if err != nil {
		return configErr.With("config", "worker").Wrap(err)
	}
	return nil
}

func loadAudit() (AuditConfig, error) {
	batchSize, err := intEnv("AUDIT_BATCH_SIZE", 50)
	if err != nil {
		return AuditConfig{}, err
	}
	pollInterval, err := durationEnv("AUDIT_POLL_INTERVAL", time.Second)
	if err != nil {
		return AuditConfig{}, err
	}
	lease, err := durationEnv("AUDIT_LEASE", 30*time.Second)
	if err != nil {
		return AuditConfig{}, err
	}
	publishTimeout, err := durationEnv("AUDIT_PUBLISH_TIMEOUT", 10*time.Second)
	if err != nil {
		return AuditConfig{}, err
	}
	maxAttempts, err := intEnv("AUDIT_MAX_ATTEMPTS", 10)
	if err != nil {
		return AuditConfig{}, err
	}
	baseBackoff, err := durationEnv("AUDIT_BASE_BACKOFF", time.Second)
	if err != nil {
		return AuditConfig{}, err
	}
	maxBackoff, err := durationEnv("AUDIT_MAX_BACKOFF", time.Minute)
	if err != nil {
		return AuditConfig{}, err
	}
	retention, err := durationEnv("AUDIT_OUTBOX_RETENTION", 7*24*time.Hour)
	if err != nil {
		return AuditConfig{}, err
	}
	cleanupLimit, err := intEnv("AUDIT_CLEANUP_LIMIT", 1000)
	if err != nil {
		return AuditConfig{}, err
	}
	return AuditConfig{
		SinkDatabaseURL: strings.TrimSpace(os.Getenv("AUDIT_SINK_DATABASE_URL")),
		BatchSize:       batchSize,
		PollInterval:    pollInterval,
		Lease:           lease,
		PublishTimeout:  publishTimeout,
		MaxAttempts:     maxAttempts,
		BaseBackoff:     baseBackoff,
		MaxBackoff:      maxBackoff,
		Retention:       retention,
		CleanupLimit:    cleanupLimit,
	}, nil
}
