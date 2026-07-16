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
	config := SessionStatusConfig{Timeout: 100 * time.Millisecond, DBFallbackTimeout: 101 * time.Millisecond}
	if err := config.Validate(); err == nil {
		t.Fatal("SessionStatusConfig.Validate() error = nil")
	}
}
