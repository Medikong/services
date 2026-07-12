package config

import (
	"testing"
	"time"

	"github.com/samber/oops"
)

func TestLoadServerUsesGroupedDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/reference")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")

	cfg, err := LoadServer()
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	if cfg.Postgres.MaxConns != 10 {
		t.Fatalf("Postgres.MaxConns = %d, want 10", cfg.Postgres.MaxConns)
	}
	if cfg.Lock.TTL != 15*time.Second || cfg.Lock.Refresh != 5*time.Second {
		t.Fatalf("lock defaults = ttl:%s refresh:%s", cfg.Lock.TTL, cfg.Lock.Refresh)
	}
	if cfg.Profile.PprofEnabled {
		t.Fatal("Profile.PprofEnabled = true, want opt-in default")
	}
}

func TestLoadServerRejectsLockRefreshAtOrAboveTTL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/reference")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("REDIS_LOCK_TTL", "5s")
	t.Setenv("REDIS_LOCK_REFRESH_INTERVAL", "5s")

	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil, want invalid lock interval")
	}
}

func TestLoadWorkerIgnoresServerOnlySettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/reference")
	t.Setenv("REDIS_POOL_SIZE", "not-an-integer")
	t.Setenv("REDIS_LOCK_TTL", "not-a-duration")

	if _, err := LoadWorker(); err != nil {
		t.Fatalf("LoadWorker() error = %v", err)
	}
}

func TestLoadServerIgnoresWorkerOnlySettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/reference")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("AUDIT_BATCH_SIZE", "not-an-integer")

	if _, err := LoadServer(); err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
}

func TestLoadMigrationIgnoresRuntimeOnlySettings(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/reference")
	t.Setenv("REDIS_POOL_SIZE", "not-an-integer")
	t.Setenv("AUDIT_BATCH_SIZE", "not-an-integer")
	t.Setenv("PYROSCOPE_ENABLED", "not-a-boolean")

	if _, err := LoadMigration(); err != nil {
		t.Fatalf("LoadMigration() error = %v", err)
	}
}

func TestLoadWorkerRejectsLeaseWithoutPublishMargin(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/reference")
	t.Setenv("AUDIT_LEASE", "10s")
	t.Setenv("AUDIT_PUBLISH_TIMEOUT", "10s")

	if _, err := LoadWorker(); err == nil {
		t.Fatal("LoadWorker() error = nil, want invalid lease")
	}
}

func TestLoadServerWrapsSettingParseContext(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/reference")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("REDIS_POOL_SIZE", "invalid")

	_, err := LoadServer()
	if err == nil {
		t.Fatal("LoadServer() error = nil, want parse error")
	}
	oopsErr, ok := oops.AsOops(err)
	if !ok {
		t.Fatalf("LoadServer() error type = %T, want oops error", err)
	}
	if oopsErr.Code() != "config.invalid" || oopsErr.Context()["setting"] != "REDIS_POOL_SIZE" {
		t.Fatalf("LoadServer() code=%v context=%v, want config code and setting context", oopsErr.Code(), oopsErr.Context())
	}
}
