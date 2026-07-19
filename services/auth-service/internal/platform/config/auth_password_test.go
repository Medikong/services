package config

import (
	"strings"
	"testing"
)

func TestLoadAuthReadsPasswordMinimumLength(t *testing.T) {
	t.Setenv("AUTH_PASSWORD_MIN_LENGTH", "18")
	config, err := loadAuth("test")
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	if config.PasswordMinLength != 18 {
		t.Fatalf("PasswordMinLength = %d, want 18", config.PasswordMinLength)
	}
}

func TestAuthConfigRejectsPasswordMinimumBelowOpenAPIContract(t *testing.T) {
	t.Setenv("AUTH_PASSWORD_MIN_LENGTH", "11")
	config, err := loadAuth("test")
	if err != nil {
		t.Fatalf("load auth config: %v", err)
	}
	if err := config.Validate(false); err == nil || !strings.Contains(err.Error(), "PasswordMinLength") {
		t.Fatalf("Validate() error = %v, want PasswordMinLength validation error", err)
	}
}
