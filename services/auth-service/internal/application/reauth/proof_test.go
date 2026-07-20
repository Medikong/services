package reauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainreauth "github.com/Medikong/services/services/auth-service/internal/domain/reauth"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func TestConsumeProofIDConsumesOnlyActiveMatchingProof(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	proofID := uuid.New()
	repository := &proofRepositoryStub{proof: domainreauth.Proof{
		ID: proofID, Hash: make([]byte, 32), UserID: uuid.New(), SessionID: uuid.New(),
		Purpose: "replace_phone", ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}}
	service := &Service{cryptography: proofCryptographyStub{}, clock: proofClockStub{now: now}}
	principal := domainsession.Principal{Authenticated: true, UserID: repository.proof.UserID, SessionID: repository.proof.SessionID}

	consumedID, err := service.ConsumeProofID(context.Background(), repository, "opaque-proof", principal, "replace_phone")
	if err != nil {
		t.Fatalf("ConsumeProofID() error = %v", err)
	}
	if consumedID != proofID || repository.consumed != proofID {
		t.Fatalf("consumed ID = %s, repository consumed = %s", consumedID, repository.consumed)
	}
}

func TestConsumeProofIDRejectsExpiredProof(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	repository := &proofRepositoryStub{proof: domainreauth.Proof{
		ID: uuid.New(), Hash: make([]byte, 32), UserID: uuid.New(), SessionID: uuid.New(),
		Purpose: "link_identity", ExpiresAt: now.Add(-time.Second), CreatedAt: now.Add(-time.Minute),
	}}
	service := &Service{cryptography: proofCryptographyStub{}, clock: proofClockStub{now: now}}
	principal := domainsession.Principal{Authenticated: true, UserID: repository.proof.UserID, SessionID: repository.proof.SessionID}

	_, err := service.ConsumeProofID(context.Background(), repository, "opaque-proof", principal, "link_identity")
	var typed *failure.Error
	if !errors.As(err, &typed) || typed.Code != "AUTH_REAUTHENTICATION_PROOF_INVALID" {
		t.Fatalf("ConsumeProofID() error = %v", err)
	}
	if repository.consumed != uuid.Nil {
		t.Fatalf("expired proof was consumed: %s", repository.consumed)
	}
}

type proofRepositoryStub struct {
	proof    domainreauth.Proof
	findErr  error
	consumed uuid.UUID
}

func (*proofRepositoryStub) Create(context.Context, domainreauth.Proof) error { return nil }

func (r *proofRepositoryStub) FindActiveForUpdate(context.Context, []byte, uuid.UUID, uuid.UUID, string) (domainreauth.Proof, error) {
	return r.proof, r.findErr
}

func (r *proofRepositoryStub) Consume(_ context.Context, id uuid.UUID) error {
	r.consumed = id
	return nil
}

type proofClockStub struct{ now time.Time }

func (c proofClockStub) Now() time.Time { return c.now }

type proofCryptographyStub struct{}

func (proofCryptographyStub) Hash(...string) []byte              { return make([]byte, 32) }
func (proofCryptographyStub) Equal([]byte, ...string) bool       { return true }
func (proofCryptographyStub) Opaque(string) (string, error)      { return "", nil }
func (proofCryptographyStub) VerifyPassword(string, string) bool { return true }
func (proofCryptographyStub) SealOutput(Output) ([]byte, error)  { return nil, nil }
func (proofCryptographyStub) OpenOutput([]byte) (Output, error)  { return Output{}, nil }
