//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/bootstrap"
	appregistration "github.com/Medikong/services/services/auth-service/internal/application/registration"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/access"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/inbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	registrationdomain "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	sessiondomain "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
)

func TestUserLinkInboxCreatesLinksExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedDomainPool(t, ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	registrationID, intentID, emailID, phoneID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	tx := beginDomainTx(t, ctx, db)
	seedRegistrationDependencies(t, ctx, tx, intentID, emailID, phoneID, now)
	registration, err := registrationdomain.New(registrationdomain.NewInput{
		ID: registrationID, IntentID: intentID, EmailIdentityID: emailID, PhoneIdentityID: phoneID,
		ProfileRequestID: "profile-request", AgreementReceiptID: "agreement-receipt", ClientChannel: "web",
		StatusTokenHash: hash32(81), StatusTokenKeyVer: 1, StatusTokenExpires: now.Add(2 * time.Hour), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("new registration: %v", err)
	}
	emailChallengeID, phoneChallengeID := uuid.New(), uuid.New()
	if err := registration.AttachChallenge(registrationdomain.MethodEmail, emailChallengeID); err != nil {
		t.Fatalf("attach email challenge: %v", err)
	}
	if err := registration.AttachChallenge(registrationdomain.MethodPhone, phoneChallengeID); err != nil {
		t.Fatalf("attach phone challenge: %v", err)
	}
	if err := registration.MarkMethodVerified(registrationdomain.MethodEmail); err != nil {
		t.Fatalf("mark email verified: %v", err)
	}
	if err := registration.MarkMethodVerified(registrationdomain.MethodPhone); err != nil {
		t.Fatalf("mark phone verified: %v", err)
	}
	completionID := uuid.New()
	completionRecord := idempotency.NewRecord(
		"complete_registration",
		hash32(83),
		hash32(84),
		hash32(85),
		&registrationID,
		nil,
		now.Add(10*time.Minute),
	)
	completionRecord.ID = completionID
	if err := idempotency.NewPostgresRepository(db).CreateCompleted(ctx, tx, completionRecord, "Registration", "awaiting_user_link"); err != nil {
		t.Fatalf("seed completion idempotency record: %v", err)
	}
	if err := registration.MarkVerificationCompleted(registrationdomain.VerificationCompletion{
		EmailChallengeID: emailChallengeID, PhoneChallengeID: phoneChallengeID, EmailVerified: true, PhoneVerified: true,
		BindingID: uuid.New(), RegistrationVersion: 1, SnapshotHash: hash32(82), VerificationCompletedEvent: uuid.New(), CompletionIdempotencyID: completionID, LinkAcceptUntil: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("mark verification completed: %v", err)
	}
	if err := registrationdomain.NewPostgresRepository(db).Create(ctx, tx, registration); err != nil {
		t.Fatalf("create registration: %v", err)
	}
	commitDomainTx(t, ctx, tx)

	keys := security.Keys{CredentialHMAC: []byte("01234567890123456789012345678901"), ReplayKey: []byte("01234567890123456789012345678901"), JWTKey: []byte("01234567890123456789012345678901"), JWTIssuer: "integration"}
	intents := intent.NewPostgresRepository(db)
	idempotencyRepository := idempotency.NewPostgresRepository(db)
	bootstrapService := bootstrap.NewService(db, keys, bootstrap.Config{IntentTTL: time.Hour}, intents, idempotencyRepository)
	identityRepository := identity.NewPostgresRepository(db)
	outboxRepository := outbox.NewPostgresRepository(db)
	sessionService := appsession.NewService(db, keys, appsession.Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, SessionTTL: time.Hour, RecoveryTTL: time.Minute}, sessiondomain.NewPostgresRepository(db), access.NewPostgresRepository(db), idempotencyRepository, outboxRepository)
	service := appregistration.NewService(db, keys, appregistration.Config{SessionDeliveryWindow: 10 * time.Minute}, bootstrapService, registrationdomain.NewPostgresRepository(db), challenge.NewPostgresRepository(db, challenge.PostgresOptions{}), identityRepository, idempotencyRepository, inbox.NewPostgresRepository(db), outboxRepository, access.NewPostgresRepository(db), intents, sessionService)

	event := appregistration.UserLinkEvent{SourceEventID: uuid.New(), CausationID: uuid.New(), RegistrationID: registrationID, UserID: uuid.New(), LinkRequestID: uuid.New()}
	if err := service.ConsumeUserLinkEvent(ctx, event); err != nil {
		t.Fatalf("consume user link event: %v", err)
	}
	if err := service.ConsumeUserLinkEvent(ctx, event); err != nil {
		t.Fatalf("consume duplicate user link event: %v", err)
	}
	var status string
	var linkedUserID uuid.UUID
	if err := db.QueryRow(ctx, `SELECT status, user_id FROM auth_registrations WHERE registration_id=$1`, registrationID).Scan(&status, &linkedUserID); err != nil {
		t.Fatalf("read linked registration: %v", err)
	}
	if status != string(registrationdomain.StatusLinked) || linkedUserID != event.UserID {
		t.Fatalf("registration status=%q user=%s", status, linkedUserID)
	}
	var links int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM auth_identity_links WHERE user_id=$1 AND link_status='active'`, event.UserID).Scan(&links); err != nil {
		t.Fatalf("count active identity links: %v", err)
	}
	if links != 2 {
		t.Fatalf("active links=%d, want 2", links)
	}
	var inboxStatus string
	if err := db.QueryRow(ctx, `SELECT process_status FROM auth_inbox_messages WHERE consumer_name='context_user_link' AND source_event_id=$1`, event.SourceEventID).Scan(&inboxStatus); err != nil {
		t.Fatalf("read inbox status: %v", err)
	}
	if inboxStatus != "processed" {
		t.Fatalf("inbox status=%q, want processed", inboxStatus)
	}
}
