package httpmiddleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
)

type RedisLockKey struct {
	Lock  string
	Fence string
}

type RedisLockConfig struct {
	Client   *redislock.Client
	Redis    redis.Cmdable
	Key      func(*http.Request) (RedisLockKey, error)
	Policy   RedisLockPolicy
	OnResult func(string)
}

type fencingTokenKey struct{}

func RedisLock(config RedisLockConfig) (Middleware, error) {
	if err := validateRedisLockConfig(&config); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, err := config.Key(r)
			if err != nil {
				config.record("key_error")
				httpapi.WriteError(w, r, err)
				return
			}
			key.Lock = strings.TrimSpace(key.Lock)
			key.Fence = strings.TrimSpace(key.Fence)
			if key.Lock == "" || key.Fence == "" {
				config.record("key_error")
				httpapi.WriteError(w, r, httpapi.Internal(oops.
					In("redis_lock").
					Code("redis_lock.invalid_key").
					New("lock and fence keys are required")))
				return
			}

			acquireCtx, cancelAcquire := context.WithTimeout(r.Context(), config.Policy.AcquireTimeout)
			lock, err := config.Client.Obtain(acquireCtx, key.Lock, config.Policy.TTL, &redislock.Options{
				RetryStrategy: redislock.LinearBackoff(config.Policy.RetryInterval),
			})
			cancelAcquire()
			if err != nil {
				if errors.Is(err, redislock.ErrNotObtained) || errors.Is(err, context.DeadlineExceeded) {
					config.record("busy")
					httpapi.WriteError(w, r, httpapi.NewError(
						http.StatusLocked,
						"common.resource_locked",
						"같은 리소스의 요청이 처리 중입니다.",
						nil,
					))
					return
				}
				config.record("acquire_error")
				httpapi.WriteError(w, r, httpapi.Internal(oops.
					In("redis_lock").
					Code("redis_lock.acquire_failed").
					With("lock_key", key.Lock).
					Wrap(err)))
				return
			}

			fence, err := config.Redis.Incr(r.Context(), key.Fence).Result()
			if err != nil {
				config.record("fence_error")
				release(config, lock, key.Lock)
				httpapi.WriteError(w, r, httpapi.Internal(oops.
					In("redis_lock").
					Code("redis_lock.fence_failed").
					With("fence_key", key.Fence).
					Wrap(err)))
				return
			}

			workCtx, cancelWork := context.WithCancelCause(r.Context())
			workCtx = context.WithValue(workCtx, fencingTokenKey{}, fence)
			watchDone := make(chan error, 1)
			stopWatch := make(chan struct{})
			go watchRedisLock(workCtx, cancelWork, stopWatch, lock, config, watchDone)
			defer func() {
				cancelWork(nil)
				close(stopWatch)
				if refreshErr := <-watchDone; refreshErr != nil {
					config.record("refresh_error")
					logger.Error(r.Context(), "redis.lock.refresh_failed",
						"lock_key", key.Lock,
						logger.Err(refreshErr),
					)
				}
				release(config, lock, key.Lock)
			}()

			config.record("acquired")
			next.ServeHTTP(w, r.WithContext(workCtx))
		})
	}, nil
}

func FencingToken(ctx context.Context) int64 {
	token, _ := ctx.Value(fencingTokenKey{}).(int64)
	return token
}

func watchRedisLock(
	ctx context.Context,
	cancel context.CancelCauseFunc,
	stop <-chan struct{},
	lock *redislock.Lock,
	config RedisLockConfig,
	done chan<- error,
) {
	ticker := time.NewTicker(config.Policy.Refresh)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			done <- nil
			return
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			if err := lock.Refresh(ctx, config.Policy.TTL, nil); err != nil {
				wrapped := oops.
					In("redis_lock").
					Code("redis_lock.refresh_failed").
					With("lock_key", lock.Key()).
					Wrap(err)
				cancel(wrapped)
				done <- wrapped
				return
			}
		}
	}
}

func release(config RedisLockConfig, lock *redislock.Lock, key string) {
	releaseCtx, cancel := context.WithTimeout(context.Background(), config.Policy.ReleaseTimeout)
	defer cancel()
	if err := lock.Release(releaseCtx); err != nil && !errors.Is(err, redislock.ErrLockNotHeld) {
		config.record("release_error")
		logger.Error(releaseCtx, "redis.lock.release_failed", "lock_key", key, logger.Err(err))
	}
}

func validateRedisLockConfig(config *RedisLockConfig) error {
	errBuilder := oops.In("redis_lock").Code("redis_lock.invalid_config")
	switch {
	case config.Client == nil:
		return errBuilder.New("redis lock client is required")
	case config.Redis == nil:
		return errBuilder.New("redis command client is required")
	case config.Key == nil:
		return errBuilder.New("redis lock key function is required")
	}
	return config.Policy.Validate()
}

func (c RedisLockConfig) record(result string) {
	if c.OnResult != nil {
		c.OnResult(result)
	}
}
