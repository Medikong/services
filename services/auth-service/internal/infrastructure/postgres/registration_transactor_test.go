//go:build integration

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	applicationregistration "github.com/Medikong/services/services/auth-service/internal/application/registration"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRegistrationTransactorCommitsAndRollsBackAtomicBundle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db := migratedIntentPool(t, ctx)
	transactor := NewRegistrationTransactor(db, false)

	committedID := uuid.New()
	if err := transactor.WithinTransaction(ctx, func(repositories applicationregistration.TxRepositories) error {
		return persistRegistrationBundle(ctx, repositories, committedID, "registration-commit")
	}); err != nil {
		t.Fatalf("commit registration bundle: %v", err)
	}
	assertRegistrationBundleCount(t, ctx, db, committedID, "registration-commit", 1)

	rolledBackID := uuid.New()
	rollbackErr := errors.New("force registration rollback")
	err := transactor.WithinTransaction(ctx, func(repositories applicationregistration.TxRepositories) error {
		if persistErr := persistRegistrationBundle(ctx, repositories, rolledBackID, "registration-rollback"); persistErr != nil {
			return persistErr
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("rollback error = %v", err)
	}
	assertRegistrationBundleCount(t, ctx, db, rolledBackID, "registration-rollback", 0)
}

func persistRegistrationBundle(ctx context.Context, repositories applicationregistration.TxRepositories, registrationID uuid.UUID, key string) error {
	now := time.Now().UTC()
	intentID, emailID, phoneID, userID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := repositories.Intents.(*IntentRepository).Create(ctx, domainintent.CreateParams{
		ID: intentID, Channel: domainintent.ChannelIOS, ReturnPath: "/", Type: "authenticate",
		OwnerProofHash: registrationTestHash(intentID.String(), "owner"), ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		return err
	}
	if err := repositories.Identities.Reserve(ctx, domainidentity.Identity{ID: emailID, Type: domainidentity.TypeEmail, NormalizedValue: key + "@example.test", MaskedValue: "r***@example.test"}); err != nil {
		return err
	}
	if err := repositories.Identities.Reserve(ctx, domainidentity.Identity{ID: phoneID, Type: domainidentity.TypePhone, NormalizedValue: "+82010" + registrationID.String()[:8], MaskedValue: "+82********10"}); err != nil {
		return err
	}
	linkID := uuid.New()
	if err := repositories.Identities.CreateActiveLink(ctx, domainidentity.Link{ID: linkID, Identity: emailID, UserID: userID, Type: domainidentity.TypeEmail}); err != nil {
		return err
	}
	if err := repositories.UserAuthState.CreateActiveForRegistration(ctx, userID, 1, key); err != nil {
		return err
	}
	registration, err := domainregistration.New(domainregistration.NewInput{
		ID: registrationID, IntentID: intentID, EmailIdentityID: emailID, PhoneIdentityID: phoneID,
		ProfileRequestID: "profile-" + key, AgreementReceiptID: "agreement-" + key,
		ClientChannel: "ios", StatusTokenHash: registrationTestHash(registrationID.String(), "status"),
		StatusTokenKeyVer: 1, StatusTokenExpires: now.Add(2 * time.Hour), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	})
	if err != nil {
		return err
	}
	if err := repositories.Registrations.Create(ctx, registration); err != nil {
		return err
	}
	sessionID := uuid.New()
	if err := repositories.Session.Sessions.Create(ctx, domainsession.CreateParams{
		Session: domainsession.Session{
			ID: sessionID, UserID: userID, IdentityID: emailID, IdentityLink: linkID,
			Method: "registration_verified", Channel: domainsession.ChannelIOS, ExpiresAt: now.Add(time.Hour),
		},
		Credential: domainsession.Credential{
			ID: uuid.New(), SessionID: sessionID, Type: "mobile_refresh_token",
			SecretHash: registrationTestHash(sessionID.String(), "refresh"), FamilyID: uuidPointer(uuid.New()), ExpiresAt: now.Add(time.Hour),
		},
	}); err != nil {
		return err
	}
	if err := repositories.Idempotency.CreateCompleted(ctx, domainidempotency.NewRecord(
		"start_registration", registrationTestHash(registrationID.String(), "scope"), registrationTestHash(key),
		registrationTestHash(registrationID.String(), "request"), &registrationID, nil, now.Add(time.Hour),
	), "Registration", "created"); err != nil {
		return err
	}
	if err := repositories.Outbox.Append(ctx, domainoutbox.Event{
		ID: uuid.New(), Type: "Auth.RegistrationStarted", AggregateType: "Registration", AggregateID: registrationID,
		Version: registration.Version, Payload: json.RawMessage(`{"registrationId":"test"}`), CorrelationID: intentID,
	}); err != nil {
		return err
	}
	return repositories.Audit.Append(ctx, "auth.registration.tested", "authentication_intent", intentID, registrationID, map[string]string{"status": "created"}, key)
}

func assertRegistrationBundleCount(t *testing.T, ctx context.Context, db *pgxpool.Pool, registrationID uuid.UUID, key string, want int) {
	t.Helper()
	queries := []struct {
		name  string
		query string
		arg   any
	}{
		{name: "registration", query: `SELECT count(*) FROM auth_registrations WHERE registration_id=$1`, arg: registrationID},
		{name: "session", query: `SELECT count(*) FROM auth_sessions s JOIN auth_identity_links l ON l.identity_link_id=s.identity_link_id WHERE l.user_id IN (SELECT user_id FROM auth_identity_links WHERE identity_id=(SELECT email_identity_id FROM auth_registrations WHERE registration_id=$1))`, arg: registrationID},
		{name: "idempotency", query: `SELECT count(*) FROM auth_idempotency_records WHERE resource_id=$1`, arg: registrationID},
		{name: "outbox", query: `SELECT count(*) FROM auth_outbox_events WHERE aggregate_id=$1`, arg: registrationID},
		{name: "audit", query: `SELECT count(*) FROM audit_outbox WHERE idempotency_key=$1`, arg: key},
	}
	for _, item := range queries {
		var got int
		if err := db.QueryRow(ctx, item.query, item.arg).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", item.name, err)
		}
		if got != want {
			t.Fatalf("%s count = %d, want %d", item.name, got, want)
		}
	}
}

func registrationTestHash(values ...string) []byte {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hash.Sum(nil)
}

func uuidPointer(value uuid.UUID) *uuid.UUID {
	return &value
}
