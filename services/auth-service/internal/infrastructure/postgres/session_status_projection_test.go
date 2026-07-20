//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSessionStatusProjectionTriggerIsAtomicAndVersioned(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedIntentPool(t, ctx)

	rolledBackSession := insertProjectionTestSession(t, ctx, db)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = 'test'
		WHERE session_id = $1
	`, rolledBackSession); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("update rolled-back session: %v", err)
	}
	assertProjectionCount(t, ctx, tx, rolledBackSession, 1)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	assertProjectionCount(t, ctx, db, rolledBackSession, 0)
	assertSessionProjectionState(t, ctx, db, rolledBackSession, "active", 0)

	revokedSession := insertProjectionTestSession(t, ctx, db)
	if _, err := db.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = 'test'
		WHERE session_id = $1
	`, revokedSession); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	assertSessionProjectionState(t, ctx, db, revokedSession, domainsession.StatusRevoked, 1)
	assertProjectionCount(t, ctx, db, revokedSession, 1)
	if _, err := db.Exec(ctx, `
		UPDATE auth_sessions SET updated_at = now() WHERE session_id = $1
	`, revokedSession); err != nil {
		t.Fatalf("repeat terminal update: %v", err)
	}
	assertProjectionCount(t, ctx, db, revokedSession, 1)

	reusedSession := insertProjectionTestSession(t, ctx, db)
	if _, err := db.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'reuse_detected', reuse_detected_at = now(),
			revocation_reason = 'refresh_reuse', row_version = row_version + 1
		WHERE session_id = $1
	`, reusedSession); err != nil {
		t.Fatalf("mark reuse detected: %v", err)
	}
	assertSessionProjectionState(t, ctx, db, reusedSession, domainsession.StatusReuseDetected, 1)
	assertProjectionCount(t, ctx, db, reusedSession, 1)
}

func TestSessionStatusProjectionRepositoryLeasesRetriesAndCleans(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedIntentPool(t, ctx)

	change := domainsession.StatusChange{
		SessionID: uuid.New(), UserID: uuid.New(), Status: domainsession.StatusRevoked,
		Version: 1, ValidUntil: time.Now().UTC().Add(time.Hour), OccurredAt: time.Now().UTC(),
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin enqueue transaction: %v", err)
	}
	appender := NewSessionStatusProjectionAppender(tx)
	if err := appender.Enqueue(ctx, []domainsession.StatusChange{change, change}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("enqueue projection: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit projection: %v", err)
	}
	assertProjectionCount(t, ctx, db, change.SessionID, 1)

	rolledBack := change
	rolledBack.SessionID = uuid.New()
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rolled-back enqueue: %v", err)
	}
	if err := NewSessionStatusProjectionAppender(tx).Enqueue(ctx, []domainsession.StatusChange{rolledBack}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("enqueue rolled-back projection: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback projection: %v", err)
	}
	assertProjectionCount(t, ctx, db, rolledBack.SessionID, 0)

	repository := NewSessionStatusProjectionRepository(db)
	claimed, err := repository.Claim(ctx, "worker-a", 10, time.Minute)
	if err != nil {
		t.Fatalf("claim projection: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed job count = %d, want 1", len(claimed))
	}
	if claimed[0].SessionID != change.SessionID || claimed[0].Attempts != 1 {
		t.Fatal("claimed job fields do not match the queued projection")
	}
	other, err := repository.Claim(ctx, "worker-b", 10, time.Minute)
	if err != nil {
		t.Fatalf("second worker claim: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("second worker claimed job count = %d, want 0", len(other))
	}
	if err := repository.ReleaseForRetry(ctx, claimed[0].JobID, "worker-a", time.Millisecond, "redis_unavailable"); err != nil {
		t.Fatalf("release projection: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	claimed, err = repository.Claim(ctx, "worker-b", 10, time.Minute)
	if err != nil {
		t.Fatalf("reclaim projection: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("reclaimed job count = %d, want 1", len(claimed))
	}
	if claimed[0].Attempts != 2 {
		t.Fatalf("reclaimed attempt count = %d, want 2", claimed[0].Attempts)
	}
	if err := repository.MarkDelivered(ctx, claimed[0].JobID, "worker-a"); !errors.Is(err, ErrSessionStatusProjectionLeaseLost) {
		t.Fatalf("wrong-owner MarkDelivered() error = %v", err)
	}
	if err := repository.MarkDelivered(ctx, claimed[0].JobID, "worker-b"); err != nil {
		t.Fatalf("mark projection delivered: %v", err)
	}
	if _, err := db.Exec(ctx, `
		UPDATE auth_session_status_projection_jobs
		SET delivered_at = now() - interval '2 hours'
		WHERE job_id = $1
	`, claimed[0].JobID); err != nil {
		t.Fatalf("age delivered projection: %v", err)
	}
	deleted, err := repository.DeleteDeliveredBefore(ctx, time.Now().UTC().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("cleanup projections: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted projections = %d, want 1", deleted)
	}
}

type projectionQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func insertProjectionTestSession(t *testing.T, ctx context.Context, db *pgxpool.Pool) uuid.UUID {
	t.Helper()
	userID, identityID, sessionID := uuid.New(), uuid.New(), uuid.New()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin session fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_identities (
			identity_id, identity_type, normalized_value, status,
			verification_status, credential_status, verified_at
		) VALUES ($1, 'provider_subject', $2, 'active', 'verified', 'active', now())
	`, identityID, "projection:"+identityID.String()); err != nil {
		t.Fatalf("insert projection identity: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_user_auth_states (
			user_id, status, user_version, status_change_id
		) VALUES ($1, 'active', 1, $2)
	`, userID, "projection:"+userID.String()); err != nil {
		t.Fatalf("insert projection user state: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth_sessions (
			session_id, user_id, identity_id, authentication_method,
			session_status, client_channel, remember_me, absolute_expires_at
		) VALUES ($1, $2, $3, 'provider', 'active', 'ios', false, now() + interval '1 hour')
	`, sessionID, userID, identityID); err != nil {
		t.Fatalf("insert projection session: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit session fixture: %v", err)
	}
	return sessionID
}

func assertProjectionCount(t *testing.T, ctx context.Context, db projectionQueryer, sessionID uuid.UUID, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_session_status_projection_jobs WHERE session_id = $1`, sessionID).Scan(&got); err != nil {
		t.Fatalf("count session status projections: %v", err)
	}
	if got != want {
		t.Fatalf("projection count = %d, want %d", got, want)
	}
}

func assertSessionProjectionState(t *testing.T, ctx context.Context, db projectionQueryer, sessionID uuid.UUID, wantStatus string, wantVersion int64) {
	t.Helper()
	var status string
	var version int64
	if err := db.QueryRow(ctx, `SELECT session_status, row_version FROM auth_sessions WHERE session_id = $1`, sessionID).Scan(&status, &version); err != nil {
		t.Fatalf("read session state: %v", err)
	}
	if status != wantStatus || version != wantVersion {
		t.Fatalf("session state = (%s, %d), want (%s, %d)", status, version, wantStatus, wantVersion)
	}
}
