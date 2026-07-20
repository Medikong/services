//go:build integration

package migration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestSessionStatusProjectionUpgradeBackfillsPublishedMigrationHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := newMigrationTestPool(t, ctx)

	provider, err := migrationProvider(pool, migrationsFS, "migrations", migrationTable, false, "auth_migration_test")
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if _, err := provider.UpTo(ctx, 9); err != nil {
		_ = provider.Close()
		t.Fatalf("migrate through published version 9: %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("close migration provider: %v", err)
	}

	userID, identityID, sessionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO auth_identities (
			identity_id, identity_type, normalized_value, status,
			verification_status, credential_status, verified_at
		) VALUES ($1, 'provider_subject', $2, 'active', 'verified', 'active', now())
	`, identityID, "migration-projection:"+identityID.String()); err != nil {
		t.Fatalf("insert migration identity: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO auth_user_auth_states (user_id, status, user_version, status_change_id)
		VALUES ($1, 'active', 1, $2)
	`, userID, "migration-projection:"+userID.String()); err != nil {
		t.Fatalf("insert migration user state: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, user_id, identity_id, authentication_method,
			session_status, client_channel, remember_me, absolute_expires_at
		) VALUES ($1, $2, $3, 'provider', 'active', 'ios', false, now() + interval '1 hour')
	`, sessionID, userID, identityID); err != nil {
		t.Fatalf("insert migration session: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = 'migration_test'
		WHERE session_id = $1
	`, sessionID); err != nil {
		t.Fatalf("revoke migration session: %v", err)
	}
	var cacheEvents, revokedEvents int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type = 'Auth.SessionStatusCacheUpdated'),
			count(*) FILTER (WHERE event_type = 'Auth.SessionRevoked')
		FROM auth_outbox_events WHERE aggregate_id = $1
	`, sessionID).Scan(&cacheEvents, &revokedEvents); err != nil {
		t.Fatalf("count published-history events: %v", err)
	}
	if cacheEvents != 1 || revokedEvents != 1 {
		t.Fatalf("published-history event counts = (%d, %d), want (1, 1)", cacheEvents, revokedEvents)
	}
	var projectionTable *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('auth_session_status_projection_jobs')::text`).Scan(&projectionTable); err != nil {
		t.Fatalf("check pre-upgrade projection table: %v", err)
	}
	if projectionTable != nil {
		t.Fatalf("projection table exists before version 10: %s", *projectionTable)
	}

	provider, err = migrationProvider(pool, migrationsFS, "migrations", migrationTable, false, "auth_migration_test")
	if err != nil {
		t.Fatalf("recreate migration provider: %v", err)
	}
	if _, err := provider.UpTo(ctx, 10); err != nil {
		_ = provider.Close()
		t.Fatalf("migrate to projection jobs version 10: %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("close upgraded migration provider: %v", err)
	}

	var version int64
	var status, deliveryStatus string
	if err := pool.QueryRow(ctx, `
		SELECT session_version, target_status, delivery_status
		FROM auth_session_status_projection_jobs
		WHERE session_id = $1
	`, sessionID).Scan(&version, &status, &deliveryStatus); err != nil {
		t.Fatalf("read backfilled projection job: %v", err)
	}
	if version != 1 || status != "revoked" || deliveryStatus != "pending" {
		t.Fatalf("backfilled projection = (%d, %s, %s), want (1, revoked, pending)", version, status, deliveryStatus)
	}
}

func newMigrationTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("auth"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start migration postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("migration postgres connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open migration postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
