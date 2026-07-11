package registration

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRegistrationVerificationLinkAndCompletion(t *testing.T) {
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	registration := mustRegistration(t, now)
	emailChallengeID := uuid.New()
	phoneChallengeID := uuid.New()
	if err := registration.AttachChallenge(MethodEmail, emailChallengeID); err != nil {
		t.Fatalf("attach email challenge: %v", err)
	}
	if err := registration.AttachChallenge(MethodPhone, phoneChallengeID); err != nil {
		t.Fatalf("attach phone challenge: %v", err)
	}
	completion := VerificationCompletion{
		EmailChallengeID:           emailChallengeID,
		PhoneChallengeID:           phoneChallengeID,
		EmailVerified:              true,
		PhoneVerified:              true,
		BindingID:                  uuid.New(),
		RegistrationVersion:        1,
		SnapshotHash:               bytes32(1),
		VerificationCompletedEvent: uuid.New(),
		CompletionIdempotencyID:    uuid.New(),
		LinkAcceptUntil:            now.Add(time.Hour),
	}
	if err := registration.MarkVerificationCompleted(completion); err != nil {
		t.Fatalf("mark verification: %v", err)
	}
	if registration.Status != StatusAwaitingUserLink || len(registration.VerifiedMethods) != 2 {
		t.Fatalf("registration after verification = %#v", registration)
	}
	if err := registration.MarkVerificationCompleted(completion); err != nil {
		t.Fatalf("same verification completion must be idempotent: %v", err)
	}

	link := UserLink{
		UserID:            uuid.New(),
		LinkRequestID:     uuid.New(),
		LinkedAt:          now.Add(30 * time.Minute),
		SessionIssueUntil: now.Add(90 * time.Minute),
	}
	if err := registration.Link(link); err != nil {
		t.Fatalf("link registration: %v", err)
	}
	if err := registration.BeginSessionIssuance(now.Add(31 * time.Minute)); err != nil {
		t.Fatalf("begin session issuance: %v", err)
	}
	sessionID := uuid.New()
	if err := registration.Complete(sessionID, now.Add(32*time.Minute)); err != nil {
		t.Fatalf("complete registration: %v", err)
	}
	if registration.Status != StatusCompleted || registration.SessionID == nil || *registration.SessionID != sessionID {
		t.Fatalf("registration after completion = %#v", registration)
	}
}

func TestRegistrationDoesNotLinkBeforeBothVerifications(t *testing.T) {
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	registration := mustRegistration(t, now)
	if err := registration.AttachChallenge(MethodEmail, uuid.New()); err != nil {
		t.Fatal(err)
	}
	err := registration.MarkVerificationCompleted(VerificationCompletion{
		EmailVerified:              true,
		BindingID:                  uuid.New(),
		RegistrationVersion:        1,
		SnapshotHash:               bytes32(2),
		VerificationCompletedEvent: uuid.New(),
		CompletionIdempotencyID:    uuid.New(),
		LinkAcceptUntil:            now.Add(time.Hour),
	})
	if !errors.Is(err, ErrVerificationIncomplete) {
		t.Fatalf("mark incomplete verification error = %v", err)
	}
}

func TestRegistrationRejectsSessionAfterDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	registration := mustRegistration(t, now)
	emailChallengeID := uuid.New()
	phoneChallengeID := uuid.New()
	_ = registration.AttachChallenge(MethodEmail, emailChallengeID)
	_ = registration.AttachChallenge(MethodPhone, phoneChallengeID)
	if err := registration.MarkVerificationCompleted(VerificationCompletion{
		EmailChallengeID:           emailChallengeID,
		PhoneChallengeID:           phoneChallengeID,
		EmailVerified:              true,
		PhoneVerified:              true,
		BindingID:                  uuid.New(),
		RegistrationVersion:        1,
		SnapshotHash:               bytes32(3),
		VerificationCompletedEvent: uuid.New(),
		CompletionIdempotencyID:    uuid.New(),
		LinkAcceptUntil:            now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := registration.Link(UserLink{
		UserID:            uuid.New(),
		LinkRequestID:     uuid.New(),
		LinkedAt:          now.Add(10 * time.Minute),
		SessionIssueUntil: now.Add(20 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := registration.BeginSessionIssuance(now.Add(21 * time.Minute)); !errors.Is(err, ErrSessionDeadlineElapsed) {
		t.Fatalf("begin after deadline error = %v", err)
	}
}

func TestRegistrationRejectsInvalidChannelAndRepeatedVerificationMethod(t *testing.T) {
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	registration := mustRegistration(t, now)
	registration.ClientChannel = "terminal"
	if err := registration.Validate(); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("invalid client channel error=%v", err)
	}

	registration = mustRegistration(t, now)
	registration.VerifiedMethods = []Method{MethodEmail, MethodEmail}
	if err := registration.Validate(); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("repeated verified method error=%v", err)
	}
}

func mustRegistration(t *testing.T, now time.Time) Registration {
	t.Helper()
	registration, err := New(NewInput{
		ID:                 uuid.New(),
		IntentID:           uuid.New(),
		EmailIdentityID:    uuid.New(),
		PhoneIdentityID:    uuid.New(),
		ProfileRequestID:   "profile-request",
		AgreementReceiptID: "agreement-receipt",
		ClientChannel:      "web",
		StatusTokenHash:    bytes32(9),
		StatusTokenKeyVer:  1,
		StatusTokenExpires: now.Add(3 * time.Hour),
		ExpiresAt:          now.Add(2 * time.Hour),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("new registration: %v", err)
	}
	return registration
}

func bytes32(seed byte) []byte {
	value := make([]byte, 32)
	for index := range value {
		value[index] = seed
	}
	return value
}
