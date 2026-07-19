package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
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
