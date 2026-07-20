//go:build integration

package integration_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"sync"
	"testing"

	"github.com/Medikong/services/services/auth-service/internal/infrastructure/cryptography"
)

var (
	integrationJWTOnce sync.Once
	integrationJWTPem  []byte
	integrationJWTErr  error
)

func integrationJWTPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	integrationJWTOnce.Do(func() {
		privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			integrationJWTErr = err
			return
		}
		encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
		if err != nil {
			integrationJWTErr = err
			return
		}
		integrationJWTPem = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	})
	if integrationJWTErr != nil {
		t.Fatalf("generate integration JWT key: %v", integrationJWTErr)
	}
	return string(integrationJWTPem)
}

func integrationSecurityKeys(t *testing.T) cryptography.Keys {
	t.Helper()
	return cryptography.Keys{
		CredentialHMAC: []byte("01234567890123456789012345678901"),
		ReplayKey:      []byte("01234567890123456789012345678901"),
		JWTKey:         []byte(integrationJWTPrivateKeyPEM(t)),
		JWTKeyID:       "integration-key",
		JWTIssuer:      "integration",
		JWTAudiences:   []string{"dropmong-api"},
	}
}

func integrationUserProofPublicKey() string {
	seed := sha256.Sum256([]byte("dropmong-user-outgoing-proof"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	return base64.RawStdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey))
}

func integrationUserProofPrivateKey() string {
	seed := sha256.Sum256([]byte("dropmong-user-outgoing-proof"))
	return base64.RawStdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed[:]))
}

func integrationAuthProofPrivateKey() string {
	seed := sha256.Sum256([]byte("dropmong-user-auth-proof"))
	return base64.RawStdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed[:]))
}
