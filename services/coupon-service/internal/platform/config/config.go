package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/samber/oops"

	platformredis "github.com/Medikong/services/packages/go-platform/redisutil"
)

const ServiceName = "coupon-service"

var configErr = oops.In("coupon_config").Code("config.invalid")

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

type RedisFailureMode string

const (
	RedisFailureDBFallback RedisFailureMode = "db_fallback"
	RedisFailureClosed     RedisFailureMode = "fail_closed"
)

// RedisConfig keeps Redis optional. PostgreSQL remains authoritative even when
// the admission gate or cache is enabled.
type RedisConfig struct {
	Enabled     bool
	FailureMode RedisFailureMode
	Client      platformredis.Config
}

func (c RedisConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.FailureMode != RedisFailureDBFallback && c.FailureMode != RedisFailureClosed {
		return configErr.
			With("setting", "COUPON_REDIS_GATE_FAILURE_MODE", "value", c.FailureMode).
			New("enabled Redis gate requires db_fallback or fail_closed failure mode")
	}
	if err := c.Client.Validate(); err != nil {
		return configErr.With("section", "redis").Wrap(err)
	}
	return nil
}

func loadService() ServiceConfig {
	return ServiceConfig{
		Name:        stringEnv("SERVICE_NAME", ServiceName),
		Version:     stringEnv("SERVICE_VERSION", "dev"),
		Environment: stringEnv("SERVICE_ENVIRONMENT", "local"),
	}
}

func loadLifecycle() (LifecycleConfig, error) {
	readinessTimeout, err := durationEnv("READINESS_CHECK_TIMEOUT", 2*time.Second, false)
	if err != nil {
		return LifecycleConfig{}, err
	}
	shutdownTimeout, err := durationEnv("SHUTDOWN_TIMEOUT", 15*time.Second, false)
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

func loadRedis() (RedisConfig, error) {
	enabled, err := boolEnv("COUPON_REDIS_GATE_ENABLED", false)
	if err != nil {
		return RedisConfig{}, err
	}
	mode := RedisFailureMode(strings.ToLower(strings.TrimSpace(os.Getenv("COUPON_REDIS_GATE_FAILURE_MODE"))))
	config := RedisConfig{Enabled: enabled, FailureMode: mode}
	if !enabled {
		return config, nil
	}
	client, err := platformredis.LoadConfigFromEnv(strings.TrimSpace(os.Getenv("REDIS_URL")))
	if err != nil {
		return RedisConfig{}, configErr.With("section", "redis").Wrap(err)
	}
	config.Client = client
	if err := config.Validate(); err != nil {
		return RedisConfig{}, err
	}
	return config, nil
}

func allowsLocalDefaults(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "local", "development", "dev", "test":
		return true
	default:
		return false
	}
}

func stringEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func intEnv(name string, fallback int, required bool) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		if required {
			return 0, configErr.With("setting", name).New("required worker policy setting is missing")
		}
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, configErr.With("setting", name, "value", raw).Wrap(err)
	}
	return value, nil
}

func durationEnv(name string, fallback time.Duration, required bool) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		if required {
			return 0, configErr.With("setting", name).New("required worker policy setting is missing")
		}
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
