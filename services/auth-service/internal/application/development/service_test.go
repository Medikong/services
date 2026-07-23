package development

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

func TestGetVirtualMessageUsesPasswordResetIntentOwnership(t *testing.T) {
	now := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	challengeID, resetID, intentID := uuid.New(), uuid.New(), uuid.New()
	repository := &developmentRepositoryFake{
		challenge: domainchallenge.Challenge{ID: challengeID, SubjectType: domainchallenge.SubjectPasswordReset, SubjectID: resetID},
		projection: domainchallenge.VirtualProjection{
			ChallengeID: challengeID, Channel: domainchallenge.ChannelEmailCode, Status: domainchallenge.VirtualReady,
			CodeCiphertext: []byte("encrypted"), MaskedDestination: "m***@example.com", ExpiresAt: now.Add(time.Minute),
		},
		passwordResetIntent: intentID,
		intent:              domainintent.Intent{ID: intentID},
	}
	ownership := &developmentOwnershipFake{}
	service := NewService(developmentTransactorFake{repository: repository}, developmentCryptographyFake{code: "123456"}, ownership, developmentClock{now: now}, nil)

	output, err := service.GetVirtualMessage(context.Background(), VirtualMessageInput{
		ChallengeID: challengeID.String(), OwnerProof: "owner-proof", CSRFToken: "csrf",
	})
	if err != nil {
		t.Fatalf("get virtual message: %v", err)
	}
	if output.Code != "123456" || output.ChallengeID != challengeID.String() || output.MaskedDestination != "m***@example.com" {
		t.Fatalf("unexpected output: %#v", output)
	}
	if ownership.intent.ID != intentID || ownership.ownerProof != "owner-proof" || ownership.requireCSRF {
		t.Fatalf("unexpected ownership call: %#v", ownership)
	}
}

func TestGetVirtualMessageHidesOwnershipFailure(t *testing.T) {
	challengeID, resetID, intentID := uuid.New(), uuid.New(), uuid.New()
	repository := &developmentRepositoryFake{
		challenge:           domainchallenge.Challenge{ID: challengeID, SubjectType: domainchallenge.SubjectPasswordReset, SubjectID: resetID},
		passwordResetIntent: intentID,
		intent:              domainintent.Intent{ID: intentID},
	}
	ownership := &developmentOwnershipFake{err: failure.Forbidden("AUTH_INTENT_OWNERSHIP_INVALID", "invalid")}
	service := NewService(developmentTransactorFake{repository: repository}, developmentCryptographyFake{code: "123456"}, ownership, developmentClock{now: time.Now().UTC()}, nil)

	_, err := service.GetVirtualMessage(context.Background(), VirtualMessageInput{ChallengeID: challengeID.String()})
	assertDevelopmentFailure(t, err, failure.KindNotFound, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND")
}

func TestGetVirtualMessagePreservesUnavailableProjectionContract(t *testing.T) {
	challengeID, intentID := uuid.New(), uuid.New()
	repository := &developmentRepositoryFake{
		challenge:     domainchallenge.Challenge{ID: challengeID, SubjectType: domainchallenge.SubjectPhoneSignIn, SubjectID: intentID},
		projectionErr: domainchallenge.ErrVirtualUnavailable,
		intent:        domainintent.Intent{ID: intentID},
	}
	service := NewService(developmentTransactorFake{repository: repository}, developmentCryptographyFake{}, &developmentOwnershipFake{}, developmentClock{now: time.Now().UTC()}, nil)

	_, err := service.GetVirtualMessage(context.Background(), VirtualMessageInput{ChallengeID: challengeID.String()})
	assertDevelopmentFailure(t, err, failure.KindConflict, "AUTH_VIRTUAL_MESSAGE_UNAVAILABLE")
}

func assertDevelopmentFailure(t *testing.T, err error, kind failure.Kind, code string) {
	t.Helper()
	var typed *failure.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error=%v, want typed application failure", err)
	}
	if typed.Kind != kind || typed.Code != code {
		t.Fatalf("failure=(%s,%s), want (%s,%s)", typed.Kind, typed.Code, kind, code)
	}
}

type developmentTransactorFake struct {
	repository Repository
}

func (f developmentTransactorFake) WithinTransaction(ctx context.Context, run func(TxRepositories) error) error {
	return run(TxRepositories{Virtual: f.repository})
}

type developmentRepositoryFake struct {
	challenge           domainchallenge.Challenge
	challengeErr        error
	projection          domainchallenge.VirtualProjection
	projectionErr       error
	registrationIntent  uuid.UUID
	passwordResetIntent uuid.UUID
	requestedLinkUser   uuid.UUID
	intent              domainintent.Intent
	intentErr           error
}

func (f *developmentRepositoryFake) FindChallenge(context.Context, uuid.UUID) (domainchallenge.Challenge, error) {
	return f.challenge, f.challengeErr
}

func (f *developmentRepositoryFake) FindVirtualProjection(context.Context, uuid.UUID, time.Time) (domainchallenge.VirtualProjection, error) {
	return f.projection, f.projectionErr
}

func (f *developmentRepositoryFake) FindRegistrationIntent(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.registrationIntent, nil
}

func (f *developmentRepositoryFake) FindPasswordResetIntent(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.passwordResetIntent, nil
}

func (f *developmentRepositoryFake) FindRequestedLinkUser(context.Context, uuid.UUID) (uuid.UUID, error) {
	return f.requestedLinkUser, nil
}

func (f *developmentRepositoryFake) FindIntentForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error) {
	return f.intent, f.intentErr
}

type developmentCryptographyFake struct {
	code string
	err  error
}

func (f developmentCryptographyFake) OpenVirtual(_ []byte, target any) error {
	if f.err != nil {
		return f.err
	}
	payload, err := json.Marshal(map[string]string{"code": f.code})
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, target)
}

type developmentOwnershipFake struct {
	intent      domainintent.Intent
	ownerProof  string
	csrf        string
	requireCSRF bool
	err         error
}

func (f *developmentOwnershipFake) VerifyOwnership(current domainintent.Intent, ownerProof, csrf string, requireCSRF bool) (domainintent.Intent, error) {
	f.intent, f.ownerProof, f.csrf, f.requireCSRF = current, ownerProof, csrf, requireCSRF
	return current, f.err
}

type developmentClock struct {
	now time.Time
}

func (c developmentClock) Now() time.Time { return c.now }
