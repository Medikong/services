package config

import (
	"testing"
	"time"
)

func TestDependencyConfigsRequireExplicitEndpoints(t *testing.T) {
	if err := (BrokerConfig{Enabled: true, PublishTimeout: time.Second}).Validate(); err == nil {
		t.Fatal("BrokerConfig.Validate() error = nil")
	}
	if err := (DeliveryConfig{
		Enabled: true, RequestTimeout: time.Second, PollInterval: time.Second,
		Lease: 2 * time.Second, BatchSize: 1, MaxAttempts: 1,
		BaseBackoff: time.Second, MaxBackoff: time.Second,
	}).Validate(); err == nil {
		t.Fatal("DeliveryConfig.Validate() error = nil")
	}
}

func TestSessionStatusTimeoutContainsDatabaseFallback(t *testing.T) {
	config := SessionStatusConfig{
		Timeout: 100 * time.Millisecond, DBFallbackTimeout: 101 * time.Millisecond,
		CacheTTL: time.Minute, TombstoneTTL: 2 * time.Minute, MaxDBLookups: 1,
	}
	if err := config.Validate(); err == nil {
		t.Fatal("SessionStatusConfig.Validate() error = nil")
	}
}

func TestSessionStatusCacheTTLValidation(t *testing.T) {
	tests := []struct {
		name   string
		config SessionStatusConfig
	}{
		{
			name: "cache ttl required",
			config: SessionStatusConfig{
				Timeout: time.Second, DBFallbackTimeout: 100 * time.Millisecond,
				TombstoneTTL: time.Minute, MaxDBLookups: 1,
			},
		},
		{
			name: "tombstone must outlive active cache",
			config: SessionStatusConfig{
				Timeout: time.Second, DBFallbackTimeout: 100 * time.Millisecond,
				CacheTTL: 2 * time.Minute, TombstoneTTL: time.Minute, MaxDBLookups: 1,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.config.Validate(); err == nil {
				t.Fatal("SessionStatusConfig.Validate() error = nil")
			}
		})
	}
}

func TestSessionStatusCacheTTLDefaults(t *testing.T) {
	t.Setenv("AUTH_SESSION_STATUS_ENABLED", "false")
	t.Setenv("AUTH_SESSION_STATUS_TIMEOUT", "")
	t.Setenv("AUTH_SESSION_STATUS_DB_TIMEOUT", "")
	t.Setenv("AUTH_SESSION_STATUS_CACHE_TTL", "")
	t.Setenv("AUTH_SESSION_STATUS_TOMBSTONE_TTL", "")
	t.Setenv("AUTH_SESSION_STATUS_MAX_DB_LOOKUPS", "")
	config, err := loadSessionStatus()
	if err != nil {
		t.Fatalf("loadSessionStatus() error = %v", err)
	}
	if config.CacheTTL != 5*time.Minute || config.TombstoneTTL != 20*time.Minute || config.MaxDBLookups != 32 {
		t.Fatalf("session status defaults = (%s, %s, %d)", config.CacheTTL, config.TombstoneTTL, config.MaxDBLookups)
	}
}

func TestSessionStatusMaxDatabaseLookupsValidation(t *testing.T) {
	config := SessionStatusConfig{
		Timeout: time.Second, DBFallbackTimeout: 100 * time.Millisecond,
		CacheTTL: time.Minute, TombstoneTTL: 2 * time.Minute, MaxDBLookups: 33,
	}
	if err := config.Validate(); err == nil {
		t.Fatal("SessionStatusConfig.Validate() error = nil")
	}
}
