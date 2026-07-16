package challenge

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestChallengeConsumesOnlyOnce(t *testing.T) {
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	challenge := mustChallenge(t, now, 3)

	result, err := challenge.Consume(now.Add(time.Minute), true)
	if err != nil || !result.Verified || !result.Changed || challenge.Status != StatusVerified {
		t.Fatal("first challenge consumption did not verify")
	}
	result, err = challenge.Consume(now.Add(2*time.Minute), false)
	if err != nil || !result.Verified || !result.AlreadyVerified || result.Changed {
		t.Fatal("verified challenge replay did not remain idempotent")
	}
}

func TestChallengeTracksAttemptsAndClosesAtLimit(t *testing.T) {
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	challenge := mustChallenge(t, now, 2)
	for attempt := 0; attempt < 2; attempt++ {
		result, err := challenge.Consume(now.Add(time.Duration(attempt+1)*time.Minute), false)
		if !errors.Is(err, ErrVerificationFailed) || !result.Changed {
			t.Fatalf("challenge mismatch attempt %d did not match the expected result", attempt)
		}
	}
	if challenge.Status != StatusFailed || challenge.ClosedAt == nil || challenge.AttemptCount != 2 {
		t.Fatal("challenge did not enter the failed state")
	}
	if _, err := challenge.Consume(now.Add(3*time.Minute), true); !errors.Is(err, ErrChallengeClosed) {
		t.Fatalf("consume closed challenge error=%v", err)
	}
}

func TestChallengeExpiresAndVirtualProjectionRules(t *testing.T) {
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	challenge := mustChallenge(t, now, 3)
	if _, err := challenge.Consume(now.Add(11*time.Minute), true); !errors.Is(err, ErrChallengeExpired) {
		t.Fatalf("expired consume error=%v", err)
	}
	if challenge.Status != StatusExpired || challenge.ClosedAt == nil {
		t.Fatal("challenge did not enter the expired state")
	}

	projection := VirtualProjection{
		ChallengeID:       uuid.New(),
		Channel:           ChannelEmailCode,
		ChallengeVersion:  0,
		CodeCiphertext:    []byte("ciphertext"),
		CodeKeyID:         "dev-key",
		MaskedDestination: "a***@example.test",
		Status:            VirtualReady,
		ExpiresAt:         now.Add(time.Minute),
		CreatedAt:         now,
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("ready projection: %v", err)
	}
	destroyedAt := now.Add(time.Minute)
	projection.Status = VirtualDestroyed
	projection.CodeCiphertext = nil
	projection.DestroyedAt = &destroyedAt
	if err := projection.Validate(); err != nil {
		t.Fatalf("destroyed projection: %v", err)
	}
}

func TestChallengeRejectsInconsistentPersistedState(t *testing.T) {
	now := time.Date(2026, time.July, 11, 9, 0, 0, 0, time.UTC)
	challenge := mustChallenge(t, now, 3)
	challenge.Status = Status("unknown")
	if err := challenge.Validate(); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("unknown status error=%v", err)
	}

	challenge = mustChallenge(t, now, 3)
	closedAt := now.Add(time.Minute)
	challenge.ClosedAt = &closedAt
	if err := challenge.Validate(); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("issued challenge with terminal timestamp error=%v", err)
	}
}

func mustChallenge(t *testing.T, now time.Time, maxAttempts int16) Challenge {
	t.Helper()
	challenge, err := New(NewInput{
		ID:                 uuid.New(),
		SubjectType:        SubjectRegistration,
		SubjectID:          uuid.New(),
		Purpose:            PurposeSignupEmail,
		Method:             MethodEmail,
		Channel:            ChannelEmailCode,
		Destination:        "a@example.test",
		CodeHash:           bytes32(1),
		VerifierKeyVersion: 1,
		MaxAttempts:        maxAttempts,
		MaxSends:           3,
		NextSendAt:         now,
		ExpiresAt:          now.Add(10 * time.Minute),
		CreatedAt:          now,
	})
	if err != nil {
		t.Fatalf("new challenge: %v", err)
	}
	return challenge
}

func bytes32(seed byte) []byte {
	value := make([]byte, 32)
	for index := range value {
		value[index] = seed
	}
	return value
}
