package registration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
)

func TestStartRejectsMissingRequiredInputBeforeUsingDependencies(t *testing.T) {
	service := &Service{}
	_, err := service.Start(context.Background(), StartInput{})
	assertFailureCode(t, err, "AUTH_INPUT_INVALID")
}

func TestVerifyChallengeCommitsFailedAttemptBeforeReturningPublicFailure(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	registrationID, intentID, emailID, phoneID, challengeID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	registration, err := domainregistration.New(domainregistration.NewInput{
		ID: registrationID, IntentID: intentID, EmailIdentityID: emailID, PhoneIdentityID: phoneID,
		ProfileRequestID: "profile", AgreementReceiptID: "agreement", ClientChannel: "web",
		StatusTokenHash: make([]byte, 32), StatusTokenKeyVer: 1,
		StatusTokenExpires: now.Add(2 * time.Hour), ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("new registration: %v", err)
	}
	if err := registration.AttachChallenge(domainregistration.MethodEmail, challengeID); err != nil {
		t.Fatalf("attach challenge: %v", err)
	}
	verification, err := domainchallenge.New(domainchallenge.NewInput{
		ID: challengeID, SubjectType: domainchallenge.SubjectRegistration, SubjectID: registrationID,
		Purpose: domainchallenge.PurposeSignupEmail, Method: domainchallenge.MethodEmail, Channel: domainchallenge.ChannelEmailCode,
		Destination: "masked", DestinationLookupHash: make([]byte, 32), IdentityID: &emailID,
		CodeHash: make([]byte, 32), VerifierKeyVersion: 1, MaxAttempts: 5, MaxSends: 5,
		NextSendAt: now.Add(time.Minute), ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}

	registrations := &registrationRepositoryStub{registration: registration}
	challenges := &challengeRepositoryStub{challenge: verification}
	transactor := &transactorStub{repositories: TxRepositories{
		Registrations: registrations,
		Challenges:    challenges,
		Intents: &intentRepositoryStub{intent: domainintent.Intent{
			ID: intentID, Channel: domainintent.ChannelWeb, ExpiresAt: now.Add(time.Hour), Status: "active",
		}},
	}}
	service := &Service{
		transactions: transactor,
		cryptography: cryptographyStub{equal: false},
		clock:        fixedClock{now: now},
		intentProof:  intentVerifierStub{},
	}
	_, err = service.VerifyChallenge(context.Background(), VerifyChallengeInput{
		RegistrationID: registrationID.String(), ChallengeID: challengeID.String(),
		OwnerProof: "owner", CSRFToken: "csrf", Code: "123456",
	})
	assertFailureCode(t, err, "AUTH_CHALLENGE_FAILED")
	if !transactor.committed {
		t.Fatal("failed verification attempt was rolled back")
	}
	if challenges.saved.AttemptCount != 1 || challenges.saved.Status != domainchallenge.StatusIssued {
		t.Fatalf("saved challenge = %#v", challenges.saved)
	}
}

func assertFailureCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *failure.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error %v is not a typed failure", err)
	}
	if typed.Code != code {
		t.Fatalf("failure code = %q, want %q", typed.Code, code)
	}
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type transactorStub struct {
	repositories TxRepositories
	committed    bool
}

func (t *transactorStub) WithinTransaction(_ context.Context, run func(TxRepositories) error) error {
	if err := run(t.repositories); err != nil {
		return err
	}
	t.committed = true
	return nil
}

type registrationRepositoryStub struct {
	registration domainregistration.Registration
}

func (r *registrationRepositoryStub) Create(context.Context, domainregistration.Registration) error {
	return nil
}
func (r *registrationRepositoryStub) Find(context.Context, uuid.UUID) (domainregistration.Registration, error) {
	return r.registration, nil
}
func (r *registrationRepositoryStub) FindForUpdate(context.Context, uuid.UUID) (domainregistration.Registration, error) {
	return r.registration, nil
}
func (r *registrationRepositoryStub) Save(_ context.Context, registration *domainregistration.Registration) error {
	r.registration = *registration
	return nil
}

type challengeRepositoryStub struct {
	challenge domainchallenge.Challenge
	saved     domainchallenge.Challenge
}

func (r *challengeRepositoryStub) Issue(context.Context, domainchallenge.Challenge) error { return nil }
func (r *challengeRepositoryStub) FindForUpdate(context.Context, uuid.UUID) (domainchallenge.Challenge, error) {
	return r.challenge, nil
}
func (r *challengeRepositoryStub) Save(_ context.Context, challenge *domainchallenge.Challenge) error {
	r.saved = *challenge
	r.challenge = *challenge
	return nil
}
func (r *challengeRepositoryStub) StoreDeliveryPayload(context.Context, domainchallenge.DeliveryPayload) error {
	return nil
}
func (r *challengeRepositoryStub) StoreVirtualProjection(context.Context, domainchallenge.VirtualProjection) error {
	return nil
}

type intentRepositoryStub struct{ intent domainintent.Intent }

func (r *intentRepositoryStub) FindActiveForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error) {
	return r.intent, nil
}
func (r *intentRepositoryStub) FindCompletionReplayForUpdate(context.Context, uuid.UUID, uuid.UUID) (domainintent.Intent, error) {
	return r.intent, nil
}
func (r *intentRepositoryStub) Consume(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

type intentVerifierStub struct{}

func (intentVerifierStub) VerifyOwnership(current domainintent.Intent, _, _ string, _ bool) (domainintent.Intent, error) {
	return current, nil
}

type cryptographyStub struct{ equal bool }

func (c cryptographyStub) Hash(...string) []byte             { return make([]byte, 32) }
func (c cryptographyStub) Equal([]byte, ...string) bool      { return c.equal }
func (c cryptographyStub) Opaque(string) (string, error)     { return "opaque", nil }
func (c cryptographyStub) VerificationCode() (string, error) { return "123456", nil }
func (c cryptographyStub) Seal(any) ([]byte, error)          { return []byte("sealed"), nil }
func (c cryptographyStub) Open([]byte, any) error            { return nil }
func (c cryptographyStub) SealVirtual(any) ([]byte, error)   { return []byte("virtual"), nil }
