//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	appoperator "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/policy"
	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func Test_SessionStatusCache_real_redis_active_fill_requires_absent_key(t *testing.T) {
	states := []appsession.StatusState{
		appsession.StatusActive,
		appsession.StatusExpired,
		appsession.StatusRevoked,
		appsession.StatusRevoking,
		appsession.StatusUnavailable,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			// Given
			fixture := newStatusFenceFixture(t)
			existing := appsession.StatusRecord{
				UserID: fixture.userID, SessionID: fixture.sessionID, State: state,
				AbsoluteExpiresAt: time.Now().UTC().Add(time.Hour), Version: 9,
			}
			require.NoError(t, fixture.cache.Put(fixture.ctx, existing, time.Minute))
			stale := existing
			stale.State = appsession.StatusActive
			stale.Version = 1

			// When
			written, err := fixture.cache.PutActiveIfWritable(fixture.ctx, stale, time.Minute)

			// Then
			require.NoError(t, err)
			require.False(t, written)
			require.Equal(t, string(state), fixture.client.HGet(fixture.ctx, appsession.RedisStatusKey(fixture.sessionID), "status").Val())
		})
	}
}

func Test_SessionStatusFence_real_redis_covers_access_token_revocation_horizon(t *testing.T) {
	// Given
	fixture := newStatusFenceFixture(t)
	status := fixture.status(fixture.cache)
	sessions := appsession.NewPostgresRepository(fixture.db)
	sessions.UseStatusProjection(status)
	tx, err := fixture.db.Begin(fixture.ctx)
	require.NoError(t, err)
	t.Cleanup(func() { domain.ResolveRevocationRollback(fixture.ctx, tx, nil) })

	// When
	fences, err := sessions.FenceRevocation(fixture.ctx, tx, fixture.sessionID)
	require.NoError(t, err)
	ttl := fixture.client.TTL(fixture.ctx, appsession.RedisStatusKey(fixture.sessionID)).Val()

	// Then
	require.GreaterOrEqual(t, ttl, 14*time.Minute)
	domain.ResolveRevocationRollback(fixture.ctx, tx, fences)
}

func Test_SessionStatusCache_real_redis_stale_active_fill_cannot_replace_revoked(t *testing.T) {
	// Given
	fixture := newStatusFenceFixture(t)
	revoked := appsession.StatusRecord{
		UserID: fixture.userID, SessionID: fixture.sessionID, State: appsession.StatusRevoked,
		AbsoluteExpiresAt: time.Now().UTC().Add(time.Hour), Version: 10,
	}
	require.NoError(t, fixture.cache.Put(fixture.ctx, revoked, 15*time.Minute))
	stale := revoked
	stale.State = appsession.StatusActive
	stale.Version = 1

	// When
	written, err := fixture.cache.PutActiveIfWritable(fixture.ctx, stale, time.Minute)

	// Then
	require.NoError(t, err)
	require.False(t, written)
	require.Equal(t, "revoked", fixture.client.HGet(fixture.ctx, appsession.RedisStatusKey(fixture.sessionID), "status").Val())
	require.Equal(t, "10", fixture.client.HGet(fixture.ctx, appsession.RedisStatusKey(fixture.sessionID), "status_version").Val())
}

func Test_OperatorRevokeSessions_real_postgres_redis_fences_and_projects_revoked(t *testing.T) {
	// Given
	fixture := newStatusFenceFixture(t)
	status := fixture.status(fixture.cache)
	sessions := appsession.NewPostgresRepository(fixture.db)
	sessions.UseStatusProjection(status)
	require.Equal(t, appsession.StatusActive, status.Check(fixture.ctx, fixture.check))
	operatorID := uuid.New()
	seedOperatorState(t, fixture.ctx, fixture.db, operatorID)
	service := appoperator.NewService(
		fixture.db, testOperatorKeys(t), appoperator.NewPostgresRepository(fixture.db), policy.NewPostgresRepository(fixture.db),
		idempotency.NewPostgresRepository(fixture.db), outbox.NewPostgresRepository(fixture.db), appoperator.Config{StrongAuthTTL: time.Minute},
		appoperator.StaticApprovalPort{Allow: true}, allowAuthorizationDecision{},
	)
	service.UseSessionRevocation(sessions)
	principal := appsession.Principal{
		Authenticated: true, SessionID: uuid.New(), UserID: operatorID,
		Method: "email_password", AuthenticatedAt: time.Now().UTC(),
	}

	// When
	_, version, err := service.Manual(fixture.ctx, appoperator.ManualInput{
		Principal: principal, CaseID: "case-session-revoke", TargetType: "session", TargetID: fixture.sessionID.String(), Action: "revoke_sessions",
		ReasonCode: "CUSTOMER_SUPPORT", ApprovalID: "approval-session-revoke", EvidenceRef: "evidence-session-revoke",
		ExpectedVersion: 0, IdempotencyKey: uuid.NewString(), AuthorizationDecision: "allow",
	})

	// Then
	require.NoError(t, err)
	require.EqualValues(t, 1, version)
	require.Equal(t, "revoked", fixture.client.HGet(fixture.ctx, appsession.RedisStatusKey(fixture.sessionID), "status").Val())
	require.Equal(t, appsession.StatusRevoked, status.Check(fixture.ctx, fixture.check))
	var corrections int
	require.NoError(t, fixture.db.QueryRow(fixture.ctx, `
		SELECT count(*) FROM auth_outbox_events
		WHERE aggregate_id = $1 AND event_type = 'Auth.SessionStatusCacheUpdated'
	`, fixture.sessionID).Scan(&corrections))
	require.Equal(t, 1, corrections)
}
