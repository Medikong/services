//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	applicationsessionprojection "github.com/Medikong/services/services/auth-service/internal/application/sessionprojection"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	postgresinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	redisinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func Test_SessionStatusProjection_retries_Redis_failure_and_acknowledges_exact_outbox_version(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)

	options, err := redis.ParseURL(startRedis(t, ctx))
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	userID, sessionID := seedProjectionSession(t, ctx, db)
	sessions := postgresinfra.NewSessionRepository(db)
	projection, err := redisinfra.NewSessionProjection(
		sessions, client, time.Second, 100*time.Millisecond, 5*time.Minute, 15*time.Minute, 32,
	)
	require.NoError(t, err)
	allowed, err := projection.Check(ctx, userID, sessionID)
	require.NoError(t, err)
	require.True(t, allowed)

	_, err = db.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = 'integration_test'
		WHERE session_id = $1
	`, sessionID)
	require.NoError(t, err)
	assertProjectionOutboxState(t, ctx, db, sessionID, 1, "pending", "pending")

	unavailableClient := redis.NewClient(&redis.Options{
		Addr: runtimeUnusedAddress(t), DialTimeout: 20 * time.Millisecond,
		ReadTimeout: 20 * time.Millisecond, WriteTimeout: 20 * time.Millisecond,
	})
	t.Cleanup(func() { require.NoError(t, unavailableClient.Close()) })
	unavailableProjection, err := redisinfra.NewSessionProjection(
		sessions, unavailableClient, 100*time.Millisecond, 20*time.Millisecond, 5*time.Minute, 15*time.Minute, 32,
	)
	require.NoError(t, err)
	repository := postgresinfra.NewSessionStatusProjectionRepository(db)
	failingRelay := newProjectionRelay(t, repository, unavailableProjection, "status-worker-failing")
	result, err := failingRelay.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, applicationsessionprojection.Result{Claimed: 1, Retried: 1}, result)
	assertProjectionOutboxState(t, ctx, db, sessionID, 1, "pending", "pending")

	time.Sleep(20 * time.Millisecond)
	healthyRelay := newProjectionRelay(t, repository, projection, "status-worker-healthy")
	result, err = healthyRelay.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, applicationsessionprojection.Result{Claimed: 1, Applied: 1}, result)
	assertProjectionOutboxState(t, ctx, db, sessionID, 1, "published", "pending")

	allowed, err = projection.Check(ctx, userID, sessionID)
	require.NoError(t, err)
	require.False(t, allowed)
	encoded, err := client.Get(ctx, "auth:session-status:v2:"+sessionID.String()).Bytes()
	require.NoError(t, err)
	var cached struct {
		Status  string `json:"status"`
		Version int64  `json:"version"`
	}
	require.NoError(t, json.Unmarshal(encoded, &cached))
	require.Equal(t, domainsession.StatusRevoked, cached.Status)
	require.EqualValues(t, 1, cached.Version)
	ttl, err := client.TTL(ctx, "auth:session-status:v2:"+sessionID.String()).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
	require.LessOrEqual(t, ttl, 15*time.Minute)
}

func newProjectionRelay(
	t *testing.T,
	repository applicationsessionprojection.Repository,
	sink applicationsessionprojection.Sink,
	workerID string,
) *applicationsessionprojection.Service {
	t.Helper()
	relay, err := applicationsessionprojection.New(repository, sink, applicationsessionprojection.Config{
		WorkerID: workerID, BatchSize: 10, PollInterval: time.Second,
		Lease: 2 * time.Second, ApplyTimeout: 500 * time.Millisecond,
		BaseBackoff: 5 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	return relay
}

func seedProjectionSession(t *testing.T, ctx context.Context, db *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	userID, identityID, linkID, sessionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedRefreshPrincipal(t, ctx, db, userID, identityID, linkID)
	_, err := db.Exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, user_id, identity_id, identity_link_id, authentication_method,
			session_status, client_channel, issued_at, idle_expires_at, absolute_expires_at
		) VALUES ($1, $2, $3, $4, 'email_password', 'active', 'web', now(), now() + interval '20 minutes', now() + interval '1 hour')
	`, sessionID, userID, identityID, linkID)
	require.NoError(t, err)
	return userID, sessionID
}

func assertProjectionOutboxState(
	t *testing.T,
	ctx context.Context,
	db *pgxpool.Pool,
	sessionID uuid.UUID,
	version int64,
	wantCacheStatus, wantRevokedStatus string,
) {
	t.Helper()
	var cacheStatus, revokedStatus string
	err := db.QueryRow(ctx, `
		SELECT
			max(publish_status) FILTER (WHERE event_type = 'Auth.SessionStatusCacheUpdated'),
			max(publish_status) FILTER (WHERE event_type = 'Auth.SessionRevoked')
		FROM auth_outbox_events
		WHERE aggregate_type = 'Session' AND aggregate_id = $1 AND aggregate_version = $2
	`, sessionID, version).Scan(&cacheStatus, &revokedStatus)
	require.NoError(t, err)
	require.Equal(t, wantCacheStatus, cacheStatus)
	require.Equal(t, wantRevokedStatus, revokedStatus)
}
