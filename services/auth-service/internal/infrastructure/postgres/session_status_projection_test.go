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
	assertStatusOutboxCounts(t, ctx, tx, rolledBackSession, 1, 1)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	assertProjectionCount(t, ctx, db, rolledBackSession, 0)
	assertStatusOutboxCounts(t, ctx, db, rolledBackSession, 0, 0)
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
	assertStatusOutboxCounts(t, ctx, db, revokedSession, 1, 1)
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
	assertStatusOutboxCounts(t, ctx, db, reusedSession, 1, 1)
}

func TestSessionRepositoryFindStatusForReconciliationWaitsForStatusTransaction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedIntentPool(t, ctx)
	repository := NewSessionRepository(db)

	rollbackSession := insertProjectionTestSession(t, ctx, db)
	rollbackTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	defer func() { _ = rollbackTx.Rollback(context.WithoutCancel(ctx)) }()
	var lockedSession uuid.UUID
	if err := rollbackTx.QueryRow(ctx, `
		SELECT session_id FROM auth_sessions WHERE session_id = $1 FOR UPDATE
	`, rollbackSession).Scan(&lockedSession); err != nil {
		t.Fatalf("lock rollback session: %v", err)
	}
	deadlineCtx, cancelDeadline := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = repository.FindStatusForReconciliation(deadlineCtx, rollbackSession)
	cancelDeadline()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("locked reconciliation error = %v, want context deadline exceeded", err)
	}

	rollbackResult := make(chan sessionReconciliationResult, 1)
	rollbackReadCtx, cancelRollbackRead := context.WithTimeout(ctx, time.Second)
	defer cancelRollbackRead()
	go func() {
		current, readErr := repository.FindStatusForReconciliation(rollbackReadCtx, rollbackSession)
		rollbackResult <- sessionReconciliationResult{current: current, err: readErr}
	}()
	assertReconciliationBlocked(t, rollbackResult)
	if err := rollbackTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback status transaction: %v", err)
	}
	rolledBack := awaitReconciliation(t, rollbackResult)
	if rolledBack.err != nil || rolledBack.current.Status != "active" || rolledBack.current.Version != 0 {
		t.Fatalf("rollback reconciliation did not return the active committed state")
	}

	committedSession := insertProjectionTestSession(t, ctx, db)
	commitTx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin commit transaction: %v", err)
	}
	defer func() { _ = commitTx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := commitTx.Exec(ctx, `
		UPDATE auth_sessions
		SET session_status = 'revoked', revoked_at = now(), revocation_reason = 'test'
		WHERE session_id = $1
	`, committedSession); err != nil {
		t.Fatalf("update committed session: %v", err)
	}
	commitResult := make(chan sessionReconciliationResult, 1)
	commitReadCtx, cancelCommitRead := context.WithTimeout(ctx, time.Second)
	defer cancelCommitRead()
	go func() {
		current, readErr := repository.FindStatusForReconciliation(commitReadCtx, committedSession)
		commitResult <- sessionReconciliationResult{current: current, err: readErr}
	}()
	assertReconciliationBlocked(t, commitResult)
	if err := commitTx.Commit(ctx); err != nil {
		t.Fatalf("commit status transaction: %v", err)
	}
	committed := awaitReconciliation(t, commitResult)
	if committed.err != nil || committed.current.Status != domainsession.StatusRevoked || committed.current.Version != 1 {
		t.Fatalf("commit reconciliation did not return the terminal committed state")
	}
}

type sessionReconciliationResult struct {
	current domainsession.Session
	err     error
}

func assertReconciliationBlocked(t *testing.T, result <-chan sessionReconciliationResult) {
	t.Helper()
	select {
	case <-result:
		t.Fatal("reconciliation returned before the status transaction ended")
	case <-time.After(50 * time.Millisecond):
	}
}

func awaitReconciliation(t *testing.T, result <-chan sessionReconciliationResult) sessionReconciliationResult {
	t.Helper()
	select {
	case current := <-result:
		return current
	case <-time.After(time.Second):
		t.Fatal("reconciliation did not return after the status transaction ended")
		return sessionReconciliationResult{}
	}
}

func TestSessionStatusProjectionAcknowledgesOnlyMatchingInternalOutboxEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedIntentPool(t, ctx)

	firstSession := insertProjectionTestSession(t, ctx, db)
	secondSession := insertProjectionTestSession(t, ctx, db)
	for _, sessionID := range []uuid.UUID{firstSession, secondSession} {
		if _, err := db.Exec(ctx, `
			UPDATE auth_sessions
			SET session_status = 'revoked', revoked_at = now(), revocation_reason = 'test'
			WHERE session_id = $1
		`, sessionID); err != nil {
			t.Fatalf("revoke session: %v", err)
		}
	}

	repository := NewSessionStatusProjectionRepository(db)
	claimed, err := repository.Claim(ctx, "worker-a", 1, time.Minute)
	if err != nil {
		t.Fatalf("claim projection: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed job count = %d, want 1", len(claimed))
	}
	claimedSession := claimed[0].SessionID
	otherSession := firstSession
	if otherSession == claimedSession {
		otherSession = secondSession
	}

	if err := repository.MarkDelivered(ctx, claimed[0].JobID, "worker-b"); !errors.Is(err, ErrSessionStatusProjectionLeaseLost) {
		t.Fatalf("wrong-owner MarkDelivered() error = %v", err)
	}
	assertStatusOutboxState(t, ctx, db, claimedSession, claimed[0].Version, "Auth.SessionStatusCacheUpdated", "pending")
	if err := repository.MarkDelivered(ctx, claimed[0].JobID, "worker-a"); err != nil {
		t.Fatalf("mark projection delivered: %v", err)
	}

	assertStatusOutboxState(t, ctx, db, claimedSession, claimed[0].Version, "Auth.SessionStatusCacheUpdated", "published")
	assertStatusOutboxState(t, ctx, db, claimedSession, claimed[0].Version, "Auth.SessionRevoked", "pending")
	assertStatusOutboxState(t, ctx, db, otherSession, 1, "Auth.SessionStatusCacheUpdated", "pending")
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

func assertStatusOutboxCounts(t *testing.T, ctx context.Context, db projectionQueryer, sessionID uuid.UUID, wantCache, wantRevoked int) {
	t.Helper()
	var cacheEvents, revokedEvents int
	if err := db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE event_type = 'Auth.SessionStatusCacheUpdated'),
			count(*) FILTER (WHERE event_type = 'Auth.SessionRevoked')
		FROM auth_outbox_events
		WHERE aggregate_type = 'Session' AND aggregate_id = $1
	`, sessionID).Scan(&cacheEvents, &revokedEvents); err != nil {
		t.Fatalf("count session status outbox events: %v", err)
	}
	if cacheEvents != wantCache || revokedEvents != wantRevoked {
		t.Fatalf("status outbox counts = (%d, %d), want (%d, %d)", cacheEvents, revokedEvents, wantCache, wantRevoked)
	}
}

func assertStatusOutboxState(t *testing.T, ctx context.Context, db projectionQueryer, sessionID uuid.UUID, version int64, eventType, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(ctx, `
		SELECT publish_status
		FROM auth_outbox_events
		WHERE aggregate_type = 'Session' AND aggregate_id = $1
		  AND aggregate_version = $2 AND event_type = $3
	`, sessionID, version, eventType).Scan(&got); err != nil {
		t.Fatalf("read session status outbox event: %v", err)
	}
	if got != want {
		t.Fatalf("status outbox event %s = %q, want %q", eventType, got, want)
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
