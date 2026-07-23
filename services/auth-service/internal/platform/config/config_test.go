package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"strings"
	"testing"
)

func TestLoadServerUsesRuntimeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/auth?sslmode=disable")
	t.Setenv("AUTH_JWT_PRIVATE_KEY_PEM", testPrivateKeyPEM(t))
	t.Setenv("AUTH_JWT_KEY_ID", "test-key")

	cfg, err := LoadServer()
	if err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
	if cfg.Service.Name != ServiceName {
		t.Fatalf("Service.Name = %q, want %q", cfg.Service.Name, ServiceName)
	}
	if cfg.HTTP.PublicAddr != ":8080" || cfg.HTTP.AdminAddr != ":9090" {
		t.Fatalf("HTTP addresses = %q, %q", cfg.HTTP.PublicAddr, cfg.HTTP.AdminAddr)
	}
	if cfg.Auth.SessionCookieName != "__Secure-dm_refresh" || cfg.Auth.AuthFlowCookieName != "__Host-dm_auth" || !cfg.Auth.CookieSecure {
		t.Fatal("browser cookie defaults are invalid")
	}
}

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
}

func TestLoadServerRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil, want database URL validation error")
	}
}

func TestLoadServerRejectsInsecurePrefixedCookies(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/auth?sslmode=disable")
	t.Setenv("AUTH_JWT_PRIVATE_KEY_PEM", testPrivateKeyPEM(t))
	t.Setenv("AUTH_JWT_KEY_ID", "test-key")
	t.Setenv("AUTH_COOKIE_SECURE", "false")

	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil, want browser cookie prefix validation error")
	}
}

func TestLoadServerAllowsInsecureUnprefixedCookiesInLocalDevelopment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/auth?sslmode=disable")
	t.Setenv("AUTH_JWT_PRIVATE_KEY_PEM", testPrivateKeyPEM(t))
	t.Setenv("AUTH_JWT_KEY_ID", "test-key")
	t.Setenv("AUTH_COOKIE_SECURE", "false")
	t.Setenv("AUTH_SESSION_COOKIE_NAME", "dm_refresh")
	t.Setenv("AUTH_FLOW_COOKIE_NAME", "dm_auth")

	if _, err := LoadServer(); err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
}

func TestLoadServerReadsJWTPrivateKeyFromFile(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/auth?sslmode=disable")
	t.Setenv("AUTH_JWT_PRIVATE_KEY_PEM", "")
	t.Setenv("AUTH_JWT_KEY_ID", "test-key")
	path := t.TempDir() + "/jwt.pem"
	if err := os.WriteFile(path, []byte(testPrivateKeyPEM(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTH_JWT_PRIVATE_KEY_FILE", path)
	if _, err := LoadServer(); err != nil {
		t.Fatalf("LoadServer() error = %v", err)
	}
}

func TestLoadServerRejectsAmbiguousJWTPrivateKeySources(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app:app@localhost:5432/auth?sslmode=disable")
	t.Setenv("AUTH_JWT_PRIVATE_KEY_PEM", testPrivateKeyPEM(t))
	t.Setenv("AUTH_JWT_PRIVATE_KEY_FILE", "/run/secrets/auth-jwt")
	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer() error = nil")
	}
}

func TestDevelopmentConfigRequiresExactly32ByteVirtualMessageKey(t *testing.T) {
	tests := []struct {
		name    string
		keySize int
		wantErr bool
	}{
		{name: "16 bytes", keySize: 16, wantErr: true},
		{name: "24 bytes", keySize: 24, wantErr: true},
		{name: "32 bytes", keySize: 32, wantErr: false},
		{name: "64 bytes", keySize: 64, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DevelopmentConfig{
				Enabled:                true,
				RouteEnabled:           true,
				VirtualAdaptersEnabled: true,
				AccessToken:            "development-access-token",
				VirtualMessageKey:      strings.Repeat("k", test.keySize),
			}

			err := config.Validate(true)
			if (err != nil) != test.wantErr {
				t.Fatalf("DevelopmentConfig.Validate(true) error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}
