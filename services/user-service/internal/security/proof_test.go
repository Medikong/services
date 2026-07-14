package security

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"
)

func TestProofSignAndVerify(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	seed := sha256.Sum256([]byte("proof-test"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	signer, err := NewSigner("auth-service", "auth-test", base64.RawStdEncoding.EncodeToString(privateKey), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier("auth-service", "auth-test", base64.RawStdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)), time.Second, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	raw, err := signer.Sign(ProofClaims{Audience: "user-service", Purpose: "create_user", RegistrationID: "reg-1", EmailVerified: true, PhoneVerified: true, Nonce: "nonce-1"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(raw, "user-service", "create_user")
	if err != nil {
		t.Fatal(err)
	}
	if claims.RegistrationID != "reg-1" || !claims.EmailVerified || !claims.PhoneVerified {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if _, err := verifier.Verify(raw, "other-service", "create_user"); err == nil {
		t.Fatal("audience mismatch was accepted")
	}
}
