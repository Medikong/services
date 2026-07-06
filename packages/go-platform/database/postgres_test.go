package database

import (
	"testing"
	"time"
)

func TestLoadPostgresConfigFromEnv(t *testing.T) {
	t.Setenv("POSTGRES_POOL_MAX_CONNS", "25")
	t.Setenv("POSTGRES_POOL_MIN_CONNS", "2")
	t.Setenv("POSTGRES_POOL_MIN_IDLE_CONNS", "1")
	t.Setenv("POSTGRES_POOL_MAX_CONN_LIFETIME", "45m")
	t.Setenv("POSTGRES_POOL_MAX_CONN_IDLE_TIME", "5m")
	t.Setenv("POSTGRES_POOL_HEALTH_CHECK_PERIOD", "30s")

	cfg, err := LoadPostgresConfigFromEnv("postgres://user:password@db:5432/app?sslmode=disable")
	if err != nil {
		t.Fatalf("LoadPostgresConfigFromEnv() error = %v", err)
	}
	if cfg.MaxConns != 25 || cfg.MinConns != 2 || cfg.MinIdleConns != 1 {
		t.Fatalf("pool sizes = max:%d min:%d minIdle:%d", cfg.MaxConns, cfg.MinConns, cfg.MinIdleConns)
	}
	if cfg.MaxConnLifetime != 45*time.Minute || cfg.MaxConnIdleTime != 5*time.Minute || cfg.HealthCheckPeriod != 30*time.Second {
		t.Fatalf("pool durations = lifetime:%s idle:%s health:%s", cfg.MaxConnLifetime, cfg.MaxConnIdleTime, cfg.HealthCheckPeriod)
	}
}

func TestLoadPostgresConfigFromEnvRejectsBadValue(t *testing.T) {
	t.Setenv("POSTGRES_POOL_MAX_CONNS", "many")

	if _, err := LoadPostgresConfigFromEnv("postgres://user:password@db:5432/app?sslmode=disable"); err == nil {
		t.Fatal("LoadPostgresConfigFromEnv() error = nil, want parse error")
	}
}

func TestLoadPostgresConfigFromEnvRejectsInvalidRange(t *testing.T) {
	t.Setenv("POSTGRES_POOL_MAX_CONNS", "1")
	t.Setenv("POSTGRES_POOL_MIN_CONNS", "2")

	if _, err := LoadPostgresConfigFromEnv("postgres://user:password@db:5432/app?sslmode=disable"); err == nil {
		t.Fatal("LoadPostgresConfigFromEnv() error = nil, want range error")
	}
}
