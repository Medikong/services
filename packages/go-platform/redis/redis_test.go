package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("REDIS_POOL_SIZE", "20")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "2")
	t.Setenv("REDIS_DIAL_TIMEOUT", "5s")

	cfg, err := LoadConfigFromEnv("redis://localhost:6379/0")
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	if cfg.PoolSize != 20 || cfg.MinIdleConns != 2 || cfg.DialTimeout != 5*time.Second {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestLoadConfigFromEnvRejectsInvalidPoolRange(t *testing.T) {
	t.Setenv("REDIS_POOL_SIZE", "1")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "2")

	if _, err := LoadConfigFromEnv("redis://localhost:6379/0"); err == nil {
		t.Fatal("LoadConfigFromEnv() error = nil, want invalid pool range")
	}
}

func TestOpenReturnsRawClient(t *testing.T) {
	server := miniredis.RunT(t)
	cfg, err := LoadConfigFromEnv("redis://" + server.Addr() + "/0")
	if err != nil {
		t.Fatalf("LoadConfigFromEnv() error = %v", err)
	}
	client, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Set(context.Background(), "key", "value", 0).Err(); err != nil {
		t.Fatalf("raw redis client Set() error = %v", err)
	}
}
