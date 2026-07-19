package config

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// AuthConfig contains authentication policy and key references, never user profile data.
type AuthConfig struct {
	CredentialHMACKey         string
	ReplayEncryptionKey       string
	JWTPrivateKeyPEM          string
	JWTKeyID                  string
	JWTIssuer                 string
	JWTAudiences              []string
	JWTRetiringPublicKeys     map[string]string
	AuthProofPrivateKey       string
	AuthProofKeyID            string
	UserProofPublicKey        string
	UserProofKeyID            string
	UserProofIssuer           string
	ProofClockSkew            time.Duration
	IntentTTL                 time.Duration
	RegistrationTTL           time.Duration
	ChallengeTTL              time.Duration
	SessionTTL                time.Duration
	RememberMeSessionTTL      time.Duration
	RefreshTTL                time.Duration
	AccessTTL                 time.Duration
	ProofTTL                  time.Duration
	RecoveryTTL               time.Duration
	PasswordMinLength         int
	SessionCookieName         string
	AuthFlowCookieName        string
	CookieSecure              bool
	AllowedOrigins            []string
	SessionStatusRedisURL     string
	SessionStatusCacheTTL     time.Duration
	SessionStatusDBTimeout    time.Duration
	SessionStatusMaxDBLookups int
}

func (c AuthConfig) Validate(operational bool) error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.CredentialHMACKey, validation.Required),
		validation.Field(&c.ReplayEncryptionKey, validation.Required),
		validation.Field(&c.JWTPrivateKeyPEM, validation.Required),
		validation.Field(&c.JWTKeyID, validation.Required),
		validation.Field(&c.JWTIssuer, validation.Required),
		validation.Field(&c.JWTAudiences, validation.Required),
		validation.Field(&c.AuthProofPrivateKey, validation.Required),
		validation.Field(&c.AuthProofKeyID, validation.Required),
		validation.Field(&c.UserProofPublicKey, validation.Required),
		validation.Field(&c.UserProofKeyID, validation.Required),
		validation.Field(&c.UserProofIssuer, validation.Required),
		validation.Field(&c.ProofClockSkew, validation.Min(time.Duration(0))),
		validation.Field(&c.AccessTTL, validation.Min(time.Nanosecond), validation.Max(24*time.Hour)),
		validation.Field(&c.RegistrationTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.SessionTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.RememberMeSessionTTL, validation.Min(c.SessionTTL)),
		validation.Field(&c.RefreshTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.ChallengeTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.IntentTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.ProofTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.RecoveryTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.PasswordMinLength, validation.Min(8)),
		validation.Field(&c.SessionCookieName, validation.Required),
		validation.Field(&c.AuthFlowCookieName, validation.Required),
		validation.Field(&c.SessionStatusRedisURL, validation.Required),
		validation.Field(&c.SessionStatusCacheTTL, validation.Min(time.Nanosecond), validation.Max(5*time.Minute)),
		validation.Field(&c.SessionStatusDBTimeout, validation.Min(time.Nanosecond), validation.Max(100*time.Millisecond)),
		validation.Field(&c.SessionStatusMaxDBLookups, validation.Min(1), validation.Max(32)),
	)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(c.CredentialHMACKey)) < 32 {
		return validation.NewError("credential_hmac_key", "AUTH_CREDENTIAL_HMAC_KEY must be at least 32 bytes")
	}
	if len(c.ReplayEncryptionKey) != 32 {
		return validation.NewError("replay_encryption_key", "AUTH_REPLAY_ENCRYPTION_KEY must be exactly 32 bytes")
	}
	if _, exists := c.JWTRetiringPublicKeys[c.JWTKeyID]; exists {
		return validation.NewError("jwt_retiring_public_keys", "active AUTH_JWT_KEY_ID cannot also be retiring")
	}
	if operational && !c.CookieSecure {
		return validation.NewError("cookie_secure", "AUTH_COOKIE_SECURE must be true outside local development")
	}
	if operational && len(c.AllowedOrigins) == 0 {
		return validation.NewError("allowed_origins", "AUTH_ALLOWED_ORIGINS is required outside local development")
	}
	for _, origin := range c.AllowedOrigins {
		parsed, parseErr := url.Parse(origin)
		if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return validation.NewError("allowed_origins", "AUTH_ALLOWED_ORIGINS must contain absolute origins only")
		}
		if operational && parsed.Scheme != "https" {
			return validation.NewError("allowed_origins", "AUTH_ALLOWED_ORIGINS must use https outside local development")
		}
	}
	return nil
}

type DevelopmentConfig struct {
	Enabled                bool
	RouteEnabled           bool
	VirtualAdaptersEnabled bool
	AccessToken            string
	VirtualMessageKey      string
}

func (c DevelopmentConfig) Validate(allowed bool) error {
	if !allowed {
		if c.Enabled || c.RouteEnabled || c.VirtualAdaptersEnabled || c.AccessToken != "" || c.VirtualMessageKey != "" {
			return validation.NewError("development", "development routes, virtual adapters, and their secrets are forbidden in production")
		}
		return nil
	}
	if (c.RouteEnabled || c.VirtualAdaptersEnabled) && !c.Enabled {
		return validation.NewError("development", "AUTH_DEVELOPMENT_ENABLED is required when a development feature is enabled")
	}
	if c.RouteEnabled != c.VirtualAdaptersEnabled {
		return validation.NewError("development", "AUTH_DEV_ROUTE_ENABLED and AUTH_VIRTUAL_ADAPTERS_ENABLED must be enabled together")
	}
	if c.Enabled && strings.TrimSpace(c.AccessToken) == "" {
		return validation.NewError("development", "AUTH_DEV_ACCESS_TOKEN is required when development mode is enabled")
	}
	if c.VirtualAdaptersEnabled && len(strings.TrimSpace(c.VirtualMessageKey)) < 32 {
		return validation.NewError("development", "AUTH_VIRTUAL_MESSAGE_KEY must be at least 32 bytes when virtual adapters are enabled")
	}
	if !c.Enabled && (strings.TrimSpace(c.AccessToken) != "" || strings.TrimSpace(c.VirtualMessageKey) != "") {
		return validation.NewError("development", "development secrets require AUTH_DEVELOPMENT_ENABLED=true")
	}
	return nil
}

type AuditConfig struct {
	SinkDatabaseURL string
	BatchSize       int
	PollInterval    time.Duration
	Lease           time.Duration
	PublishTimeout  time.Duration
	MaxAttempts     int
	BaseBackoff     time.Duration
	MaxBackoff      time.Duration
	Retention       time.Duration
	CleanupLimit    int
}

func (c AuditConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.BatchSize, validation.Min(1)),
		validation.Field(&c.PollInterval, validation.Min(time.Nanosecond)),
		validation.Field(&c.Lease, validation.Min(2*c.PublishTimeout).Error("must be at least twice PublishTimeout")),
		validation.Field(&c.PublishTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.MaxAttempts, validation.Min(1)),
		validation.Field(&c.BaseBackoff, validation.Min(time.Nanosecond)),
		validation.Field(&c.MaxBackoff, validation.Min(c.BaseBackoff)),
		validation.Field(&c.Retention, validation.Min(time.Nanosecond)),
		validation.Field(&c.CleanupLimit, validation.Min(1)),
	)
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

func loadAuth(environment string) (AuthConfig, error) {
	developmentAllowed := AllowsDevelopmentFeatures(environment)
	credentialDefault, replayDefault, originDefault, redisDefault := "", "", "", ""
	if developmentAllowed {
		credentialDefault = "local-development-credential-hmac-key-change-before-shared-use"
		replayDefault = "local-development-replay-key-001"
		originDefault = "http://localhost:3000"
		redisDefault = "redis://127.0.0.1:6379/0"
	}
	registrationTTL, err := durationEnv("AUTH_REGISTRATION_TTL", 30*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	sessionTTL, err := durationEnv("AUTH_SESSION_TTL", 24*time.Hour)
	if err != nil {
		return AuthConfig{}, err
	}
	rememberTTL, err := durationEnv("AUTH_REMEMBER_ME_SESSION_TTL", 30*24*time.Hour)
	if err != nil {
		return AuthConfig{}, err
	}
	refreshTTL, err := durationEnv("AUTH_REFRESH_TTL", 14*24*time.Hour)
	if err != nil {
		return AuthConfig{}, err
	}
	challengeTTL, err := durationEnv("AUTH_CHALLENGE_TTL", 10*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	intentTTL, err := durationEnv("AUTH_INTENT_TTL", 15*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	proofTTL, err := durationEnv("AUTH_PROOF_TTL", 5*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	proofClockSkew, err := durationEnv("AUTH_PROOF_CLOCK_SKEW", 30*time.Second)
	if err != nil {
		return AuthConfig{}, err
	}
	recoveryTTL, err := durationEnv("AUTH_RECOVERY_TTL", 2*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	accessTTL, err := durationEnv("AUTH_ACCESS_TTL", 15*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	statusCacheTTL, err := durationEnv("AUTH_SESSION_STATUS_CACHE_TTL", 5*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	statusDBTimeout, err := durationEnv("AUTH_SESSION_STATUS_DB_TIMEOUT", 100*time.Millisecond)
	if err != nil {
		return AuthConfig{}, err
	}
	statusMaxDBLookups, err := intEnv("AUTH_SESSION_STATUS_MAX_DB_LOOKUPS", 32)
	if err != nil {
		return AuthConfig{}, err
	}
	passwordMinLength, err := intEnv("AUTH_PASSWORD_MIN_LENGTH", 12)
	if err != nil {
		return AuthConfig{}, err
	}
	cookieSecure, err := boolEnv("AUTH_COOKIE_SECURE", true)
	if err != nil {
		return AuthConfig{}, err
	}
	authProofPrivateKey, authProofKeyID := "", ""
	userProofPublicKey, userProofKeyID := "", ""
	if developmentAllowed {
		authSeed := sha256.Sum256([]byte("dropmong-user-auth-proof"))
		authProofPrivateKey = base64.RawStdEncoding.EncodeToString(ed25519.NewKeyFromSeed(authSeed[:]))
		authProofKeyID = "auth-local-1"
		seed := sha256.Sum256([]byte("dropmong-user-outgoing-proof"))
		privateKey := ed25519.NewKeyFromSeed(seed[:])
		userProofPublicKey = base64.RawStdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))
		userProofKeyID = "user-local-1"
	}
	retiringKeys, err := jsonStringMapEnv("AUTH_JWT_RETIRING_PUBLIC_KEYS")
	if err != nil {
		return AuthConfig{}, err
	}
	jwtPrivateKey, err := secretStringEnv("AUTH_JWT_PRIVATE_KEY_PEM", "AUTH_JWT_PRIVATE_KEY_FILE")
	if err != nil {
		return AuthConfig{}, err
	}
	return AuthConfig{
		CredentialHMACKey:     stringEnv("AUTH_CREDENTIAL_HMAC_KEY", credentialDefault),
		ReplayEncryptionKey:   stringEnv("AUTH_REPLAY_ENCRYPTION_KEY", replayDefault),
		JWTPrivateKeyPEM:      jwtPrivateKey,
		JWTKeyID:              strings.TrimSpace(os.Getenv("AUTH_JWT_KEY_ID")),
		JWTIssuer:             stringEnv("AUTH_JWT_ISSUER", ServiceName),
		JWTAudiences:          stringListEnv("AUTH_JWT_AUDIENCES", "dropmong-api"),
		JWTRetiringPublicKeys: retiringKeys,
		AuthProofPrivateKey:   stringEnv("AUTH_PROOF_PRIVATE_KEY", authProofPrivateKey),
		AuthProofKeyID:        stringEnv("AUTH_PROOF_KEY_ID", authProofKeyID),
		UserProofPublicKey:    stringEnv("AUTH_USER_PROOF_PUBLIC_KEY", userProofPublicKey),
		UserProofKeyID:        stringEnv("AUTH_USER_PROOF_KEY_ID", userProofKeyID),
		UserProofIssuer:       stringEnv("AUTH_USER_PROOF_ISSUER", "user-service"),
		ProofClockSkew:        proofClockSkew,
		IntentTTL:             intentTTL, RegistrationTTL: registrationTTL, ChallengeTTL: challengeTTL,
		SessionTTL: sessionTTL, RememberMeSessionTTL: rememberTTL, RefreshTTL: refreshTTL,
		AccessTTL: accessTTL, ProofTTL: proofTTL, RecoveryTTL: recoveryTTL,
		PasswordMinLength:         passwordMinLength,
		SessionCookieName:         stringEnv("AUTH_SESSION_COOKIE_NAME", "__Host-dm_refresh"),
		AuthFlowCookieName:        stringEnv("AUTH_FLOW_COOKIE_NAME", "__Host-dm_auth"),
		CookieSecure:              cookieSecure,
		AllowedOrigins:            stringListEnv("AUTH_ALLOWED_ORIGINS", originDefault),
		SessionStatusRedisURL:     stringEnv("AUTH_SESSION_STATUS_REDIS_URL", redisDefault),
		SessionStatusCacheTTL:     statusCacheTTL,
		SessionStatusDBTimeout:    statusDBTimeout,
		SessionStatusMaxDBLookups: statusMaxDBLookups,
	}, nil
}

func jsonStringMapEnv(name string) (map[string]string, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, nil
	}
	result := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, validation.NewError(name, "must be a JSON object of string values")
	}
	for key, value := range result {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return nil, validation.NewError(name, "key IDs and public keys must be non-empty")
		}
	}
	return result, nil
}

func loadDevelopment() (DevelopmentConfig, error) {
	enabled, err := boolEnv("AUTH_DEVELOPMENT_ENABLED", false)
	if err != nil {
		return DevelopmentConfig{}, err
	}
	routeEnabled, err := boolEnv("AUTH_DEV_ROUTE_ENABLED", false)
	if err != nil {
		return DevelopmentConfig{}, err
	}
	virtualEnabled, err := boolEnv("AUTH_VIRTUAL_ADAPTERS_ENABLED", false)
	if err != nil {
		return DevelopmentConfig{}, err
	}
	return DevelopmentConfig{
		Enabled: enabled, RouteEnabled: routeEnabled, VirtualAdaptersEnabled: virtualEnabled,
		AccessToken:       strings.TrimSpace(os.Getenv("AUTH_DEV_ACCESS_TOKEN")),
		VirtualMessageKey: strings.TrimSpace(os.Getenv("AUTH_VIRTUAL_MESSAGE_KEY")),
	}, nil
}

func stringListEnv(name, fallback string) []string {
	raw := os.Getenv(name)
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	seen := map[string]struct{}{}
	var result []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func intEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, configErr.With("setting", name, "value", raw).Wrap(err)
	}
	return value, nil
}
