package config

import (
	"os"
	"strings"
	"time"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type HTTPConfig struct {
	PublicAddr     string
	AdminAddr      string
	RequestTimeout time.Duration
	DrainDelay     time.Duration
}

func (c HTTPConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.PublicAddr, validation.Required),
		validation.Field(&c.AdminAddr, validation.Required),
		validation.Field(&c.RequestTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.DrainDelay, validation.Min(time.Duration(0))),
	)
}

type ServerConfig struct {
	Service     ServiceConfig
	HTTP        HTTPConfig
	Lifecycle   LifecycleConfig
	Postgres    platformdb.PostgresConfig
	Auth        AuthConfig
	Development DevelopmentConfig
	Profile     ProfileConfig
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
		return ServerConfig{}, configErr.With("config", "server", "setting", "postgres").Wrap(err)
	}
	httpConfig, err := loadHTTP()
	if err != nil {
		return ServerConfig{}, err
	}
	authConfig, err := loadAuth(service.Environment)
	if err != nil {
		return ServerConfig{}, err
	}
	development, err := loadDevelopment()
	if err != nil {
		return ServerConfig{}, err
	}
	cfg := ServerConfig{
		Service: service, HTTP: httpConfig, Lifecycle: lifecycle, Postgres: postgres,
		Auth: authConfig, Development: development, Profile: profile,
	}
	if err := cfg.Validate(); err != nil {
		return ServerConfig{}, err
	}
	return cfg, nil
}

func (c ServerConfig) Validate() error {
	developmentAllowed := AllowsDevelopmentFeatures(c.Service.Environment)
	err := validation.ValidateStruct(&c,
		validation.Field(&c.Service),
		validation.Field(&c.HTTP),
		validation.Field(&c.Lifecycle),
		validation.Field(&c.Postgres, validation.By(func(any) error {
			return validation.Validate(c.Postgres.DatabaseURL, validation.Required)
		})),
		validation.Field(&c.Profile),
	)
	if err != nil {
		return configErr.With("config", "server").Wrap(err)
	}
	if err := c.Auth.Validate(!developmentAllowed); err != nil {
		return configErr.With("config", "server", "section", "auth").Wrap(err)
	}
	if err := c.Development.Validate(developmentAllowed); err != nil {
		return configErr.With("config", "server", "section", "development").Wrap(err)
	}
	return nil
}

func loadHTTP() (HTTPConfig, error) {
	requestTimeout, err := durationEnv("HTTP_REQUEST_TIMEOUT", 10*time.Second)
	if err != nil {
		return HTTPConfig{}, err
	}
	drainDelay, err := durationEnv("DRAIN_DELAY", 5*time.Second)
	if err != nil {
		return HTTPConfig{}, err
	}
	return HTTPConfig{
		PublicAddr:     stringEnv("HTTP_ADDR", ":8080"),
		AdminAddr:      stringEnv("ADMIN_ADDR", ":9090"),
		RequestTimeout: requestTimeout,
		DrainDelay:     drainDelay,
	}, nil
}
