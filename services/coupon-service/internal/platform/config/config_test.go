package config

import (
	"testing"
	"time"
)

func TestServerConfigKeepsRedisOptional(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/coupon?sslmode=disable")
	t.Setenv("COUPON_REDIS_GATE_ENABLED", "false")
	t.Setenv("REDIS_URL", "")
	t.Setenv("COUPON_REDIS_GATE_FAILURE_MODE", "")

	config, err := LoadServer()
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	if config.Redis.Enabled {
		t.Fatal("Redis gate is enabled without an explicit setting")
	}
}

func TestEnabledRedisRequiresFailureMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/coupon?sslmode=disable")
	t.Setenv("COUPON_REDIS_GATE_ENABLED", "true")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("COUPON_REDIS_GATE_FAILURE_MODE", "")

	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil without a Redis failure mode")
	}
}

func TestSharedEnvironmentRequiresWorkerRetryPolicy(t *testing.T) {
	t.Setenv("SERVICE_ENVIRONMENT", "production")
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/coupon?sslmode=disable")
	t.Setenv("COUPON_REDIS_GATE_ENABLED", "false")
	for _, name := range []string{
		"COUPON_WORKER_BATCH_SIZE",
		"COUPON_WORKER_POLL_INTERVAL",
		"COUPON_WORKER_LEASE",
		"COUPON_WORKER_ATTEMPT_TIMEOUT",
		"COUPON_WORKER_MAX_ATTEMPTS",
		"COUPON_WORKER_BASE_BACKOFF",
		"COUPON_WORKER_MAX_BACKOFF",
	} {
		t.Setenv(name, "")
	}

	if _, err := LoadWorker(); err == nil {
		t.Fatal("LoadWorker() error = nil without an explicit production retry policy")
	}
}

func TestWorkerLeaseCoversAttemptTimeout(t *testing.T) {
	policy := WorkerPolicy{
		BatchSize: 1, PollInterval: time.Second, Lease: 10 * time.Second,
		AttemptTimeout: 10 * time.Second, MaxAttempts: 1,
		BaseBackoff: time.Second, MaxBackoff: time.Second,
	}
	if err := policy.Validate(); err == nil {
		t.Fatal("WorkerPolicy.Validate() error = nil for a lease shorter than twice the attempt timeout")
	}
}

func TestWorkerLeaseCoversSequentialBatch(t *testing.T) {
	policy := WorkerPolicy{
		BatchSize: 3, PollInterval: time.Second, Lease: 20 * time.Second,
		AttemptTimeout: 10 * time.Second, MaxAttempts: 1,
		BaseBackoff: time.Second, MaxBackoff: time.Second,
	}
	if err := policy.Validate(); err == nil {
		t.Fatal("WorkerPolicy.Validate() error = nil for a lease shorter than the sequential batch")
	}
	policy.Lease = 30 * time.Second
	if err := policy.Validate(); err != nil {
		t.Fatalf("WorkerPolicy.Validate() error = %v for a lease covering the sequential batch", err)
	}
}

func TestLocalWorkerDefaultsToSingleItemBatch(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/coupon?sslmode=disable")
	t.Setenv("COUPON_REDIS_GATE_ENABLED", "false")
	t.Setenv("COUPON_WORKER_BATCH_SIZE", "")

	config, err := LoadWorker()
	if err != nil {
		t.Fatalf("LoadWorker() error = %v", err)
	}
	if config.Policy.BatchSize != 1 {
		t.Fatalf("LoadWorker() batch size = %d, want 1", config.Policy.BatchSize)
	}
}
