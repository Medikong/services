package httpmiddleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bsm/redislock"
	"github.com/redis/go-redis/v9"
)

func TestRedisLockAddsFencingTokenAndReleases(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	middleware, err := RedisLock(RedisLockConfig{
		Client: redislock.New(client),
		Redis:  client,
		Key:    testLockKey,
		Policy: testRedisLockPolicy(),
	})
	if err != nil {
		t.Fatalf("RedisLock() error = %v", err)
	}
	var tokens []int64
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokens = append(tokens, FencingToken(r.Context()))
		w.WriteHeader(http.StatusNoContent)
	}))

	for range 2 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/locked", nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
		}
	}
	if len(tokens) != 2 || tokens[0] != 1 || tokens[1] != 2 {
		t.Fatalf("fencing tokens = %v, want [1 2]", tokens)
	}
}

func TestRedisLockReturnsLockedWhenKeyIsHeld(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	locker := redislock.New(client)
	held, err := locker.Obtain(context.Background(), "lock:{resource-1}", time.Second, nil)
	if err != nil {
		t.Fatalf("obtain held lock: %v", err)
	}
	t.Cleanup(func() { _ = held.Release(context.Background()) })

	policy := testRedisLockPolicy()
	policy.AcquireTimeout = 30 * time.Millisecond
	policy.RetryInterval = 5 * time.Millisecond
	middleware, err := RedisLock(RedisLockConfig{
		Client: locker,
		Redis:  client,
		Key:    testLockKey,
		Policy: policy,
	})
	if err != nil {
		t.Fatalf("RedisLock() error = %v", err)
	}
	response := httptest.NewRecorder()
	middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("locked handler was called")
	})).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/locked", nil))

	if response.Code != http.StatusLocked {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusLocked)
	}
}

func TestRedisLockReleasesWhenHandlerPanics(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	locker := redislock.New(client)
	middleware, err := RedisLock(RedisLockConfig{
		Client: locker,
		Redis:  client,
		Key:    testLockKey,
		Policy: testRedisLockPolicy(),
	})
	if err != nil {
		t.Fatalf("RedisLock() error = %v", err)
	}
	handler := middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/locked", nil))
	}()
	if !panicked {
		t.Fatal("handler panic was not propagated")
	}

	lock, err := locker.Obtain(context.Background(), "lock:{resource-1}", time.Second, nil)
	if err != nil {
		t.Fatalf("lock was not released after panic: %v", err)
	}
	_ = lock.Release(context.Background())
}

func TestLoadRedisLockPolicyFromEnvRejectsRefreshAtTTL(t *testing.T) {
	t.Setenv("REDIS_LOCK_TTL", "5s")
	t.Setenv("REDIS_LOCK_REFRESH_INTERVAL", "5s")

	if _, err := LoadRedisLockPolicyFromEnv(); err == nil {
		t.Fatal("LoadRedisLockPolicyFromEnv() error = nil, want invalid refresh")
	}
}

func testRedisLockPolicy() RedisLockPolicy {
	return RedisLockPolicy{
		TTL:            time.Second,
		AcquireTimeout: 100 * time.Millisecond,
		RetryInterval:  5 * time.Millisecond,
		Refresh:        100 * time.Millisecond,
		ReleaseTimeout: 100 * time.Millisecond,
	}
}

func testLockKey(*http.Request) (RedisLockKey, error) {
	return RedisLockKey{
		Lock:  "lock:{resource-1}",
		Fence: "lock:{resource-1}:fence",
	}, nil
}
