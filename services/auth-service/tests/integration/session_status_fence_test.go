//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type failRevokedStatusCache struct {
	cache *appsession.RedisStatusCache
}

type failSecondFenceStatusCache struct {
	cache      *appsession.RedisStatusCache
	fenceCalls int
}

func (c failRevokedStatusCache) Get(ctx context.Context, sessionID uuid.UUID) (appsession.StatusRecord, error) {
	return c.cache.Get(ctx, sessionID)
}

func (c failRevokedStatusCache) Put(ctx context.Context, record appsession.StatusRecord, ttl time.Duration) error {
	if record.State == appsession.StatusRevoked {
		return errors.New("injected final projection failure")
	}
	return c.cache.Put(ctx, record, ttl)
}

func (c failRevokedStatusCache) PutActiveIfWritable(ctx context.Context, record appsession.StatusRecord, ttl time.Duration) (bool, error) {
	return c.cache.PutActiveIfWritable(ctx, record, ttl)
}

func (c failRevokedStatusCache) RestoreActive(ctx context.Context, record appsession.StatusRecord, version int64, ttl time.Duration) (bool, error) {
	return c.cache.RestoreActive(ctx, record, version, ttl)
}

func (c *failSecondFenceStatusCache) Get(ctx context.Context, sessionID uuid.UUID) (appsession.StatusRecord, error) {
	return c.cache.Get(ctx, sessionID)
}

func (c *failSecondFenceStatusCache) Put(ctx context.Context, record appsession.StatusRecord, ttl time.Duration) error {
	if record.State == appsession.StatusRevoking {
		c.fenceCalls++
		if c.fenceCalls == 2 {
			return errors.New("injected partial fence failure")
		}
	}
	return c.cache.Put(ctx, record, ttl)
}

func (c *failSecondFenceStatusCache) PutActiveIfWritable(ctx context.Context, record appsession.StatusRecord, ttl time.Duration) (bool, error) {
	return c.cache.PutActiveIfWritable(ctx, record, ttl)
}

func (c *failSecondFenceStatusCache) RestoreActive(ctx context.Context, record appsession.StatusRecord, version int64, ttl time.Duration) (bool, error) {
	return c.cache.RestoreActive(ctx, record, version, ttl)
}

type statusFenceFixture struct {
	ctx       context.Context
	db        *pgxpool.Pool
	client    *redis.Client
	cache     *appsession.RedisStatusCache
	userID    uuid.UUID
	sessionID uuid.UUID
	check     appsession.StatusCheck
}

func Test_SessionStatusRevocationFence_real_redis_denies_when_final_projection_fails(t *testing.T) {
	// Given
	fixture := newStatusFenceFixture(t)
	failingStatus := fixture.status(failRevokedStatusCache{cache: fixture.cache})
	sessions := appsession.NewPostgresRepository(fixture.db)
	sessions.UseStatusProjection(failingStatus)
	require.Equal(t, appsession.StatusActive, failingStatus.Check(fixture.ctx, fixture.check))
	staleActive, err := appsession.NewPostgresStatusSource(fixture.db, 15*time.Minute).FindStatus(fixture.ctx, fixture.sessionID)
	require.NoError(t, err)

	// When
	tx, err := fixture.db.Begin(fixture.ctx)
	require.NoError(t, err)
	fences, err := sessions.FenceRevocation(fixture.ctx, tx, fixture.sessionID)
	require.NoError(t, err)
	written, err := fixture.cache.PutActiveIfWritable(fixture.ctx, staleActive, 5*time.Minute)
	require.NoError(t, err)
	require.False(t, written)
	require.Equal(t, appsession.StatusUnavailable, failingStatus.Check(fixture.ctx, fixture.check))
	require.NoError(t, sessions.Revoke(fixture.ctx, tx, fixture.sessionID, "integration_test"))
	require.NoError(t, tx.Commit(fixture.ctx))
	require.Error(t, fences.Resolve(fixture.ctx))

	// Then
	key := appsession.RedisStatusKey(fixture.sessionID)
	require.Equal(t, "revoking", fixture.client.HGet(fixture.ctx, key, "status").Val())
	healthyStatus := fixture.status(fixture.cache)
	require.Equal(t, appsession.StatusRevoked, healthyStatus.Check(fixture.ctx, fixture.check))
	require.Equal(t, "revoked", fixture.client.HGet(fixture.ctx, key, "status").Val())
}

func Test_SessionStatusRevocationFence_real_redis_restores_active_when_transaction_rolls_back(t *testing.T) {
	// Given
	fixture := newStatusFenceFixture(t)
	status := fixture.status(fixture.cache)
	sessions := appsession.NewPostgresRepository(fixture.db)
	sessions.UseStatusProjection(status)
	require.Equal(t, appsession.StatusActive, status.Check(fixture.ctx, fixture.check))

	// When
	tx, err := fixture.db.Begin(fixture.ctx)
	require.NoError(t, err)
	fences, err := sessions.FenceRevocation(fixture.ctx, tx, fixture.sessionID)
	require.NoError(t, err)
	require.NoError(t, sessions.Revoke(fixture.ctx, tx, fixture.sessionID, "integration_test"))
	domain.ResolveRevocationRollback(fixture.ctx, tx, fences)

	// Then
	key := appsession.RedisStatusKey(fixture.sessionID)
	require.Equal(t, "active", fixture.client.HGet(fixture.ctx, key, "status").Val())
	require.Equal(t, appsession.StatusActive, status.Check(fixture.ctx, fixture.check))
}

func Test_SessionStatusRevocationFence_real_redis_restores_partial_fences_after_write_failure(t *testing.T) {
	// Given
	fixture := newStatusFenceFixture(t)
	secondSessionID := uuid.New()
	_, err := fixture.db.Exec(fixture.ctx, `
		INSERT INTO auth_sessions (
			session_id, user_id, identity_id, identity_link_id, authentication_method,
			session_status, client_channel, issued_at, idle_expires_at, absolute_expires_at
		)
		SELECT $2, user_id, identity_id, identity_link_id, authentication_method,
			'active', client_channel, now(), now() + interval '20 minutes', now() + interval '1 hour'
		FROM auth_sessions WHERE session_id = $1
	`, fixture.sessionID, secondSessionID)
	require.NoError(t, err)
	failingCache := &failSecondFenceStatusCache{cache: fixture.cache}
	status := fixture.status(failingCache)
	sessions := appsession.NewPostgresRepository(fixture.db)
	sessions.UseStatusProjection(status)
	secondCheck := appsession.StatusCheck{UserID: fixture.userID, SessionID: secondSessionID, TokenID: uuid.New()}
	require.Equal(t, appsession.StatusActive, status.Check(fixture.ctx, fixture.check))
	require.Equal(t, appsession.StatusActive, status.Check(fixture.ctx, secondCheck))
	tx, err := fixture.db.Begin(fixture.ctx)
	require.NoError(t, err)

	// When
	fences, fenceErr := sessions.FenceRevocationsForUser(fixture.ctx, tx, fixture.userID)
	require.Error(t, fenceErr)
	domain.ResolveRevocationRollback(fixture.ctx, tx, fences)

	// Then
	healthyStatus := fixture.status(fixture.cache)
	require.Equal(t, appsession.StatusActive, healthyStatus.Check(fixture.ctx, fixture.check))
	require.Equal(t, appsession.StatusActive, healthyStatus.Check(fixture.ctx, secondCheck))
	require.Equal(t, "active", fixture.client.HGet(fixture.ctx, appsession.RedisStatusKey(fixture.sessionID), "status").Val())
	require.Equal(t, "active", fixture.client.HGet(fixture.ctx, appsession.RedisStatusKey(secondSessionID), "status").Val())
}

func newStatusFenceFixture(t *testing.T) statusFenceFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	db := migratedDomainPool(t, ctx)
	options, err := redis.ParseURL(startRedis(t, ctx))
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
	return statusFenceFixture{
		ctx: ctx, db: db, client: client, cache: appsession.NewRedisStatusCache(client),
		userID: userID, sessionID: sessionID,
		check: appsession.StatusCheck{UserID: userID, SessionID: sessionID, TokenID: uuid.New()},
	}
}

func (f statusFenceFixture) status(cache appsession.StatusCache) *appsession.StatusService {
	return appsession.NewStatusService(appsession.StatusServiceOptions{
		Cache: cache, Source: appsession.NewPostgresStatusSource(f.db, 15*time.Minute),
		Config: appsession.StatusServiceConfig{
			ActiveTTL: 5 * time.Minute, AccessTTL: 15 * time.Minute,
			FallbackTimeout: 100 * time.Millisecond, MaxFallbacks: 32,
		},
	})
}
