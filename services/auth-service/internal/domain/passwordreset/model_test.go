package passwordreset

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestResetLifecycle(t *testing.T) {
	now := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	intentID, identityID, challengeID := uuid.New(), uuid.New(), uuid.New()
	reset, err := New(uuid.New(), &intentID, &identityID, now.Add(15*time.Minute), now)
	if err != nil {
		t.Fatalf("new reset: %v", err)
	}
	if err := reset.AttachChallenge(challengeID); err != nil {
		t.Fatalf("attach challenge: %v", err)
	}
	grantHash := make([]byte, 32)
	grantHash[0] = 1
	if err := reset.Verify(grantHash, now.Add(time.Minute)); err != nil {
		t.Fatalf("verify reset: %v", err)
	}
	grantHash[0] = 9
	if reset.ResetGrantHash[0] != 1 {
		t.Fatal("reset retained the caller's mutable grant hash")
	}
	if err := reset.Complete(now.Add(2 * time.Minute)); err != nil {
		t.Fatalf("complete reset: %v", err)
	}
	if reset.Status != StatusCompleted || reset.ChallengeVerifiedAt == nil || reset.CompletedAt == nil {
		t.Fatalf("unexpected completed reset: %#v", reset)
	}
	if err := reset.Validate(); err != nil {
		t.Fatalf("validate completed reset: %v", err)
	}
}

func TestResetExpiresInsteadOfCrossingStateBoundary(t *testing.T) {
	now := time.Date(2026, time.July, 20, 9, 0, 0, 0, time.UTC)
	reset, err := New(uuid.New(), nil, nil, now.Add(time.Minute), now)
	if err != nil {
		t.Fatalf("new reset: %v", err)
	}
	if err := reset.AttachChallenge(uuid.New()); err != nil {
		t.Fatalf("attach challenge: %v", err)
	}
	if err := reset.Verify(make([]byte, 32), now.Add(time.Minute)); !errors.Is(err, ErrTransition) {
		t.Fatalf("verify at expiry error=%v, want %v", err, ErrTransition)
	}
	if reset.Status != StatusExpired {
		t.Fatalf("status=%q, want %q", reset.Status, StatusExpired)
	}
	if err := reset.Complete(now.Add(time.Minute)); !errors.Is(err, ErrTransition) {
		t.Fatalf("complete expired reset error=%v, want %v", err, ErrTransition)
	}
}
