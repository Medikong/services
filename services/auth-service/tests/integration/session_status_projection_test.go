//go:build integration

package integration_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func Test_SessionStatusProjection_real_redis_and_postgres_surface(t *testing.T) {
	// Given
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	redisURL := startRedis(t, ctx)
	options, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	userID, identityID, linkID, sessionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedRefreshPrincipal(t, ctx, db, userID, identityID, linkID)
	_, err = db.Exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, user_id, identity_id, identity_link_id, authentication_method,
			session_status, client_channel, issued_at, idle_expires_at, absolute_expires_at
		) VALUES ($1, $2, $3, $4, 'email_password', 'active', 'web', now(), now() + interval '20 minutes', now() + interval '1 hour')
	`, sessionID, userID, identityID, linkID)
	require.NoError(t, err)
	status := appsession.NewStatusService(appsession.StatusServiceOptions{
		Cache:  appsession.NewRedisStatusCache(client),
		Source: appsession.NewPostgresStatusSource(db, 15*time.Minute),
		Config: appsession.StatusServiceConfig{
			ActiveTTL: 5 * time.Minute, AccessTTL: 15 * time.Minute,
			FallbackTimeout: 100 * time.Millisecond, MaxFallbacks: 32,
		},
	})
	sessions := appsession.NewPostgresRepository(db)
	sessions.UseStatusProjection(status)
	check := appsession.StatusCheck{UserID: userID, SessionID: sessionID, TokenID: uuid.New()}

	// When
	active := status.Check(ctx, check)

	// Then
	require.Equal(t, appsession.StatusActive, active)
	key := appsession.RedisStatusKey(sessionID)
	activeFields, err := client.HGetAll(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, "active", activeFields["status"])
	activeTTL, err := client.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.Positive(t, activeTTL)
	require.LessOrEqual(t, activeTTL, 5*time.Minute)
	require.NotContains(t, strings.Join(mapValues(activeFields), " "), check.TokenID.String())
	t.Logf("redis key=auth:session-status:{<synthetic-sid>} status=%s ttl_seconds=%d fields=%v", activeFields["status"], int64(activeTTL.Seconds()), mapKeys(activeFields))
	require.NoError(t, client.HSet(ctx, key, "access_token", "eyJhbGciOiJSUzI1NiJ9.adversarial.signature").Err())

	// When
	tx, err := db.Begin(ctx)
	require.NoError(t, err)
	fences, err := sessions.FenceRevocation(ctx, tx, sessionID)
	require.NoError(t, err)
	err = sessions.Revoke(ctx, tx, sessionID, "integration_test")
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	require.NoError(t, fences.Resolve(ctx))

	// Then
	revokedFields, err := client.HGetAll(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, "revoked", revokedFields["status"])
	require.NotEmpty(t, revokedFields["revoked_until"])
	require.NotContains(t, revokedFields, "access_token")
	revokedTTL, err := client.TTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, revokedTTL, 14*time.Minute)
	require.LessOrEqual(t, revokedTTL, 15*time.Minute)
	t.Logf("redis key=auth:session-status:{<synthetic-sid>} status=%s ttl_seconds=%d revoked_until=present fields=%v", revokedFields["status"], int64(revokedTTL.Seconds()), mapKeys(revokedFields))

	var cacheEvents, revokedEvents int
	err = db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type = 'Auth.SessionStatusCacheUpdated'),
			count(*) FILTER (WHERE event_type = 'Auth.SessionRevoked')
		FROM auth_outbox_events WHERE aggregate_id = $1
	`, sessionID).Scan(&cacheEvents, &revokedEvents)
	require.NoError(t, err)
	require.Equal(t, 1, cacheEvents)
	require.Equal(t, 1, revokedEvents)

	// When
	require.NoError(t, client.Del(ctx, key).Err())
	relay, err := appsession.NewStatusProjectionRelay(
		outbox.NewPostgresRepository(db), status,
		outbox.Config{
			WorkerID: "integration-status-worker", BatchSize: 10, PollInterval: time.Second,
			Lease: time.Minute, MaxAttempts: 5, BaseBackoff: time.Second, MaxBackoff: 30 * time.Second,
		},
	)
	require.NoError(t, err)
	result, err := relay.RunOnce(ctx)

	// Then
	require.NoError(t, err)
	require.Equal(t, 1, result.Published)
	repairedFields, err := client.HGetAll(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, "revoked", repairedFields["status"])
}

func mapValues(fields map[string]string) []string {
	values := make([]string, 0, len(fields))
	for _, value := range fields {
		values = append(values, value)
	}
	return values
}

func mapKeys(fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
