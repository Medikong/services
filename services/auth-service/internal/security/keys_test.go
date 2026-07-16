package security

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func TestAccessTokenUsesRS256AllowlistAndJWKS(t *testing.T) {
	active := testPrivateKey(t)
	retiring := testPrivateKey(t)
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	keys := Keys{
		CredentialHMAC: []byte("01234567890123456789012345678901"),
		ReplayKey:      []byte("01234567890123456789012345678901"),
		JWTKey:         privateKeyPEM(t, active),
		JWTKeyID:       "active-2026-07",
		JWTIssuer:      "https://auth.dropmong.test",
		JWTAudiences:   []string{"dropmong-api"},
		JWTVerifyKeys:  map[string]*rsa.PublicKey{"retiring-2026-06": &retiring.PublicKey},
		Now:            func() time.Time { return now },
	}
	if err := keys.Validate(false); err != nil {
		t.Fatal(err)
	}
	raw, expiresAt, err := keys.SignAccessToken("7df1ef50-f05b-4d58-8934-c52a7510af35", "cc61de80-da8e-4149-8b7a-c166237d552c", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := keys.VerifyAccessToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject == "" || claims.SessionID == "" || claims.TokenID == "" || !expiresAt.Equal(now.Add(15*time.Minute)) {
		t.Fatal("JWT required claims or expiry are invalid")
	}
	parts := splitJWT(t, raw)
	var payload map[string]any
	decodeJWTPart(t, parts[1], &payload)
	want := map[string]bool{"iss": true, "sub": true, "sid": true, "aud": true, "iat": true, "exp": true, "jti": true}
	if len(payload) != len(want) {
		t.Fatalf("JWT claim count = %d, want %d", len(payload), len(want))
	}
	for claim := range payload {
		if !want[claim] {
			t.Fatalf("JWT contains forbidden claim %q", claim)
		}
	}
	var header map[string]string
	decodeJWTPart(t, parts[0], &header)
	if header["alg"] != "RS256" || header["kid"] != "active-2026-07" || header["typ"] != "JWT" {
		t.Fatalf("JWT protected header is invalid: %#v", header)
	}
	jwks, err := keys.JWKS()
	if err != nil {
		t.Fatal(err)
	}
	if len(jwks.Keys) != 2 || jwks.Keys[0].KeyID != "active-2026-07" {
		t.Fatalf("JWKS does not contain active and retiring keys: %#v", jwks)
	}
}

func TestAccessTokenRejectsTamperingExpiryAudienceAndUnknownKey(t *testing.T) {
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	privateKey := testPrivateKey(t)
	keys := Keys{
		CredentialHMAC: []byte("01234567890123456789012345678901"),
		ReplayKey:      []byte("01234567890123456789012345678901"),
		JWTKey:         privateKeyPEM(t, privateKey),
		JWTKeyID:       "active-key",
		JWTIssuer:      "auth-service",
		JWTAudiences:   []string{"dropmong-api"},
		Now:            func() time.Time { return now },
	}
	raw, _, err := keys.SignAccessToken("user-id", "session-id", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	parts := splitJWT(t, raw)
	tampered := parts[0] + "." + parts[1] + "." + parts[2][:len(parts[2])-1] + "A"
	if _, err := keys.VerifyAccessToken(tampered); err == nil {
		t.Fatal("tampered JWT was accepted")
	}
	expiredVerifier := keys
	expiredVerifier.Now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := expiredVerifier.VerifyAccessToken(raw); err == nil {
		t.Fatal("expired JWT was accepted")
	}
	wrongAudience := keys
	wrongAudience.JWTAudiences = []string{"other-api"}
	if _, err := wrongAudience.VerifyAccessToken(raw); err == nil {
		t.Fatal("JWT with a wrong audience was accepted")
	}
	var header map[string]string
	decodeJWTPart(t, parts[0], &header)
	header["kid"] = "unknown-key"
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	unknownKey := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + parts[1] + "." + parts[2]
	if _, err := keys.VerifyAccessToken(unknownKey); err == nil {
		t.Fatal("JWT with an unknown kid was accepted")
	}
}

func testPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func privateKeyPEM(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
}

func splitJWT(t *testing.T, raw string) []string {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT segment count = %d", len(parts))
	}
	return parts
}

func decodeJWTPart(t *testing.T, raw string, target any) {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(decoded, target); err != nil {
		t.Fatal(err)
	}
}
