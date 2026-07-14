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
	AllowedOrigins []string
}

func (c HTTPConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.PublicAddr, validation.Required),
		validation.Field(&c.AdminAddr, validation.Required),
		validation.Field(&c.RequestTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.DrainDelay, validation.Min(time.Nanosecond)),
	)
}

type DomainPolicyConfig struct {
	// ReservationTTL is runtime policy input until HOTSPOT.A.19-03 is resolved.
	ReservationTTL     time.Duration
	CodeReservationTTL time.Duration
	CodeHashKey        string
	CommandLease       time.Duration
	IdempotencyTTL     time.Duration
}

func (c DomainPolicyConfig) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.ReservationTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.CodeReservationTTL, validation.Min(time.Nanosecond)),
		validation.Field(&c.CodeHashKey, validation.Length(32, 0)),
		validation.Field(&c.CommandLease, validation.Min(time.Nanosecond)),
		validation.Field(&c.IdempotencyTTL, validation.Min(c.CommandLease+time.Nanosecond)),
	)
}

type ServerConfig struct {
	Service   ServiceConfig
	HTTP      HTTPConfig
	Lifecycle LifecycleConfig
	Postgres  platformdb.PostgresConfig
	Redis     RedisConfig
	Policy    DomainPolicyConfig
	Profile   ProfileConfig
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
	httpConfig, err := loadHTTP(!allowsLocalDefaults(service.Environment))
	if err != nil {
		return ServerConfig{}, err
	}
	policy, err := loadDomainPolicy(!allowsLocalDefaults(service.Environment))
	if err != nil {
		return ServerConfig{}, err
	}
	redisConfig, err := loadRedis()
	if err != nil {
		return ServerConfig{}, err
	}
	config := ServerConfig{
		Service:   service,
		HTTP:      httpConfig,
		Lifecycle: lifecycle,
		Postgres:  postgres,
		Redis:     redisConfig,
		Policy:    policy,
		Profile:   profile,
	}
	if err := config.Validate(); err != nil {
		return ServerConfig{}, err
	}
	return config, nil
}

func (c ServerConfig) Validate() error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.Service),
		validation.Field(&c.HTTP),
		validation.Field(&c.Lifecycle),
		validation.Field(&c.Postgres, validation.By(func(any) error {
			return validation.Validate(c.Postgres.DatabaseURL, validation.Required)
		})),
		validation.Field(&c.Policy),
		validation.Field(&c.Profile),
	)
	if err != nil {
		return configErr.With("config", "server").Wrap(err)
	}
	if err := c.Redis.Validate(); err != nil {
		return configErr.With("config", "server", "section", "redis").Wrap(err)
	}
	return nil
}

func loadHTTP(required bool) (HTTPConfig, error) {
	requestTimeout, err := durationEnv("HTTP_REQUEST_TIMEOUT", 10*time.Second, false)
	if err != nil {
		return HTTPConfig{}, err
	}
	drainDelay, err := durationEnv("DRAIN_DELAY", 5*time.Second, false)
	if err != nil {
		return HTTPConfig{}, err
	}
	originsRaw := strings.TrimSpace(os.Getenv("COUPON_ALLOWED_ORIGINS"))
	if originsRaw == "" && required {
		return HTTPConfig{}, configErr.With("setting", "COUPON_ALLOWED_ORIGINS").New("allowed web origins are required outside local development")
	}
	if originsRaw == "" {
		originsRaw = "http://localhost:3000"
	}
	allowedOrigins := make([]string, 0)
	for _, origin := range strings.Split(originsRaw, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			allowedOrigins = append(allowedOrigins, origin)
		}
	}
	return HTTPConfig{
		PublicAddr:     stringEnv("HTTP_ADDR", ":8080"),
		AdminAddr:      stringEnv("ADMIN_ADDR", ":9090"),
		RequestTimeout: requestTimeout,
		DrainDelay:     drainDelay,
		AllowedOrigins: allowedOrigins,
	}, nil
}

func loadDomainPolicy(required bool) (DomainPolicyConfig, error) {
	reservationTTL, err := durationEnv("COUPON_REDEMPTION_RESERVATION_TTL", 15*time.Minute, required)
	if err != nil {
		return DomainPolicyConfig{}, err
	}
	codeReservationTTL, err := durationEnv("COUPON_CODE_RESERVATION_TTL", 15*time.Minute, required)
	if err != nil {
		return DomainPolicyConfig{}, err
	}
	codeHashKey := strings.TrimSpace(os.Getenv("COUPON_CODE_HASH_KEY"))
	if codeHashKey == "" && required {
		return DomainPolicyConfig{}, configErr.With("setting", "COUPON_CODE_HASH_KEY").New("coupon code hash key is required outside local development")
	}
	if codeHashKey == "" {
		codeHashKey = "local-coupon-code-hash-key-change-me"
	}
	commandLease, err := durationEnv("COUPON_COMMAND_LEASE", 30*time.Second, required)
	if err != nil {
		return DomainPolicyConfig{}, err
	}
	idempotencyTTL, err := durationEnv("COUPON_IDEMPOTENCY_TTL", 24*time.Hour, required)
	if err != nil {
		return DomainPolicyConfig{}, err
	}
	return DomainPolicyConfig{
		ReservationTTL:     reservationTTL,
		CodeReservationTTL: codeReservationTTL,
		CodeHashKey:        codeHashKey,
		CommandLease:       commandLease,
		IdempotencyTTL:     idempotencyTTL,
	}, nil
}
