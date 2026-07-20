//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/clock"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/cryptography"
	postgresinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRefreshReuseRevokesSessionAndRefreshFamily(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	userID, identityID, linkID := uuid.New(), uuid.New(), uuid.New()
	seedRefreshPrincipal(t, ctx, db, userID, identityID, linkID)

	keys := integrationSecurityKeys(t)
	repository := postgresinfra.NewSessionRepository(db)
	service := appsession.NewService(
		postgresinfra.NewSessionTransactor(db),
		cryptography.NewSession(keys),
		clock.System{},
		appsession.Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, SessionTTL: time.Hour, RecoveryTTL: 5 * time.Minute},
		repository,
	)
	issued, err := service.Issue(ctx, appsession.IssueInput{UserID: userID, IdentityID: identityID, IdentityLink: linkID, Method: "phone_otp", Channel: "ios"})
	if err != nil {
		t.Fatalf("issue mobile session: %v", err)
	}

	firstKey := uuid.NewString()
	first, err := service.Refresh(ctx, issued.RefreshToken, "", firstKey)
	if err != nil {
		t.Fatalf("rotate refresh credential: %v", err)
	}
	replayed, err := service.Refresh(ctx, issued.RefreshToken, "", firstKey)
	if err != nil {
		t.Fatalf("replay rotated refresh credential: %v", err)
	}
	if replayed.AccessToken != first.AccessToken || replayed.RefreshToken != first.RefreshToken || replayed.SessionID != first.SessionID {
		t.Fatal("same idempotency key did not return the first refresh response")
	}
	var replayCount int
	if err := db.QueryRow(ctx, `SELECT replay_count FROM auth_idempotency_replay_payloads`).Scan(&replayCount); err != nil {
		t.Fatalf("read refresh replay count: %v", err)
	}
	if replayCount != 1 {
		t.Fatalf("replay count=%d, want 1", replayCount)
	}
	var rotationEvents int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_outbox_events WHERE event_type='Auth.SessionRefreshRotated'`).Scan(&rotationEvents); err != nil {
		t.Fatalf("count refresh outbox events: %v", err)
	}
	if rotationEvents != 1 {
		t.Fatalf("refresh outbox events=%d, want 1", rotationEvents)
	}
	var auditEvents int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM audit_outbox WHERE event_name='auth.session.refresh_rotated' AND idempotency_key=$1`, firstKey).Scan(&auditEvents); err != nil {
		t.Fatalf("count refresh audit events: %v", err)
	}
	if auditEvents != 1 {
		t.Fatalf("refresh audit events=%d, want 1", auditEvents)
	}
	if _, err := service.Refresh(ctx, issued.RefreshToken, "", uuid.NewString()); err == nil {
		t.Fatal("reused refresh token unexpectedly succeeded")
	}
	var status string
	if err := db.QueryRow(ctx, `SELECT session_status FROM auth_sessions WHERE session_id=$1`, issued.SessionID).Scan(&status); err != nil {
		t.Fatalf("read session status: %v", err)
	}
	if status != "reuse_detected" {
		t.Fatalf("session status=%q, want reuse_detected", status)
	}
	var activeCredentials int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_session_credentials WHERE session_id=$1 AND credential_status='active'`, issued.SessionID).Scan(&activeCredentials); err != nil {
		t.Fatalf("count active credentials: %v", err)
	}
	if activeCredentials != 0 {
		t.Fatalf("active credentials after reuse=%d, want 0", activeCredentials)
	}
}

func seedRefreshPrincipal(t *testing.T, ctx context.Context, db *pgxpool.Pool, userID, identityID, linkID uuid.UUID) {
	t.Helper()
	if _, err := db.Exec(ctx, `INSERT INTO auth_identities (identity_id,identity_type,identity_namespace,normalized_value,masked_value,status,verification_status,credential_status,verified_at) VALUES ($1,'phone','default',$2,'***','verified','verified','active',now())`, identityID, "phone-"+identityID.String()); err != nil {
		t.Fatalf("seed refresh principal: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO auth_identity_links (identity_link_id,identity_id,identity_type,user_id,link_status,link_reason,activated_at) VALUES ($1,$2,'phone',$3,'active','signup',now())`, linkID, identityID, userID); err != nil {
		t.Fatalf("seed refresh link: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO auth_user_auth_states (user_id,status,user_version,status_change_id,effective_at) VALUES ($1,'active',1,$2,now())`, userID, uuid.NewString()); err != nil {
		t.Fatalf("seed refresh state: %v", err)
	}
}
