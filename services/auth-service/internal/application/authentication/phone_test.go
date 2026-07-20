package authentication

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
)

func TestPhoneVerifyCommitsFailedAttemptBeforeReturningFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	intentID := uuid.New()
	challengeID := uuid.New()
	identityID := uuid.New()
	challenge, err := domainchallenge.New(domainchallenge.NewInput{
		ID: challengeID, SubjectType: domainchallenge.SubjectPhoneSignIn, SubjectID: intentID,
		Purpose: domainchallenge.PurposePhoneSignIn, Method: domainchallenge.MethodPhone,
		Channel: domainchallenge.ChannelSMSCode, Destination: "+821012345678",
		DestinationLookupHash: make([]byte, 32), IdentityID: &identityID, CodeHash: make([]byte, 32),
		VerifierKeyVersion: 1, MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(time.Minute),
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	intents := &authenticationIntentRepository{current: domainintent.Intent{ID: intentID, Channel: domainintent.ChannelIOS, ExpiresAt: now.Add(time.Hour)}}
	challenges := &authenticationChallengeRepository{current: challenge}
	transactions := &authenticationTransactor{repositories: TxRepositories{
		Intents: intents, Identities: authenticationIdentityRepository{}, Challenges: challenges,
		Outbox: authenticationOutbox{}, Audit: authenticationAudit{},
	}}
	service := NewPhoneService(transactions, authenticationOwnership{}, authenticationCryptography{}, authenticationClock{now: now}, nil, Config{})

	_, err = service.Verify(context.Background(), PhoneVerifyInput{
		IntentID: intentID.String(), ChallengeID: challengeID.String(), OwnerProof: "owner", Code: "000000",
	})
	var typed *failure.Error
	if !errors.As(err, &typed) || typed.Code != "AUTH_CHALLENGE_FAILED" || typed.Kind != failure.KindInvalid {
		t.Fatalf("Verify() error = %#v", err)
	}
	if transactions.callbackErr != nil {
		t.Fatalf("transaction callback error = %v, want commit", transactions.callbackErr)
	}
	if challenges.current.AttemptCount != 1 || challenges.current.Status != domainchallenge.StatusIssued {
		t.Fatalf("challenge after mismatch = %#v", challenges.current)
	}
}

type authenticationTransactor struct {
	repositories TxRepositories
	callbackErr  error
}

func (t *authenticationTransactor) WithinTransaction(ctx context.Context, run func(TxRepositories) error) error {
	t.callbackErr = run(t.repositories)
	return t.callbackErr
}

type authenticationIntentRepository struct {
	current domainintent.Intent
}

func (r *authenticationIntentRepository) FindActiveForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error) {
	return r.current, nil
}

func (*authenticationIntentRepository) SetRememberMe(context.Context, uuid.UUID, bool) error {
	return nil
}

func (*authenticationIntentRepository) Consume(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

type authenticationIdentityRepository struct{}

func (authenticationIdentityRepository) FindEmailCredentialForUpdate(context.Context, string) (domainidentity.Identity, domainidentity.Link, domainidentity.PasswordCredential, error) {
	return domainidentity.Identity{}, domainidentity.Link{}, domainidentity.PasswordCredential{}, domainidentity.ErrNotFound
}

func (authenticationIdentityRepository) FindActivePhoneLinkForUpdate(context.Context, string) (domainidentity.Identity, domainidentity.Link, error) {
	return domainidentity.Identity{}, domainidentity.Link{}, domainidentity.ErrNotFound
}

func (authenticationIdentityRepository) FindActiveLinkForIdentity(context.Context, uuid.UUID) (domainidentity.Link, error) {
	return domainidentity.Link{}, domainidentity.ErrNotFound
}

type authenticationChallengeRepository struct {
	current domainchallenge.Challenge
}

func (*authenticationChallengeRepository) Issue(context.Context, domainchallenge.Challenge) error {
	return nil
}

func (r *authenticationChallengeRepository) FindForUpdate(context.Context, uuid.UUID) (domainchallenge.Challenge, error) {
	return r.current, nil
}

func (r *authenticationChallengeRepository) Save(_ context.Context, challenge *domainchallenge.Challenge) error {
	r.current = *challenge
	return nil
}

func (*authenticationChallengeRepository) StoreDeliveryPayload(context.Context, domainchallenge.DeliveryPayload) error {
	return nil
}

func (*authenticationChallengeRepository) StoreVirtualProjection(context.Context, domainchallenge.VirtualProjection) error {
	return nil
}

type authenticationOwnership struct{}

func (authenticationOwnership) VerifyOwnership(current domainintent.Intent, _, _ string, _ bool) (domainintent.Intent, error) {
	return current, nil
}

type authenticationCryptography struct{}

func (authenticationCryptography) Hash(...string) []byte              { return make([]byte, 32) }
func (authenticationCryptography) Equal([]byte, ...string) bool       { return false }
func (authenticationCryptography) VerificationCode() (string, error)  { return "000000", nil }
func (authenticationCryptography) VerifyPassword(string, string) bool { return false }
func (authenticationCryptography) SealDelivery(string, string) ([]byte, error) {
	return []byte("sealed"), nil
}
func (authenticationCryptography) SealVirtualCode(string) ([]byte, error) {
	return []byte("sealed"), nil
}

type authenticationClock struct{ now time.Time }

func (c authenticationClock) Now() time.Time { return c.now }

type authenticationOutbox struct{}

func (authenticationOutbox) Append(context.Context, domainoutbox.Event) error { return nil }

type authenticationAudit struct{}

func (authenticationAudit) Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error {
	return nil
}
