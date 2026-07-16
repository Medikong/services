//go:build integration

package integration_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	appstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/Medikong/services/services/auth-service/internal/security"
)

type allowAuthorizationDecision struct{}

func (allowAuthorizationDecision) Verify(context.Context, string, string, string, string) error {
	return nil
}

func TestApplyUserAccountStatusRevokesTargetSessionsAndReplays(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	keys := integrationSecurityKeys(t)
	targetUserID, targetIdentityID, targetLinkID := uuid.New(), uuid.New(), uuid.New()
	seedRefreshPrincipal(t, ctx, db, targetUserID, targetIdentityID, targetLinkID)
	targetSession := issueIntegrationSession(t, ctx, db, keys, targetUserID, targetIdentityID, targetLinkID)

	operatorUserID, operatorIdentityID, operatorLinkID := uuid.New(), uuid.New(), uuid.New()
	seedRefreshPrincipal(t, ctx, db, operatorUserID, operatorIdentityID, operatorLinkID)
	operatorSession := issueIntegrationSession(t, ctx, db, keys, operatorUserID, operatorIdentityID, operatorLinkID)
	principal := appsession.Principal{
		Authenticated: true, UserID: operatorUserID, SessionID: uuid.MustParse(operatorSession.SessionID),
		Method: "email_password", AuthenticatedAt: time.Now().UTC(),
	}
	verifier, err := security.NewUserProofVerifier("user-service", "user-local-1", integrationUserProofPublicKey(), 30*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := appstate.NewService(
		db, appstate.NewPostgresRepository(db), appsession.NewPostgresRepository(db), verifier,
		allowAuthorizationDecision{}, appstate.Config{StrongAuthTTL: 5 * time.Minute},
	)
	proof := signUserStatusProof(t, targetUserID, uuid.NewString(), "restricted", 2, time.Now().UTC())
	input := appstate.ApplyInput{Principal: principal, PathUserID: targetUserID.String(), UserStatusChangeProof: proof, AuthorizationDecision: "allow"}
	result, err := service.Apply(ctx, input)
	if err != nil {
		t.Fatalf("apply user status: %v", err)
	}
	if !result.Applied || result.AccountStatus != appstate.StatusRestricted || result.UserVersion != 2 {
		t.Fatalf("unexpected apply result: %#v", result)
	}
	var sessionStatus, credentialStatus string
	if err := db.QueryRow(ctx, `SELECT session_status FROM auth_sessions WHERE session_id=$1`, targetSession.SessionID).Scan(&sessionStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT credential_status FROM auth_session_credentials WHERE session_id=$1`, targetSession.SessionID).Scan(&credentialStatus); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "revoked" || credentialStatus != "revoked" {
		t.Fatalf("target session=%q credential=%q", sessionStatus, credentialStatus)
	}
	replayed, err := service.Apply(ctx, input)
	if err != nil || !replayed.Applied || replayed.UserVersion != 2 {
		t.Fatalf("replay result=%#v err=%v", replayed, err)
	}
	conflicting := input
	conflicting.UserStatusChangeProof = signUserStatusProof(t, targetUserID, uuid.NewString(), "deactivated", 2, time.Now().UTC())
	if _, err := service.Apply(ctx, conflicting); errorCode(err) != "AUTH_RESOURCE_PRECONDITION_FAILED" {
		t.Fatalf("same-version conflict error=%v", err)
	}
}

func issueIntegrationSession(t *testing.T, ctx context.Context, db *pgxpool.Pool, keys security.Keys, userID, identityID, linkID uuid.UUID) appsession.Issued {
	t.Helper()
	sessions := appsession.NewPostgresRepository(db)
	service := appsession.NewService(
		db, keys,
		appsession.Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, SessionTTL: time.Hour, RecoveryTTL: time.Minute},
		sessions, appstate.NewPostgresRepository(db), idempotency.NewPostgresRepository(db), outbox.NewPostgresRepository(db),
	)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueTx(ctx, tx, appsession.IssueInput{
		UserID: userID, IdentityID: identityID, IdentityLink: linkID,
		Method: "email_password", Channel: "ios",
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return issued
}

func signUserStatusProof(t *testing.T, userID uuid.UUID, changeID, status string, version int64, changedAt time.Time) string {
	t.Helper()
	seed := sha256.Sum256([]byte("dropmong-user-outgoing-proof"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	now := time.Now().UTC()
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "user-local-1"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(security.UserStatusProof{
		Issuer: "user-service", Audience: "auth-service", Purpose: "apply_user_status",
		StatusChangeID: changeID, UserID: userID.String(), AccountStatus: status,
		UserVersion: version, ChangedAt: changedAt.Unix(), IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix(), Nonce: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(unsigned)))
}

func signUserCreationProof(t *testing.T, registrationID string, userID uuid.UUID, version int64) string {
	t.Helper()
	seed := sha256.Sum256([]byte("dropmong-user-outgoing-proof"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	now := time.Now().UTC()
	header, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "user-local-1"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(security.UserProofClaims{
		Issuer: "user-service", Audience: "auth-service", Purpose: "complete_registration",
		RegistrationID: registrationID, UserID: userID.String(), UserVersion: version,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(5 * time.Minute).Unix(), Nonce: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, []byte(unsigned)))
}
