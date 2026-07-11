package config

import "testing"

func TestLoadServerUsesLocalSecurityDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/auth")

	cfg, err := LoadServer()
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	if cfg.Auth.AccessTTL.String() != "15m0s" {
		t.Fatalf("Auth.AccessTTL = %s, want 15m", cfg.Auth.AccessTTL)
	}
	if !cfg.Auth.CookieSecure {
		t.Fatal("Auth.CookieSecure = false, want true")
	}
	if cfg.Development.Enabled || cfg.Development.RouteEnabled || cfg.Development.VirtualAdaptersEnabled {
		t.Fatalf("Development = %+v, want disabled defaults", cfg.Development)
	}
}

func TestLoadServerRejectsProductionDevelopmentRoute(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/auth")
	t.Setenv("SERVICE_ENVIRONMENT", "production")
	t.Setenv("AUTH_CREDENTIAL_HMAC_KEY", "01234567890123456789012345678901")
	t.Setenv("AUTH_REPLAY_ENCRYPTION_KEY", "01234567890123456789012345678901")
	t.Setenv("AUTH_JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("AUTH_ALLOWED_ORIGINS", "https://app.example.test")
	t.Setenv("AUTH_DEVELOPMENT_ENABLED", "true")
	t.Setenv("AUTH_DEV_ROUTE_ENABLED", "true")
	t.Setenv("AUTH_DEV_ACCESS_TOKEN", "development-token")

	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil, want production development gate failure")
	}
}

func TestLoadServerRejectsStagingDevelopmentRoute(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/auth")
	t.Setenv("SERVICE_ENVIRONMENT", "staging")
	t.Setenv("AUTH_CREDENTIAL_HMAC_KEY", "01234567890123456789012345678901")
	t.Setenv("AUTH_REPLAY_ENCRYPTION_KEY", "01234567890123456789012345678901")
	t.Setenv("AUTH_JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("AUTH_ALLOWED_ORIGINS", "https://staging.example.test")
	t.Setenv("AUTH_DEVELOPMENT_ENABLED", "true")
	t.Setenv("AUTH_DEV_ROUTE_ENABLED", "true")
	t.Setenv("AUTH_DEV_ACCESS_TOKEN", "development-token")

	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil, want staging development gate failure")
	}
}

func TestLoadServerRejectsDevelopmentVirtualAdapterWithoutKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/auth")
	t.Setenv("AUTH_DEVELOPMENT_ENABLED", "true")
	t.Setenv("AUTH_VIRTUAL_ADAPTERS_ENABLED", "true")
	t.Setenv("AUTH_DEV_ACCESS_TOKEN", "development-token")

	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil, want virtual adapter key validation failure")
	}
}

func TestLoadServerRejectsUnpairedDevelopmentRoute(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/auth")
	t.Setenv("AUTH_DEVELOPMENT_ENABLED", "true")
	t.Setenv("AUTH_DEV_ROUTE_ENABLED", "true")
	t.Setenv("AUTH_DEV_ACCESS_TOKEN", "development-token")

	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil, want development route pairing failure")
	}
}

func TestLoadMigrationIgnoresRuntimeSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/auth")
	t.Setenv("AUTH_JWT_SECRET", "")
	t.Setenv("AUTH_CREDENTIAL_HMAC_KEY", "")

	if _, err := LoadMigration(); err != nil {
		t.Fatalf("LoadMigration() error = %v", err)
	}
}
