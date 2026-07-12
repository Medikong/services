package httpmiddleware

import (
	"os"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/samber/oops"
)

var redisLockConfigErr = oops.In("redis_lock_config").Code("config.invalid")

type RedisLockPolicy struct {
	TTL            time.Duration
	AcquireTimeout time.Duration
	RetryInterval  time.Duration
	Refresh        time.Duration
	ReleaseTimeout time.Duration
}

func LoadRedisLockPolicyFromEnv() (RedisLockPolicy, error) {
	ttl, err := redisLockDurationEnv("REDIS_LOCK_TTL", 15*time.Second)
	if err != nil {
		return RedisLockPolicy{}, err
	}
	acquireTimeout, err := redisLockDurationEnv("REDIS_LOCK_ACQUIRE_TIMEOUT", 200*time.Millisecond)
	if err != nil {
		return RedisLockPolicy{}, err
	}
	retryInterval, err := redisLockDurationEnv("REDIS_LOCK_RETRY_INTERVAL", 25*time.Millisecond)
	if err != nil {
		return RedisLockPolicy{}, err
	}
	refresh, err := redisLockDurationEnv("REDIS_LOCK_REFRESH_INTERVAL", 5*time.Second)
	if err != nil {
		return RedisLockPolicy{}, err
	}
	releaseTimeout, err := redisLockDurationEnv("REDIS_LOCK_RELEASE_TIMEOUT", time.Second)
	if err != nil {
		return RedisLockPolicy{}, err
	}
	policy := RedisLockPolicy{
		TTL:            ttl,
		AcquireTimeout: acquireTimeout,
		RetryInterval:  retryInterval,
		Refresh:        refresh,
		ReleaseTimeout: releaseTimeout,
	}
	if err := policy.Validate(); err != nil {
		return RedisLockPolicy{}, err
	}
	return policy, nil
}

func (p RedisLockPolicy) Validate() error {
	err := validation.ValidateStruct(&p,
		validation.Field(&p.TTL, validation.Min(time.Nanosecond)),
		validation.Field(&p.AcquireTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&p.RetryInterval, validation.Min(time.Nanosecond)),
		validation.Field(
			&p.Refresh,
			validation.Min(time.Nanosecond),
			validation.Max(p.TTL-time.Nanosecond).Error("must be less than TTL"),
		),
		validation.Field(&p.ReleaseTimeout, validation.Min(time.Nanosecond)),
	)
	if err != nil {
		return redisLockConfigErr.With("config", "redis_lock").Wrap(err)
	}
	return nil
}

func redisLockDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, redisLockConfigErr.With("setting", name, "value", raw).Wrap(err)
	}
	return value, nil
}
