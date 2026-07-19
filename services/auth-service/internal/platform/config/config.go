package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/samber/oops"
)

const ServiceName = "auth-service"

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

func stringEnv(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func secretStringEnv(valueName, fileName string) (string, error) {
	value := strings.TrimSpace(os.Getenv(valueName))
	path := strings.TrimSpace(os.Getenv(fileName))
	if value != "" && path != "" {
		return "", configErr.With("setting", valueName).New("secret value and secret file cannot both be set")
	}
	if path == "" {
		return value, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", configErr.With("setting", fileName).Wrap(err)
	}
	return strings.TrimSpace(string(contents)), nil
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
