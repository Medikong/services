//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	postgresinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	redisinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func Test_SessionRevocationFence_real_Redis_times_out_during_transaction_and_recovers_commit(t *testing.T) {
	fixture := newProjectionFenceFixture(t)
	allowed, err := fixture.projection.Check(fixture.ctx, fixture.userID, fixture.sessionID)
	require.NoError(t, err)
	require.True(t, allowed)

	tx, err := fixture.db.Begin(fixture.ctx)
	require.NoError(t, err)
	repository := postgresinfra.NewSessionTxRepository(tx)
	current, err := repository.FindActiveForUpdate(fixture.ctx, fixture.sessionID)
	require.NoError(t, err)
	fence, err := fixture.projection.Fence(fixture.ctx, []domainsession.Session{current})
	require.NoError(t, err)
	require.NotNil(t, fence)
	allowed, err = fixture.projection.Check(fixture.ctx, fixture.userID, fixture.sessionID)
	require.Error(t, err)
	require.False(t, allowed)
	assertCachedProjection(t, fixture.ctx, fixture.client, fixture.sessionID.String(), "revoking", 0)
	require.NoError(t, repository.Revoke(fixture.ctx, fixture.sessionID, "integration_test"))
	require.NoError(t, tx.Commit(fixture.ctx))

	allowed, err = fixture.projection.Check(fixture.ctx, fixture.userID, fixture.sessionID)
	require.NoError(t, err)
	require.False(t, allowed)
	assertCachedProjection(t, fixture.ctx, fixture.client, fixture.sessionID.String(), domainsession.StatusRevoked, 1)
	assertProjectionOutboxState(t, fixture.ctx, fixture.db, fixture.sessionID, 1, "pending", "pending")
}

func Test_SessionRevocationFence_real_Redis_recovers_active_after_orphaned_rollback(t *testing.T) {
	fixture := newProjectionFenceFixture(t)
	allowed, err := fixture.projection.Check(fixture.ctx, fixture.userID, fixture.sessionID)
	require.NoError(t, err)
	require.True(t, allowed)

	tx, err := fixture.db.Begin(fixture.ctx)
	require.NoError(t, err)
	repository := postgresinfra.NewSessionTxRepository(tx)
	current, err := repository.FindActiveForUpdate(fixture.ctx, fixture.sessionID)
	require.NoError(t, err)
	fence, err := fixture.projection.Fence(fixture.ctx, []domainsession.Session{current})
	require.NoError(t, err)
	require.NotNil(t, fence)
	require.NoError(t, repository.Revoke(fixture.ctx, fixture.sessionID, "integration_test"))
	require.NoError(t, tx.Rollback(fixture.ctx))

	allowed, err = fixture.projection.Check(fixture.ctx, fixture.userID, fixture.sessionID)
	require.NoError(t, err)
	require.True(t, allowed)
	assertCachedProjection(t, fixture.ctx, fixture.client, fixture.sessionID.String(), "active", 0)
	var jobs, events int
	require.NoError(t, fixture.db.QueryRow(fixture.ctx, `
		SELECT
			(SELECT count(*) FROM auth_session_status_projection_jobs WHERE session_id = $1),
			(SELECT count(*) FROM auth_outbox_events WHERE aggregate_id = $1)
	`, fixture.sessionID).Scan(&jobs, &events))
	require.Zero(t, jobs)
	require.Zero(t, events)
}

func Test_SessionTxRepository_locks_user_and_identity_link_revocation_targets(t *testing.T) {
	fixture := newProjectionFenceFixture(t)
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
	var identityLinkID uuid.UUID
	require.NoError(t, fixture.db.QueryRow(fixture.ctx, `
		SELECT identity_link_id FROM auth_sessions WHERE session_id = $1
	`, fixture.sessionID).Scan(&identityLinkID))

	tx, err := fixture.db.Begin(fixture.ctx)
	require.NoError(t, err)
	repository := postgresinfra.NewSessionTxRepository(tx)
	userSessions, err := repository.FindActiveForUserForUpdate(fixture.ctx, fixture.userID)
	require.NoError(t, err)
	require.Len(t, userSessions, 2)
	linkedSessions, err := repository.FindActiveForIdentityLinkExceptForUpdate(fixture.ctx, identityLinkID, fixture.sessionID)
	require.NoError(t, err)
	require.Len(t, linkedSessions, 1)
	require.Equal(t, secondSessionID, linkedSessions[0].ID)
	require.NoError(t, tx.Rollback(fixture.ctx))
}

type projectionFenceFixture struct {
	ctx        context.Context
	db         *pgxpool.Pool
	client     *redis.Client
	projection *redisinfra.SessionProjection
	userID     uuid.UUID
	sessionID  uuid.UUID
}

func newProjectionFenceFixture(t *testing.T) projectionFenceFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	db := migratedDomainPool(t, ctx)
	options, err := redis.ParseURL(startRedis(t, ctx))
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	userID, sessionID := seedProjectionSession(t, ctx, db)
	projection, err := redisinfra.NewSessionProjection(
		postgresinfra.NewSessionRepository(db), client,
		time.Second, 100*time.Millisecond, 5*time.Minute, 15*time.Minute, 32,
	)
	require.NoError(t, err)
	return projectionFenceFixture{
		ctx: ctx, db: db, client: client, projection: projection,
		userID: userID, sessionID: sessionID,
	}
}
