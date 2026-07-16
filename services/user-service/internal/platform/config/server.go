package config

import (
	"errors"
	"os"
	"strings"
	"time"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
)

type HTTPConfig struct {
	PublicAddr     string
	AdminAddr      string
	RequestTimeout time.Duration
	DrainDelay     time.Duration
	AllowedOrigins map[string]struct{}
}

type ServerConfig struct {
	Service            ServiceConfig
	HTTP               HTTPConfig
	Lifecycle          LifecycleConfig
	Postgres           platformdb.PostgresConfig
	Proof              ProofConfig
	Development        DevelopmentConfig
	Profile            ProfileConfig
	RequiredAgreements map[string]string
	IdempotencyTTL     time.Duration
}

func LoadServer() (ServerConfig, error) {
	service := loadService()
	lifecycle, err := loadLifecycle()
	if err != nil {
		return ServerConfig{}, err
	}
	profile, err := loadProfile()
	if err != nil {
		return ServerConfig{}, err
	}
	postgres, err := platformdb.LoadPostgresConfigFromEnv(strings.TrimSpace(os.Getenv("DATABASE_URL")))
	if err != nil {
		return ServerConfig{}, err
	}
	proof, authPrivate, mediaPrivate, err := loadProof(service.Environment)
	if err != nil {
		return ServerConfig{}, err
	}
	development, err := loadDevelopment(service.Environment, authPrivate, mediaPrivate)
	if err != nil {
		return ServerConfig{}, err
	}
	requestTimeout, err := durationEnv("HTTP_REQUEST_TIMEOUT", 10*time.Second)
	if err != nil {
		return ServerConfig{}, err
	}
	drainDelay, err := durationEnv("DRAIN_DELAY", 5*time.Second)
	if err != nil {
		return ServerConfig{}, err
	}
	originDefault := "http://localhost:3000"
	if !AllowsDevelopmentFeatures(service.Environment) {
		originDefault = ""
	}
	origins, err := parseOrigins(stringEnv("USER_ALLOWED_ORIGINS", originDefault), !AllowsDevelopmentFeatures(service.Environment))
	if err != nil {
		return ServerConfig{}, err
	}
	required, err := parseRequiredAgreements(stringEnv("USER_REQUIRED_AGREEMENTS", "TERMS_OF_SERVICE:2026-07-01"))
	if err != nil {
		return ServerConfig{}, err
	}
	idempotencyTTL, err := durationEnv("USER_IDEMPOTENCY_TTL", 30*24*time.Hour)
	if err != nil {
		return ServerConfig{}, err
	}
	cfg := ServerConfig{
		Service: service,
		HTTP: HTTPConfig{
			PublicAddr:     stringEnv("HTTP_ADDR", ":8080"),
			AdminAddr:      stringEnv("ADMIN_ADDR", ":9090"),
			RequestTimeout: requestTimeout,
			DrainDelay:     drainDelay,
			AllowedOrigins: origins,
		},
		Lifecycle:          lifecycle,
		Postgres:           postgres,
		Proof:              proof,
		Development:        development,
		Profile:            profile,
		RequiredAgreements: required,
		IdempotencyTTL:     idempotencyTTL,
	}
	if err := cfg.Validate(); err != nil {
		return ServerConfig{}, err
	}
	return cfg, nil
}

func (c ServerConfig) Validate() error {
	if strings.TrimSpace(c.Service.Name) == "" || strings.TrimSpace(c.Service.Version) == "" || strings.TrimSpace(c.Service.Environment) == "" {
		return errors.New("service name, version, and environment are required")
	}
	if strings.TrimSpace(c.Postgres.DatabaseURL) == "" {
		return errors.New("DATABASE_URL is required")
	}
	if c.HTTP.PublicAddr == "" || c.HTTP.AdminAddr == "" || c.HTTP.RequestTimeout <= 0 || c.HTTP.DrainDelay < 0 || len(c.HTTP.AllowedOrigins) == 0 {
		return errors.New("HTTP addresses, timeout, non-negative drain delay, and allowed origins are required")
	}
	if c.Lifecycle.ReadinessTimeout <= 0 || c.Lifecycle.ShutdownTimeout <= 0 || c.IdempotencyTTL <= 0 {
		return errors.New("lifecycle and idempotency durations must be positive")
	}
	if c.Proof.UserSigningPrivateKey == "" || c.Proof.UserSigningKeyID == "" || c.Proof.AuthProofPublicKey == "" || c.Proof.AuthProofKeyID == "" || c.Proof.MediaProofPublicKey == "" || c.Proof.MediaProofKeyID == "" || c.Proof.PrivateNameEncryptionKey == "" || c.Proof.ProofTTL <= 0 || c.Proof.ClockSkew < 0 {
		return errors.New("proof signing, verification, encryption keys, and durations are required")
	}
	if len(c.RequiredAgreements) == 0 {
		return errors.New("required agreements are required")
	}
	if c.Profile.PyroscopeEnabled && c.Profile.PyroscopeAddress == "" {
		return errors.New("PYROSCOPE_SERVER_ADDRESS is required when profiling is enabled")
	}
	if IsProduction(c.Service.Environment) && c.Development.Enabled {
		return errors.New("development routes are forbidden in production")
	}
	return nil
}
