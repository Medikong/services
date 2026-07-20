package reauth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProofActive(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	proof := Proof{
		ID: uuid.New(), Hash: make([]byte, 32), UserID: uuid.New(), SessionID: uuid.New(),
		Purpose: "replace_phone", ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}
	if !proof.Active(now) {
		t.Fatal("unconsumed, unexpired proof should be active")
	}
	consumedAt := now
	proof.ConsumedAt = &consumedAt
	if proof.Active(now) {
		t.Fatal("consumed proof should not be active")
	}
	proof.ConsumedAt = nil
	proof.Purpose = "unsupported"
	if proof.Active(now) {
		t.Fatal("unsupported purpose should not be active")
	}
}
