package config

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const ServiceName = "user-service"

var agreementCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

type ServiceConfig struct {
	Name        string
	Version     string
	Environment string
}

type LifecycleConfig struct {
	ReadinessTimeout time.Duration
	ShutdownTimeout  time.Duration
}

type ProfileConfig struct {
	PprofEnabled      bool
	PyroscopeEnabled  bool
	PyroscopeAddress  string
	PyroscopeUser     string
	PyroscopePassword string
	PyroscopeTenantID string
}

type ProofConfig struct {
	UserSigningPrivateKey    string
	UserSigningKeyID         string
	AuthProofPublicKey       string
	AuthProofKeyID           string
	MediaProofPublicKey      string
	MediaProofKeyID          string
	PrivateNameEncryptionKey string
	ProofTTL                 time.Duration
	ClockSkew                time.Duration
}

type DevelopmentConfig struct {
	Enabled                bool
	AccessToken            string
	AuthSigningPrivateKey  string
	MediaSigningPrivateKey string
}

func IsProduction(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "production", "prod":
		return true
	default:
		return false
	}
}

func AllowsDevelopmentFeatures(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "local", "development", "dev", "test":
		return true
	default:
		return false
	}
}

func loadService() ServiceConfig {
	return ServiceConfig{
		Name:        stringEnv("SERVICE_NAME", ServiceName),
		Version:     stringEnv("SERVICE_VERSION", "dev"),
		Environment: stringEnv("SERVICE_ENVIRONMENT", "local"),
	}
}

func loadLifecycle() (LifecycleConfig, error) {
	readiness, err := durationEnv("READINESS_CHECK_TIMEOUT", 2*time.Second)
	if err != nil {
		return LifecycleConfig{}, err
	}
	shutdown, err := durationEnv("SHUTDOWN_TIMEOUT", 20*time.Second)
	if err != nil {
		return LifecycleConfig{}, err
	}
	return LifecycleConfig{ReadinessTimeout: readiness, ShutdownTimeout: shutdown}, nil
}

func loadProfile() (ProfileConfig, error) {
	pprofEnabled, err := boolEnv("PPROF_ENABLED", false)
	if err != nil {
		return ProfileConfig{}, err
	}
	pyroscopeEnabled, err := boolEnv("PYROSCOPE_ENABLED", false)
	if err != nil {
		return ProfileConfig{}, err
	}
	return ProfileConfig{
		PprofEnabled:      pprofEnabled,
		PyroscopeEnabled:  pyroscopeEnabled,
		PyroscopeAddress:  strings.TrimSpace(os.Getenv("PYROSCOPE_SERVER_ADDRESS")),
		PyroscopeUser:     os.Getenv("PYROSCOPE_BASIC_AUTH_USERNAME"),
		PyroscopePassword: os.Getenv("PYROSCOPE_BASIC_AUTH_PASSWORD"),
		PyroscopeTenantID: os.Getenv("PYROSCOPE_TENANT_ID"),
	}, nil
}

func loadProof(environment string) (ProofConfig, string, string, error) {
	authPublic, authPrivate := developmentKeyPair("dropmong-user-auth-proof")
	mediaPublic, mediaPrivate := developmentKeyPair("dropmong-user-media-proof")
	_, userPrivate := developmentKeyPair("dropmong-user-outgoing-proof")
	userKeyID, authKeyID, mediaKeyID := "user-local-1", "auth-local-1", "media-local-1"
	encryptionSeed := sha256.Sum256([]byte("dropmong-user-private-name-development-key"))
	privateNameKey := base64.RawStdEncoding.EncodeToString(encryptionSeed[:])
	if !AllowsDevelopmentFeatures(environment) {
		authPublic, authPrivate, mediaPublic, mediaPrivate, userPrivate, privateNameKey = "", "", "", "", "", ""
		userKeyID, authKeyID, mediaKeyID = "", "", ""
	}
	proofTTL, err := durationEnv("USER_PROOF_TTL", 5*time.Minute)
	if err != nil {
		return ProofConfig{}, "", "", err
	}
	clockSkew, err := durationEnv("USER_PROOF_CLOCK_SKEW", 30*time.Second)
	if err != nil {
		return ProofConfig{}, "", "", err
	}
	return ProofConfig{
		UserSigningPrivateKey:    stringEnv("USER_SIGNING_PRIVATE_KEY", userPrivate),
		UserSigningKeyID:         stringEnv("USER_SIGNING_KEY_ID", userKeyID),
		AuthProofPublicKey:       stringEnv("AUTH_PROOF_PUBLIC_KEY", authPublic),
		AuthProofKeyID:           stringEnv("AUTH_PROOF_KEY_ID", authKeyID),
		MediaProofPublicKey:      stringEnv("MEDIA_PROOF_PUBLIC_KEY", mediaPublic),
		MediaProofKeyID:          stringEnv("MEDIA_PROOF_KEY_ID", mediaKeyID),
		PrivateNameEncryptionKey: stringEnv("USER_PRIVATE_NAME_ENCRYPTION_KEY", privateNameKey),
		ProofTTL:                 proofTTL,
		ClockSkew:                clockSkew,
	}, authPrivate, mediaPrivate, nil
}

func loadDevelopment(environment, authPrivate, mediaPrivate string) (DevelopmentConfig, error) {
	enabled, err := boolEnv("USER_DEVELOPMENT_ENABLED", false)
	if err != nil {
		return DevelopmentConfig{}, err
	}
	cfg := DevelopmentConfig{
		Enabled:                enabled,
		AccessToken:            strings.TrimSpace(os.Getenv("USER_DEV_ACCESS_TOKEN")),
		AuthSigningPrivateKey:  stringEnv("USER_DEV_AUTH_SIGNING_PRIVATE_KEY", authPrivate),
		MediaSigningPrivateKey: stringEnv("USER_DEV_MEDIA_SIGNING_PRIVATE_KEY", mediaPrivate),
	}
	if !AllowsDevelopmentFeatures(environment) && (cfg.Enabled || cfg.AccessToken != "" || os.Getenv("USER_DEV_AUTH_SIGNING_PRIVATE_KEY") != "" || os.Getenv("USER_DEV_MEDIA_SIGNING_PRIVATE_KEY") != "") {
		return DevelopmentConfig{}, errors.New("development routes and secrets are forbidden outside local, development, and test environments")
	}
	if cfg.Enabled && (len(cfg.AccessToken) < 16 || cfg.AuthSigningPrivateKey == "" || cfg.MediaSigningPrivateKey == "") {
		return DevelopmentConfig{}, errors.New("development proof routes require an access token and signing keys")
	}
	return cfg, nil
}

func parseRequiredAgreements(raw string) (map[string]string, error) {
	result := map[string]string{}
	for _, item := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("required agreement %q must use CODE:VERSION", item)
		}
		code, version := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if !agreementCodePattern.MatchString(code) || version == "" || len(version) > 64 {
			return nil, fmt.Errorf("required agreement %q is invalid", item)
		}
		if _, exists := result[code]; exists {
			return nil, fmt.Errorf("required agreement %q is duplicated", code)
		}
		result[code] = version
	}
	if len(result) == 0 {
		return nil, errors.New("at least one required agreement is required")
	}
	return result, nil
}

func parseOrigins(raw string, operational bool) (map[string]struct{}, error) {
	result := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(item)
		if origin == "" {
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("allowed origin %q must be an absolute origin", origin)
		}
		if operational && parsed.Scheme != "https" {
			return nil, fmt.Errorf("allowed origin %q must use https outside development", origin)
		}
		result[origin] = struct{}{}
	}
	if len(result) == 0 {
		return nil, errors.New("at least one allowed origin is required")
	}
	return result, nil
}

func developmentKeyPair(label string) (string, string) {
	seed := sha256.Sum256([]byte(label))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return base64.RawStdEncoding.EncodeToString(publicKey), base64.RawStdEncoding.EncodeToString(privateKey)
}

func stringEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return value, nil
}

func boolEnv(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}
	return value, nil
}
