package config

import (
	"os"
	"strings"
	"time"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	platformmiddleware "github.com/Medikong/services/packages/go-platform/httpmiddleware"
	platformredis "github.com/Medikong/services/packages/go-platform/redisutil"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type HTTPConfig struct {
	PublicAddr     string
	AdminAddr      string
	GRPCAddr       string
	RequestTimeout time.Duration
	DrainDelay     time.Duration
}

func (c HTTPConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.PublicAddr, validation.Required),
		validation.Field(&c.AdminAddr, validation.Required),
		validation.Field(&c.RequestTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.DrainDelay, validation.Min(time.Nanosecond)),
	)
}

type ServerConfig struct {
	Service   ServiceConfig
	HTTP      HTTPConfig
	Lifecycle LifecycleConfig
	Postgres  platformdb.PostgresConfig
	Redis     platformredis.Config
	Lock      platformmiddleware.RedisLockPolicy
	Profile   ProfileConfig
}

func LoadServer() (ServerConfig, error) {
	lifecycle, err := loadLifecycle()
	if err != nil {
		return ServerConfig{}, err
	}
	profile, err := loadProfile()
	if err != nil {
		return ServerConfig{}, err
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	postgres, err := platformdb.LoadPostgresConfigFromEnv(databaseURL)
	if err != nil {
		return ServerConfig{}, configErr.With("config", "server", "setting", "postgres").Wrap(err)
	}
	httpConfig, err := loadHTTP()
	if err != nil {
		return ServerConfig{}, err
	}
	redisConfig, err := platformredis.LoadConfigFromEnv(strings.TrimSpace(os.Getenv("REDIS_URL")))
	if err != nil {
		return ServerConfig{}, err
	}
	lockConfig, err := platformmiddleware.LoadRedisLockPolicyFromEnv()
	if err != nil {
		return ServerConfig{}, err
	}
	cfg := ServerConfig{
		Service:   loadService(),
		HTTP:      httpConfig,
		Lifecycle: lifecycle,
		Postgres:  postgres,
		Redis:     redisConfig,
		Lock:      lockConfig,
		Profile:   profile,
	}
	if err := cfg.Validate(); err != nil {
		return ServerConfig{}, err
	}
	return cfg, nil
}

func (c ServerConfig) Validate() error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.Service),
		validation.Field(&c.HTTP),
		validation.Field(&c.Lifecycle),
		validation.Field(&c.Postgres, validation.By(func(any) error {
			return validation.Validate(c.Postgres.DatabaseURL, validation.Required)
		})),
		validation.Field(&c.Redis),
		validation.Field(&c.Lock),
		validation.Field(&c.Profile),
	)
	if err != nil {
		return configErr.With("config", "server").Wrap(err)
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
		GRPCAddr:       strings.TrimSpace(os.Getenv("GRPC_ADDR")),
		RequestTimeout: requestTimeout,
		DrainDelay:     drainDelay,
	}, nil
}
