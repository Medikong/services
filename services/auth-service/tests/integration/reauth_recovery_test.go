//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/reauth"
	appsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	appstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReauthenticationKeepsSessionAndRecoversOnlyExactWebDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	userID, emailID, emailLinkID := uuid.New(), uuid.New(), uuid.New()
	password := "correct horse battery staple"
	seedEmailPrincipal(t, ctx, db, userID, emailID, emailLinkID, password)
	keys := testOperatorKeys(t)
	sessionRepository := appsession.NewPostgresRepository(db)
	sessionService := appsession.NewService(db, keys, appsession.Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, SessionTTL: time.Hour, RecoveryTTL: 5 * time.Minute}, sessionRepository, appstate.NewPostgresRepository(db), idempotency.NewPostgresRepository(db), outbox.NewPostgresRepository(db))
	tx := beginDomainTx(t, ctx, db)
	initial, err := sessionService.IssueTx(ctx, tx, appsession.IssueInput{UserID: userID, IdentityID: emailID, IdentityLink: emailLinkID, Method: "registration_verified", Channel: "web", WebCSRFToken: "test-csrf-token"})
	if err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("issue initial web session: %v", err)
	}
	commitDomainTx(t, ctx, tx)
	sessionID, err := uuid.Parse(initial.SessionID)
	if err != nil {
		t.Fatalf("parse session ID: %v", err)
	}
	service := reauth.NewReauthService(db, keys, identity.NewPostgresRepository(db), reauth.NewPostgresRepository(db), sessionRepository, idempotency.NewPostgresRepository(db), sessionService, 5*time.Minute, 5*time.Minute)
	principal := appsession.Principal{Authenticated: true, SessionID: sessionID, UserID: userID, Channel: "web", Method: "registration_verified"}
	key := uuid.NewString()
	result, err := service.Reauthenticate(ctx, identity.ReauthInput{Principal: principal, Purpose: "replace_phone", Password: password, IdempotencyKey: key, PreviousWebCookie: initial.WebCookie})
	if err != nil {
		t.Fatalf("reauthenticate: %v", err)
	}
	if result.Issued.SessionID != initial.SessionID || result.Issued.WebCookie == "" || result.Proof == "" {
		t.Fatal("reauthentication result is incomplete")
	}
	if _, err := sessionService.Authenticate(ctx, initial.WebCookie, ""); err == nil {
		t.Fatal("old web cookie unexpectedly authenticated outside recovery")
	}
	newPrincipal, err := sessionService.Authenticate(ctx, result.Issued.WebCookie, "")
	if err != nil {
		t.Fatalf("authenticate rotated web cookie: %v", err)
	}
	if newPrincipal.SessionID != sessionID || newPrincipal.Method != "email_password" {
		t.Fatalf("unexpected rebound principal: %#v", newPrincipal)
	}
	replayed, err := service.Reauthenticate(ctx, identity.ReauthInput{Principal: principal, Purpose: "replace_phone", Password: password, IdempotencyKey: key, PreviousWebCookie: initial.WebCookie})
	if err != nil {
		t.Fatalf("replay reauthentication: %v", err)
	}
	if replayed.Proof != result.Proof || replayed.Issued.WebCookie != result.Issued.WebCookie {
		t.Fatal("same reauthentication key did not replay the original delivery")
	}
	recovered, err := service.RecoverWebDelivery(ctx, initial.WebCookie, initial.CSRFToken, "replace_phone", password, key)
	if err != nil {
		t.Fatalf("recover lost web delivery: %v", err)
	}
	if recovered.Proof != result.Proof || recovered.Issued.WebCookie != result.Issued.WebCookie {
		t.Fatal("recovery did not return the original reauthentication delivery")
	}
	if _, err := service.RecoverWebDelivery(ctx, initial.WebCookie, initial.CSRFToken, "replace_phone", password, uuid.NewString()); errorCode(err) != "AUTH_SESSION_REQUIRED" {
		t.Fatalf("wrong recovery key error=%v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE auth_session_credentials SET delivery_recovery_expires_at = now() - interval '1 second' WHERE secret_hash = $1`, keys.Hash(initial.WebCookie)); err != nil {
		t.Fatalf("expire reauthentication delivery: %v", err)
	}
	if _, err := service.RecoverWebDelivery(ctx, initial.WebCookie, initial.CSRFToken, "replace_phone", password, key); errorCode(err) != "AUTH_SESSION_DELIVERY_EXPIRED" {
		t.Fatalf("expired recovery error=%v", err)
	}
}

func TestMethodLinkStartReplaysBeforeConsumingProofAgain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	userID, emailID, emailLinkID := uuid.New(), uuid.New(), uuid.New()
	password := "correct horse battery staple"
	seedEmailPrincipal(t, ctx, db, userID, emailID, emailLinkID, password)
	keys := testOperatorKeys(t)
	sessionRepository := appsession.NewPostgresRepository(db)
	idempotencyRepository := idempotency.NewPostgresRepository(db)
	sessionService := appsession.NewService(db, keys, appsession.Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, SessionTTL: time.Hour, RecoveryTTL: 5 * time.Minute}, sessionRepository, appstate.NewPostgresRepository(db), idempotencyRepository, outbox.NewPostgresRepository(db))
	tx := beginDomainTx(t, ctx, db)
	initial, err := sessionService.IssueTx(ctx, tx, appsession.IssueInput{UserID: userID, IdentityID: emailID, IdentityLink: emailLinkID, Method: "registration_verified", Channel: "web", WebCSRFToken: "test-csrf-token"})
	if err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("issue session: %v", err)
	}
	commitDomainTx(t, ctx, tx)
	sessionID, err := uuid.Parse(initial.SessionID)
	if err != nil {
		t.Fatalf("parse session ID: %v", err)
	}
	principal := appsession.Principal{Authenticated: true, SessionID: sessionID, UserID: userID, Channel: "web", Method: "registration_verified"}
	reauthService := reauth.NewReauthService(db, keys, identity.NewPostgresRepository(db), reauth.NewPostgresRepository(db), sessionRepository, idempotencyRepository, sessionService, 5*time.Minute, 5*time.Minute)
	linkService := identity.NewLinkService(db, keys, reauthService, identity.NewPostgresRepository(db), challenge.NewPostgresRepository(db, challenge.PostgresOptions{}), sessionRepository, sessionService, idempotencyRepository, outbox.NewPostgresRepository(db), false, 10*time.Minute, 5*time.Minute)
	reauthenticated, err := reauthService.Reauthenticate(ctx, identity.ReauthInput{Principal: principal, Purpose: "link_identity", Password: password, IdempotencyKey: uuid.NewString(), PreviousWebCookie: initial.WebCookie})
	if err != nil {
		t.Fatalf("reauthenticate for link: %v", err)
	}
	key := uuid.NewString()
	input := identity.StartLinkInput{Principal: principal, Phone: "+821098765432", Proof: reauthenticated.Proof, IdempotencyKey: key}
	started, err := linkService.StartLink(ctx, input)
	if err != nil {
		t.Fatalf("start method link: %v", err)
	}
	replayed, err := linkService.StartLink(ctx, input)
	if err != nil {
		t.Fatalf("replay method link start: %v", err)
	}
	if replayed.LinkID != started.LinkID || !replayed.ExpiresAt.Equal(started.ExpiresAt) || replayed.Existing {
		t.Fatalf("method link start replay=%#v first=%#v", replayed, started)
	}
	_, err = linkService.StartLink(ctx, identity.StartLinkInput{Principal: principal, Phone: "+821011112222", Proof: reauthenticated.Proof, IdempotencyKey: key})
	if errorCode(err) != "AUTH_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("method link start conflict error=%v", err)
	}
}

func TestPhoneReplacementKeepsReauthenticatedSessionAndReplaysDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	userID, emailID, emailLinkID := uuid.New(), uuid.New(), uuid.New()
	password := "correct horse battery staple"
	seedEmailPrincipal(t, ctx, db, userID, emailID, emailLinkID, password)
	phoneID, phoneLinkID := uuid.New(), uuid.New()
	seedPhoneLink(t, ctx, db, userID, phoneID, phoneLinkID, "+821012345678")
	keys := testOperatorKeys(t)
	keys.VirtualKey = []byte("01234567890123456789012345678901")
	sessionRepository := appsession.NewPostgresRepository(db)
	idempotencyRepository := idempotency.NewPostgresRepository(db)
	sessionService := appsession.NewService(db, keys, appsession.Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, SessionTTL: time.Hour, RecoveryTTL: 5 * time.Minute}, sessionRepository, appstate.NewPostgresRepository(db), idempotencyRepository, outbox.NewPostgresRepository(db))
	tx := beginDomainTx(t, ctx, db)
	initial, err := sessionService.IssueTx(ctx, tx, appsession.IssueInput{UserID: userID, IdentityID: emailID, IdentityLink: emailLinkID, Method: "registration_verified", Channel: "web", WebCSRFToken: "test-csrf-token"})
	if err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("issue initial session: %v", err)
	}
	other, err := sessionService.IssueTx(ctx, tx, appsession.IssueInput{UserID: userID, IdentityID: phoneID, IdentityLink: phoneLinkID, Method: "phone_otp", Channel: "ios"})
	if err != nil {
		rollbackDomainTx(ctx, tx)
		t.Fatalf("issue old phone session: %v", err)
	}
	commitDomainTx(t, ctx, tx)
	sessionID, err := uuid.Parse(initial.SessionID)
	if err != nil {
		t.Fatalf("parse initial session: %v", err)
	}
	otherSessionID, err := uuid.Parse(other.SessionID)
	if err != nil {
		t.Fatalf("parse other session: %v", err)
	}
	identityRepository := identity.NewPostgresRepository(db)
	challengeRepository := challenge.NewPostgresRepository(db, challenge.PostgresOptions{VirtualProjectionEnabled: true})
	reauthService := reauth.NewReauthService(db, keys, identityRepository, reauth.NewPostgresRepository(db), sessionRepository, idempotencyRepository, sessionService, 5*time.Minute, 5*time.Minute)
	linkService := identity.NewLinkService(db, keys, reauthService, identityRepository, challengeRepository, sessionRepository, sessionService, idempotencyRepository, outbox.NewPostgresRepository(db), true, 10*time.Minute, 5*time.Minute)
	principal := appsession.Principal{Authenticated: true, SessionID: sessionID, UserID: userID, Channel: "web", Method: "registration_verified"}
	reauthKey := uuid.NewString()
	reauthenticated, err := reauthService.Reauthenticate(ctx, identity.ReauthInput{Principal: principal, Purpose: "replace_phone", Password: password, IdempotencyKey: reauthKey, PreviousWebCookie: initial.WebCookie})
	if err != nil {
		t.Fatalf("reauthenticate before phone replacement: %v", err)
	}
	startKey := uuid.NewString()
	startInput := identity.ReplacementInput{Principal: principal, Phone: "+821098765432", Proof: reauthenticated.Proof, IdempotencyKey: startKey}
	started, err := linkService.StartReplacement(ctx, startInput)
	if err != nil {
		t.Fatalf("start replacement: %v", err)
	}
	startReplay, err := linkService.StartReplacement(ctx, startInput)
	if err != nil {
		t.Fatalf("replay replacement start: %v", err)
	}
	if startReplay.LinkID != started.LinkID || !startReplay.ExpiresAt.Equal(started.ExpiresAt) {
		t.Fatalf("same start key did not replay the original link: %#v != %#v", startReplay, started)
	}
	_, err = linkService.StartReplacement(ctx, identity.ReplacementInput{Principal: principal, Phone: "+821011112222", Proof: reauthenticated.Proof, IdempotencyKey: startKey})
	if errorCode(err) != "AUTH_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("replacement start conflict error=%v", err)
	}
	issued, err := linkService.IssuePhoneReplacement(ctx, identity.IssueLinkInput{Principal: principal, LinkID: started.LinkID, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatalf("issue replacement challenge: %v", err)
	}
	challengeID, err := uuid.Parse(issued.ChallengeID)
	if err != nil {
		t.Fatalf("parse challenge: %v", err)
	}
	tx = beginDomainTx(t, ctx, db)
	projection, err := challengeRepository.FindVirtualProjection(ctx, tx, challengeID, time.Now().UTC())
	rollbackDomainTx(ctx, tx)
	if err != nil {
		t.Fatalf("read virtual replacement code: %v", err)
	}
	var message map[string]string
	if err := keys.OpenVirtual(projection.CodeCiphertext, &message); err != nil {
		t.Fatalf("decrypt virtual replacement code: %v", err)
	}
	code := message["code"]
	if len(code) != 6 {
		t.Fatal("invalid virtual code length")
	}
	key := uuid.NewString()
	completed, err := linkService.CompletePhoneReplacement(ctx, identity.CompleteLinkInput{Principal: principal, LinkID: started.LinkID, ChallengeID: issued.ChallengeID, Code: code, IdempotencyKey: key, PreviousWebCookie: reauthenticated.Issued.WebCookie})
	if err != nil {
		t.Fatalf("complete replacement: %v", err)
	}
	if completed.Issued.SessionID != initial.SessionID || completed.Issued.WebCookie == "" {
		t.Fatal("replacement credential delivery is incomplete")
	}
	if _, err := sessionService.Authenticate(ctx, reauthenticated.Issued.WebCookie, ""); err == nil {
		t.Fatal("pre-replacement cookie unexpectedly authenticated outside recovery")
	}
	if current, err := sessionService.Authenticate(ctx, completed.Issued.WebCookie, ""); err != nil || current.SessionID != sessionID || current.Method != "email_password" {
		t.Fatalf("current rebound session=%#v err=%v", current, err)
	}
	replayed, err := linkService.CompletePhoneReplacement(ctx, identity.CompleteLinkInput{Principal: principal, LinkID: started.LinkID, ChallengeID: issued.ChallengeID, Code: code, IdempotencyKey: key, PreviousWebCookie: reauthenticated.Issued.WebCookie})
	if err != nil {
		t.Fatalf("replay replacement: %v", err)
	}
	if replayed.Issued.WebCookie != completed.Issued.WebCookie || replayed.LinkID != completed.LinkID {
		t.Fatal("same replacement key did not replay the original delivery")
	}
	recovered, err := linkService.RecoverPhoneReplacementWebDelivery(ctx, reauthenticated.Issued.WebCookie, initial.CSRFToken, started.LinkID, issued.ChallengeID, code, key)
	if err != nil {
		t.Fatalf("recover replacement delivery: %v", err)
	}
	if recovered.Issued.WebCookie != completed.Issued.WebCookie || recovered.LinkID != completed.LinkID {
		t.Fatal("recovered replacement delivery did not match original")
	}
	var currentStatus, otherStatus, oldLinkStatus, newLinkStatus string
	if err := db.QueryRow(ctx, `SELECT session_status FROM auth_sessions WHERE session_id=$1`, sessionID).Scan(&currentStatus); err != nil {
		t.Fatalf("read current session: %v", err)
	}
	if err := db.QueryRow(ctx, `SELECT session_status FROM auth_sessions WHERE session_id=$1`, otherSessionID).Scan(&otherStatus); err != nil {
		t.Fatalf("read old phone session: %v", err)
	}
	if err := db.QueryRow(ctx, `SELECT link_status FROM auth_identity_links WHERE identity_link_id=$1`, phoneLinkID).Scan(&oldLinkStatus); err != nil {
		t.Fatalf("read old link: %v", err)
	}
	if err := db.QueryRow(ctx, `SELECT link_status FROM auth_identity_links WHERE identity_link_id=$1`, started.LinkID).Scan(&newLinkStatus); err != nil {
		t.Fatalf("read new link: %v", err)
	}
	if currentStatus != "active" || otherStatus != "revoked" || oldLinkStatus != "replaced" || newLinkStatus != "active" {
		t.Fatalf("replacement states current=%s other=%s oldLink=%s newLink=%s", currentStatus, otherStatus, oldLinkStatus, newLinkStatus)
	}
}

func seedEmailPrincipal(t *testing.T, ctx context.Context, db *pgxpool.Pool, userID, identityID, linkID uuid.UUID, password string) {
	t.Helper()
	hash, err := security.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_identities (
			identity_id, identity_type, identity_namespace, normalized_value, masked_value,
			status, verification_status, credential_status, verified_at
		) VALUES ($1, 'email', 'default', $2, 'a***@example.test', 'verified', 'verified', 'active', now())
	`, identityID, "email-"+identityID.String()+"@example.test"); err != nil {
		t.Fatalf("seed email identity: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_identity_links (
			identity_link_id, identity_id, identity_type, user_id, link_status, link_reason, activated_at
		) VALUES ($1, $2, 'email', $3, 'active', 'signup', now())
	`, linkID, identityID, userID); err != nil {
		t.Fatalf("seed email link: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_password_credentials (password_credential_id, identity_id, password_hash, password_status, hash_algorithm, created_at, updated_at)
		VALUES ($1, $2, $3, 'active', 'bcrypt', now(), now())
	`, uuid.New(), identityID, hash); err != nil {
		t.Fatalf("seed password credential: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO auth_user_auth_states (user_id,status,user_version,status_change_id,effective_at) VALUES ($1,'active',1,$2,now())`, userID, uuid.NewString()); err != nil {
		t.Fatalf("seed email state: %v", err)
	}
}

func seedPhoneLink(t *testing.T, ctx context.Context, db *pgxpool.Pool, userID, identityID, linkID uuid.UUID, phone string) {
	t.Helper()
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_identities (
			identity_id, identity_type, identity_namespace, normalized_value, masked_value,
			status, verification_status, credential_status, verified_at
		) VALUES ($1, 'phone', 'default', $2, '***-**-5678', 'verified', 'verified', 'active', now())
	`, identityID, phone); err != nil {
		t.Fatalf("seed phone identity: %v", err)
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO auth_identity_links (
			identity_link_id, identity_id, identity_type, user_id, link_status, link_reason, activated_at
		) VALUES ($1, $2, 'phone', $3, 'active', 'signup', now())
	`, linkID, identityID, userID); err != nil {
		t.Fatalf("seed phone link: %v", err)
	}
}
