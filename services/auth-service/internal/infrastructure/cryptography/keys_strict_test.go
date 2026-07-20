package cryptography

import (
	"strings"
	"testing"
	"time"
)

func TestAccessTokenRejectsNonCanonicalBase64URLSignature(t *testing.T) {
	// Given
	now := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)
	keys := Keys{
		CredentialHMAC: []byte("01234567890123456789012345678901"),
		ReplayKey:      []byte("01234567890123456789012345678901"),
		JWTKey:         privateKeyPEM(t, testPrivateKey(t)),
		JWTKeyID:       "active-key",
		JWTIssuer:      "auth-service",
		JWTAudiences:   []string{"dropmong-api"},
		Now:            func() time.Time { return now },
	}
	token, _, err := keys.SignAccessToken(
		"7df1ef50-f05b-4d58-8934-c52a7510af35",
		"cc61de80-da8e-4149-8b7a-c166237d552c",
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT segment count = %d", len(parts))
	}
	nonCanonical := parts[0] + "." + parts[1] + "." + nonCanonicalBase64URLTail(t, parts[2])

	// When
	_, err = keys.VerifyAccessToken(nonCanonical)

	// Then
	if err == nil {
		t.Fatal("JWT with a non-canonical base64url signature was accepted")
	}
}

func nonCanonicalBase64URLTail(t *testing.T, value string) string {
	t.Helper()
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	if len(value)%4 != 2 {
		t.Fatalf("base64url length modulo 4 = %d, want 2", len(value)%4)
	}
	index := strings.IndexByte(alphabet, value[len(value)-1])
	if index < 0 || index&15 != 0 {
		t.Fatalf("canonical base64url tail index = %d", index)
	}
	return value[:len(value)-1] + string(alphabet[index+1])
}
