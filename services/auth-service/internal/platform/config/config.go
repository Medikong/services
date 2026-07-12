package config

import (
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/samber/oops"
)

const ServiceName = "auth-service"

const (
	localCredentialHMACKey   = "local-development-credential-hmac-key-change-before-shared-use"
	localReplayEncryptionKey = "local-development-replay-key-001"
	localJWTSecret           = "local-development-jwt-secret-change-before-shared-use"
)

var configErr = oops.In("auth_config").Code("config.invalid")

type ServiceConfig struct {
	Name        string
	Version     string
	Environment string
}

func (c ServiceConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.Name, validation.Required),
		validation.Field(&c.Version, validation.Required),
		validation.Field(&c.Environment, validation.Required),
	)
}

type LifecycleConfig struct {
	ReadinessTimeout time.Duration
	ShutdownTimeout  time.Duration
}

func (c LifecycleConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.ReadinessTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.ShutdownTimeout, validation.Min(time.Nanosecond)),
	)
}

type ProfileConfig struct {
	PprofEnabled      bool
	PyroscopeEnabled  bool
	PyroscopeAddress  string
	PyroscopeUser     string
	PyroscopePassword string
	PyroscopeTenantID string
}

func (c ProfileConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.PyroscopeAddress, validation.When(c.PyroscopeEnabled, validation.Required)),
	)
}

// AuthConfig holds only process-local authentication policy and key material.
// It deliberately has no email, phone, or profile fields.
type AuthConfig struct {
	CredentialHMACKey    string
	ReplayEncryptionKey  string
	JWTSecret            string
	JWTIssuer            string
	IntentTTL            time.Duration
	RegistrationTTL      time.Duration
	ChallengeTTL         time.Duration
	SessionTTL           time.Duration
	RememberMeSessionTTL time.Duration
	RefreshTTL           time.Duration
	AccessTTL            time.Duration
	ProofTTL             time.Duration
	RecoveryTTL          time.Duration
	PasswordMinLength    int
	SessionCookieName    string
	AuthFlowCookieName   string
	CookieSecure         bool
	AllowedOrigins       []string
}

func (c AuthConfig) Validate(operational bool) error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.CredentialHMACKey, validation.Required),
		validation.Field(&c.ReplayEncryptionKey, validation.Required),
		validation.Field(&c.JWTIssuer, validation.Required),
		validation.Field(&c.JWTSecret, validation.Required),
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
	if len(strings.TrimSpace(c.JWTSecret)) < 32 {
		return validation.NewError("jwt_secret", "AUTH_JWT_SECRET must be at least 32 bytes")
	}
	if operational && !c.CookieSecure {
		return validation.NewError("cookie_secure", "AUTH_COOKIE_SECURE must be true outside local development")
	}
	if operational && len(c.AllowedOrigins) == 0 {
		return validation.NewError("allowed_origins", "AUTH_ALLOWED_ORIGINS is required outside local development")
	}
	for _, origin := range c.AllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return validation.NewError("allowed_origins", "AUTH_ALLOWED_ORIGINS must contain absolute origins only")
		}
		if operational && parsed.Scheme != "https" {
			return validation.NewError("allowed_origins", "AUTH_ALLOWED_ORIGINS must use https outside local development")
		}
	}
	return nil
}

// DevelopmentConfig controls only local/test virtual delivery and development
// inspection. Any environment other than local/test development rejects every
// enabled flag and every development secret.
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

func loadService() ServiceConfig {
	return ServiceConfig{
		Name:        stringEnv("SERVICE_NAME", ServiceName),
		Version:     stringEnv("SERVICE_VERSION", "dev"),
		Environment: stringEnv("SERVICE_ENVIRONMENT", "local"),
	}
}

func loadLifecycle() (LifecycleConfig, error) {
	readinessTimeout, err := durationEnv("READINESS_CHECK_TIMEOUT", 2*time.Second)
	if err != nil {
		return LifecycleConfig{}, err
	}
	shutdownTimeout, err := durationEnv("SHUTDOWN_TIMEOUT", 15*time.Second)
	if err != nil {
		return LifecycleConfig{}, err
	}
	return LifecycleConfig{ReadinessTimeout: readinessTimeout, ShutdownTimeout: shutdownTimeout}, nil
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

func loadAuth(environment string) (AuthConfig, error) {
	developmentAllowed := AllowsDevelopmentFeatures(environment)
	credentialDefault := localCredentialHMACKey
	replayDefault := localReplayEncryptionKey
	jwtDefault := localJWTSecret
	originDefault := "http://localhost:3000"
	if !developmentAllowed {
		credentialDefault = ""
		replayDefault = ""
		jwtDefault = ""
		originDefault = ""
	}
	registrationTTL, err := durationEnv("AUTH_REGISTRATION_TTL", 30*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	sessionTTL, err := durationEnv("AUTH_SESSION_TTL", 24*time.Hour)
	if err != nil {
		return AuthConfig{}, err
	}
	rememberMeSessionTTL, err := durationEnv("AUTH_REMEMBER_ME_SESSION_TTL", 30*24*time.Hour)
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
	recoveryTTL, err := durationEnv("AUTH_RECOVERY_TTL", 2*time.Minute)
	if err != nil {
		return AuthConfig{}, err
	}
	accessTTL, err := durationEnv("AUTH_ACCESS_TTL", 15*time.Minute)
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
	return AuthConfig{
		CredentialHMACKey:    stringEnv("AUTH_CREDENTIAL_HMAC_KEY", credentialDefault),
		ReplayEncryptionKey:  stringEnv("AUTH_REPLAY_ENCRYPTION_KEY", replayDefault),
		JWTSecret:            stringEnv("AUTH_JWT_SECRET", jwtDefault),
		JWTIssuer:            stringEnv("AUTH_JWT_ISSUER", ServiceName),
		IntentTTL:            intentTTL,
		RegistrationTTL:      registrationTTL,
		ChallengeTTL:         challengeTTL,
		SessionTTL:           sessionTTL,
		RememberMeSessionTTL: rememberMeSessionTTL,
		RefreshTTL:           refreshTTL,
		AccessTTL:            accessTTL,
		ProofTTL:             proofTTL,
		RecoveryTTL:          recoveryTTL,
		PasswordMinLength:    passwordMinLength,
		SessionCookieName:    stringEnv("AUTH_SESSION_COOKIE_NAME", "__Host-dm_session"),
		AuthFlowCookieName:   stringEnv("AUTH_FLOW_COOKIE_NAME", "__Host-dm_auth"),
		CookieSecure:         cookieSecure,
		AllowedOrigins:       stringListEnv("AUTH_ALLOWED_ORIGINS", originDefault),
	}, nil
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
	virtualAdaptersEnabled, err := boolEnv("AUTH_VIRTUAL_ADAPTERS_ENABLED", false)
	if err != nil {
		return DevelopmentConfig{}, err
	}
	return DevelopmentConfig{
		Enabled:                enabled,
		RouteEnabled:           routeEnabled,
		VirtualAdaptersEnabled: virtualAdaptersEnabled,
		AccessToken:            strings.TrimSpace(os.Getenv("AUTH_DEV_ACCESS_TOKEN")),
		VirtualMessageKey:      strings.TrimSpace(os.Getenv("AUTH_VIRTUAL_MESSAGE_KEY")),
	}, nil
}

func stringEnv(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func stringListEnv(name string, fallback string) []string {
	raw := os.Getenv(name)
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	values := strings.Split(raw, ",")
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
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

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, configErr.With("setting", name, "value", raw).Wrap(err)
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
		return false, configErr.With("setting", name, "value", raw).Wrap(err)
	}
	return value, nil
}
