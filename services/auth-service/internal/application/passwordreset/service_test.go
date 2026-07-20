package passwordreset

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainpasswordreset "github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func TestCompleteUsesConfiguredPasswordMinimumLength(t *testing.T) {
	service := &Service{config: Config{PasswordMinLength: 16}}
	password := "123456789012"
	err := service.Complete(context.Background(), CompleteInput{
		ResetID: uuid.NewString(), NewPassword: password, ConfirmPassword: password, IdempotencyKey: "key",
	})
	assertPasswordResetFailure(t, err, failure.KindInvalid, "AUTH_PASSWORD_POLICY_NOT_MET")
}

func TestVerifyCommitsFailedChallengeAttempt(t *testing.T) {
	now := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	resetID, intentID, identityID, challengeID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	reset, err := domainpasswordreset.New(resetID, &intentID, &identityID, now.Add(15*time.Minute), now)
	if err != nil {
		t.Fatalf("new reset: %v", err)
	}
	if err := reset.AttachChallenge(challengeID); err != nil {
		t.Fatalf("attach challenge: %v", err)
	}
	cryptography := &passwordResetCryptographyFake{}
	challenge, err := domainchallenge.New(domainchallenge.NewInput{
		ID: challengeID, SubjectType: domainchallenge.SubjectPasswordReset, SubjectID: resetID,
		Purpose: domainchallenge.PurposePasswordReset, Method: domainchallenge.MethodEmail, Channel: domainchallenge.ChannelEmailCode,
		Destination: "masked-destination", DestinationLookupHash: cryptography.Hash("destination"),
		CodeHash: cryptography.Hash("challenge", challengeID.String(), "654321"), VerifierKeyVersion: 1,
		MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(time.Minute), ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}
	resetRepository := &passwordResetRepositoryFake{value: reset}
	challengeRepository := &passwordResetChallengeRepositoryFake{value: challenge}
	transaction := &passwordResetTransactorFake{repositories: TxRepositories{
		Resets: resetRepository, Challenges: challengeRepository,
		Intents: &passwordResetIntentRepositoryFake{value: domainintent.Intent{ID: intentID}},
		Audit:   &passwordResetAuditFake{},
	}}
	service := NewService(transaction, cryptography, passwordResetOwnershipFake{}, passwordResetClock{now: now.Add(time.Minute)}, Config{})

	_, err = service.Verify(context.Background(), VerifyInput{
		ResetID: resetID.String(), ChallengeID: challengeID.String(), OwnerProof: "owner-proof", CSRFToken: "csrf",
		Code: "123456", Channel: "web", IdempotencyKey: "verify-key",
	})
	assertPasswordResetFailure(t, err, failure.KindInvalid, "AUTH_CHALLENGE_FAILED")
	if transaction.commits != 1 || transaction.rollbacks != 0 {
		t.Fatalf("transaction commits=%d rollbacks=%d, want 1/0", transaction.commits, transaction.rollbacks)
	}
	if challengeRepository.saves != 1 || challengeRepository.value.AttemptCount != 1 || challengeRepository.value.Status != domainchallenge.StatusIssued {
		t.Fatalf("failed attempt was not persisted: %#v", challengeRepository.value)
	}
}

func TestCompleteKeepsCredentialSessionEventAndAuditInOneTransaction(t *testing.T) {
	now := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	resetID, intentID, identityID, challengeID, userID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cryptography := &passwordResetCryptographyFake{passwordHash: "password-hash"}
	reset, err := domainpasswordreset.New(resetID, &intentID, &identityID, now.Add(15*time.Minute), now)
	if err != nil {
		t.Fatalf("new reset: %v", err)
	}
	if err := reset.AttachChallenge(challengeID); err != nil {
		t.Fatalf("attach challenge: %v", err)
	}
	grant := "rgr_test_grant"
	if err := reset.Verify(cryptography.Hash(resetID.String(), grant), now.Add(time.Minute)); err != nil {
		t.Fatalf("verify reset: %v", err)
	}

	var events []string
	resetRepository := &passwordResetRepositoryFake{value: reset, events: &events}
	identityRepository := &passwordResetIdentityRepositoryFake{
		link: domainidentity.Link{ID: uuid.New(), Identity: identityID, UserID: userID, Status: "active"}, events: &events,
	}
	sessionID := uuid.New()
	transaction := &passwordResetTransactorFake{events: &events, repositories: TxRepositories{
		Resets:     resetRepository,
		Identities: identityRepository,
		Intents:    &passwordResetIntentRepositoryFake{value: domainintent.Intent{ID: intentID}},
		Sessions:   &passwordResetSessionFake{events: &events, sessions: []domainsession.Session{{ID: sessionID, UserID: userID, Status: "active"}}},
		Outbox:     &passwordResetOutboxFake{events: &events},
		Audit:      &passwordResetAuditFake{events: &events},
	}}
	fencer := &passwordResetRevocationFencerFake{events: &events, transactionDone: &transaction.finished}
	service := NewService(transaction, cryptography, passwordResetOwnershipFake{}, passwordResetClock{now: now.Add(2 * time.Minute)}, Config{})
	service.UseSessionRevocation(fencer)

	err = service.Complete(context.Background(), CompleteInput{
		ResetID: resetID.String(), OwnerProof: "owner-proof", CSRFToken: "csrf", Channel: "ios", ResetGrant: grant,
		NewPassword: "correct-horse-battery-staple", ConfirmPassword: "correct-horse-battery-staple", IdempotencyKey: "complete-key",
	})
	if err != nil {
		t.Fatalf("complete reset: %v", err)
	}
	if transaction.commits != 1 || transaction.rollbacks != 0 {
		t.Fatalf("transaction commits=%d rollbacks=%d, want 1/0", transaction.commits, transaction.rollbacks)
	}
	wantEvents := []string{"credential", "link", "reset", "outbox", "audit", "session-find", "fence", "session", "commit", "resolve"}
	if strings.Join(events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events=%v, want %v", events, wantEvents)
	}
	if resetRepository.value.Status != domainpasswordreset.StatusCompleted {
		t.Fatalf("reset status=%q, want %q", resetRepository.value.Status, domainpasswordreset.StatusCompleted)
	}
	if identityRepository.passwordHash != "password-hash" || len(fencer.targets) != 1 || fencer.targets[0].ID != sessionID {
		t.Fatalf("credential/fence mismatch: hash=%q targets=%#v", identityRepository.passwordHash, fencer.targets)
	}
	if fencer.fence == nil || !fencer.fence.resolved || !fencer.fence.contextLive || !fencer.fence.resolvedAfterTransaction {
		t.Fatalf("fence resolution mismatch: %#v", fencer.fence)
	}
}

func assertPasswordResetFailure(t *testing.T, err error, kind failure.Kind, code string) {
	t.Helper()
	var typed *failure.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error=%v, want typed application failure", err)
	}
	if typed.Kind != kind || typed.Code != code {
		t.Fatalf("failure=(%s,%s), want (%s,%s)", typed.Kind, typed.Code, kind, code)
	}
}

type passwordResetTransactorFake struct {
	repositories       TxRepositories
	events             *[]string
	commits, rollbacks int
	finished           bool
}

func (f *passwordResetTransactorFake) WithinTransaction(ctx context.Context, run func(TxRepositories) error) error {
	if err := run(f.repositories); err != nil {
		f.rollbacks++
		f.finished = true
		appendPasswordResetEvent(f.events, "rollback")
		return err
	}
	f.commits++
	f.finished = true
	appendPasswordResetEvent(f.events, "commit")
	return nil
}

type passwordResetRepositoryFake struct {
	value   domainpasswordreset.Reset
	findErr error
	events  *[]string
	creates int
	saves   int
}

func (f *passwordResetRepositoryFake) Create(_ context.Context, value domainpasswordreset.Reset) error {
	f.value, f.creates = value, f.creates+1
	appendPasswordResetEvent(f.events, "reset-create")
	return nil
}

func (f *passwordResetRepositoryFake) FindForUpdate(context.Context, uuid.UUID) (domainpasswordreset.Reset, error) {
	return f.value, f.findErr
}

func (f *passwordResetRepositoryFake) Save(_ context.Context, value *domainpasswordreset.Reset) error {
	f.value, f.saves = *value, f.saves+1
	appendPasswordResetEvent(f.events, "reset")
	return nil
}

type passwordResetChallengeRepositoryFake struct {
	value domainchallenge.Challenge
	saves int
}

func (f *passwordResetChallengeRepositoryFake) Issue(_ context.Context, value domainchallenge.Challenge) error {
	f.value = value
	return nil
}

func (f *passwordResetChallengeRepositoryFake) FindForUpdate(context.Context, uuid.UUID) (domainchallenge.Challenge, error) {
	return f.value, nil
}

func (f *passwordResetChallengeRepositoryFake) Save(_ context.Context, value *domainchallenge.Challenge) error {
	f.value, f.saves = *value, f.saves+1
	return nil
}

func (*passwordResetChallengeRepositoryFake) StoreDeliveryPayload(context.Context, domainchallenge.DeliveryPayload) error {
	return nil
}

func (*passwordResetChallengeRepositoryFake) StoreVirtualProjection(context.Context, domainchallenge.VirtualProjection) error {
	return nil
}

type passwordResetIdentityRepositoryFake struct {
	identity     domainidentity.Identity
	findValueErr error
	findIDErr    error
	link         domainidentity.Link
	linkErr      error
	replaceErr   error
	passwordHash string
	events       *[]string
}

func (f *passwordResetIdentityRepositoryFake) FindByValueForUpdate(context.Context, domainidentity.Type, string) (domainidentity.Identity, error) {
	return f.identity, f.findValueErr
}

func (f *passwordResetIdentityRepositoryFake) FindByIDForUpdate(context.Context, uuid.UUID) (domainidentity.Identity, error) {
	return f.identity, f.findIDErr
}

func (f *passwordResetIdentityRepositoryFake) ReplacePasswordCredential(_ context.Context, _ uuid.UUID, passwordHash string) error {
	f.passwordHash = passwordHash
	appendPasswordResetEvent(f.events, "credential")
	return f.replaceErr
}

func (f *passwordResetIdentityRepositoryFake) FindActiveLinkForIdentity(context.Context, uuid.UUID) (domainidentity.Link, error) {
	appendPasswordResetEvent(f.events, "link")
	return f.link, f.linkErr
}

type passwordResetIntentRepositoryFake struct {
	value domainintent.Intent
	err   error
}

func (f *passwordResetIntentRepositoryFake) FindActiveForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error) {
	return f.value, f.err
}

type passwordResetIdempotencyRepositoryFake struct{}

func (*passwordResetIdempotencyRepositoryFake) FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error) {
	return domainidempotency.Record{}, domainidempotency.ErrNotFound
}

func (*passwordResetIdempotencyRepositoryFake) CreateCompleted(context.Context, domainidempotency.Record, string, string) error {
	return nil
}

type passwordResetSessionFake struct {
	events   *[]string
	sessions []domainsession.Session
	err      error
}

func (f *passwordResetSessionFake) FindActiveForUserForUpdate(context.Context, uuid.UUID) ([]domainsession.Session, error) {
	appendPasswordResetEvent(f.events, "session-find")
	return append([]domainsession.Session(nil), f.sessions...), f.err
}

func (f *passwordResetSessionFake) RevokeForUser(context.Context, uuid.UUID, string) error {
	appendPasswordResetEvent(f.events, "session")
	return f.err
}

type passwordResetOutboxFake struct {
	events *[]string
	err    error
}

func (f *passwordResetOutboxFake) Append(context.Context, domainoutbox.Event) error {
	appendPasswordResetEvent(f.events, "outbox")
	return f.err
}

type passwordResetAuditFake struct {
	events *[]string
	err    error
}

func (f *passwordResetAuditFake) Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error {
	appendPasswordResetEvent(f.events, "audit")
	return f.err
}

type passwordResetRevocationFencerFake struct {
	events          *[]string
	transactionDone *bool
	targets         []domainsession.Session
	fence           *passwordResetRevocationFenceFake
}

func (f *passwordResetRevocationFencerFake) Fence(_ context.Context, targets []domainsession.Session) (domainsession.RevocationFence, error) {
	f.targets = append([]domainsession.Session(nil), targets...)
	f.fence = &passwordResetRevocationFenceFake{events: f.events, transactionDone: f.transactionDone}
	appendPasswordResetEvent(f.events, "fence")
	return f.fence, nil
}

type passwordResetRevocationFenceFake struct {
	events                   *[]string
	transactionDone          *bool
	resolved                 bool
	contextLive              bool
	resolvedAfterTransaction bool
}

func (f *passwordResetRevocationFenceFake) Resolve(ctx context.Context) error {
	f.resolved = true
	f.contextLive = ctx.Err() == nil
	f.resolvedAfterTransaction = f.transactionDone != nil && *f.transactionDone
	appendPasswordResetEvent(f.events, "resolve")
	return nil
}

type passwordResetCryptographyFake struct {
	passwordHash string
}

func (*passwordResetCryptographyFake) Hash(values ...string) []byte {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return append([]byte(nil), digest[:]...)
}

func (f *passwordResetCryptographyFake) Equal(expected []byte, values ...string) bool {
	return hmac.Equal(expected, f.Hash(values...))
}

func (*passwordResetCryptographyFake) EqualHash(expected, actual []byte) bool {
	return hmac.Equal(expected, actual)
}

func (*passwordResetCryptographyFake) Opaque(prefix string) (string, error) {
	return prefix + "opaque", nil
}

func (*passwordResetCryptographyFake) VerificationCode() (string, error) { return "123456", nil }

func (*passwordResetCryptographyFake) Seal(any) ([]byte, error) { return []byte("sealed"), nil }

func (*passwordResetCryptographyFake) SealVirtual(any) ([]byte, error) {
	return []byte("virtual-sealed"), nil
}

func (f *passwordResetCryptographyFake) HashPassword(string) (string, error) {
	return f.passwordHash, nil
}

type passwordResetOwnershipFake struct {
	err error
}

func (f passwordResetOwnershipFake) VerifyOwnership(current domainintent.Intent, _, _ string, _ bool) (domainintent.Intent, error) {
	return current, f.err
}

type passwordResetClock struct {
	now time.Time
}

func (c passwordResetClock) Now() time.Time { return c.now }

func appendPasswordResetEvent(events *[]string, event string) {
	if events != nil {
		*events = append(*events, event)
	}
}
