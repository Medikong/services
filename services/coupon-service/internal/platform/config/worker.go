package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// WorkerPolicy is runtime input for leasing, retry, and dead-letter cutoff.
// Values outside local/test must be supplied explicitly because HOTSPOT.A.19-06
// has not fixed production retry counts or intervals.
type WorkerPolicy struct {
	BatchSize      int
	PollInterval   time.Duration
	Lease          time.Duration
	AttemptTimeout time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

func (c WorkerPolicy) Validate() error {
	if err := validation.ValidateStruct(&c,
		validation.Field(&c.BatchSize, validation.Min(1)),
		validation.Field(&c.PollInterval, validation.Min(time.Nanosecond)),
		validation.Field(&c.AttemptTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.Lease, validation.Min(time.Nanosecond)),
		validation.Field(&c.MaxAttempts, validation.Min(1)),
		validation.Field(&c.BaseBackoff, validation.Min(time.Nanosecond)),
		validation.Field(&c.MaxBackoff, validation.Min(c.BaseBackoff)),
	); err != nil {
		return err
	}
	multiplier := c.BatchSize
	if multiplier < 2 {
		multiplier = 2
	}
	if c.AttemptTimeout > time.Duration((int64(^uint64(0)>>1))/int64(multiplier)) {
		return fmt.Errorf("worker lease calculation overflows for batch size %d", c.BatchSize)
	}
	requiredLease := c.AttemptTimeout * time.Duration(multiplier)
	if c.Lease < requiredLease {
		return fmt.Errorf("worker lease must cover the sequential batch: got %s, need at least %s", c.Lease, requiredLease)
	}
	return nil
}

type WorkerConfig struct {
	Service   ServiceConfig
	AdminAddr string
	Lifecycle LifecycleConfig
	Postgres  platformdb.PostgresConfig
	Redis     RedisConfig
	Domain    DomainPolicyConfig
	Policy    WorkerPolicy
	Profile   ProfileConfig
}

func LoadWorker() (WorkerConfig, error) {
	service := loadService()
	lifecycle, err := loadLifecycle()
	if err != nil {
		return WorkerConfig{}, err
	}
	profile, err := loadProfile()
	if err != nil {
		return WorkerConfig{}, err
	}
	postgres, err := platformdb.LoadPostgresConfigFromEnv(strings.TrimSpace(os.Getenv("DATABASE_URL")))
	if err != nil {
		return WorkerConfig{}, configErr.With("config", "worker", "setting", "postgres").Wrap(err)
	}
	redisConfig, err := loadRedis()
	if err != nil {
		return WorkerConfig{}, err
	}
	domainPolicy, err := loadDomainPolicy(!allowsLocalDefaults(service.Environment))
	if err != nil {
		return WorkerConfig{}, err
	}
	policy, err := loadWorkerPolicy(!allowsLocalDefaults(service.Environment))
	if err != nil {
		return WorkerConfig{}, err
	}
	config := WorkerConfig{
		Service:   service,
		AdminAddr: stringEnv("WORKER_ADMIN_ADDR", ":9092"),
		Lifecycle: lifecycle,
		Postgres:  postgres,
		Redis:     redisConfig,
		Domain:    domainPolicy,
		Policy:    policy,
		Profile:   profile,
	}
	if err := config.Validate(); err != nil {
		return WorkerConfig{}, err
	}
	return config, nil
}

func (c WorkerConfig) Validate() error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.Service),
		validation.Field(&c.AdminAddr, validation.Required),
		validation.Field(&c.Lifecycle),
		validation.Field(&c.Postgres, validation.By(func(any) error {
			return validation.Validate(c.Postgres.DatabaseURL, validation.Required)
		})),
		validation.Field(&c.Domain),
		validation.Field(&c.Policy),
		validation.Field(&c.Profile),
	)
	if err != nil {
		return configErr.With("config", "worker").Wrap(err)
	}
	if err := c.Redis.Validate(); err != nil {
		return configErr.With("config", "worker", "section", "redis").Wrap(err)
	}
	return nil
}

func loadWorkerPolicy(required bool) (WorkerPolicy, error) {
	batchSize, err := intEnv("COUPON_WORKER_BATCH_SIZE", 1, required)
	if err != nil {
		return WorkerPolicy{}, err
	}
	pollInterval, err := durationEnv("COUPON_WORKER_POLL_INTERVAL", time.Second, required)
	if err != nil {
		return WorkerPolicy{}, err
	}
	lease, err := durationEnv("COUPON_WORKER_LEASE", 30*time.Second, required)
	if err != nil {
		return WorkerPolicy{}, err
	}
	attemptTimeout, err := durationEnv("COUPON_WORKER_ATTEMPT_TIMEOUT", 10*time.Second, required)
	if err != nil {
		return WorkerPolicy{}, err
	}
	maxAttempts, err := intEnv("COUPON_WORKER_MAX_ATTEMPTS", 10, required)
	if err != nil {
		return WorkerPolicy{}, err
	}
	baseBackoff, err := durationEnv("COUPON_WORKER_BASE_BACKOFF", time.Second, required)
	if err != nil {
		return WorkerPolicy{}, err
	}
	maxBackoff, err := durationEnv("COUPON_WORKER_MAX_BACKOFF", time.Minute, required)
	if err != nil {
		return WorkerPolicy{}, err
	}
	return WorkerPolicy{
		BatchSize:      batchSize,
		PollInterval:   pollInterval,
		Lease:          lease,
		AttemptTimeout: attemptTimeout,
		MaxAttempts:    maxAttempts,
		BaseBackoff:    baseBackoff,
		MaxBackoff:     maxBackoff,
	}, nil
}
