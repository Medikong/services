package security

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ProofClaims struct {
	Issuer         string `json:"iss"`
	Audience       string `json:"aud"`
	Purpose        string `json:"purpose"`
	RegistrationID string `json:"registrationId,omitempty"`
	UserID         string `json:"userId,omitempty"`
	MediaAssetID   string `json:"mediaAssetId,omitempty"`
	StatusChangeID string `json:"statusChangeId,omitempty"`
	AccountStatus  string `json:"accountStatus,omitempty"`
	UserVersion    int64  `json:"userVersion,omitempty"`
	ChangedAt      int64  `json:"changedAt,omitempty"`
	EmailVerified  bool   `json:"emailVerified,omitempty"`
	PhoneVerified  bool   `json:"phoneVerified,omitempty"`
	ScanCompleted  bool   `json:"scanCompleted,omitempty"`
	IssuedAt       int64  `json:"iat"`
	ExpiresAt      int64  `json:"exp"`
	Nonce          string `json:"nonce"`
}

type proofHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid"`
}

type Signer struct {
	issuer string
	keyID  string
	key    ed25519.PrivateKey
	now    func() time.Time
}

type Verifier struct {
	issuer    string
	keyID     string
	key       ed25519.PublicKey
	clockSkew time.Duration
	now       func() time.Time
}

func NewSigner(issuer, keyID, encodedPrivateKey string, now func() time.Time) (Signer, error) {
	key, err := decodePrivateKey(encodedPrivateKey)
	if err != nil {
		return Signer{}, err
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(keyID) == "" {
		return Signer{}, errors.New("proof issuer and key ID are required")
	}
	return Signer{issuer: issuer, keyID: keyID, key: key, now: nowOrUTC(now)}, nil
}

func NewVerifier(issuer, keyID, encodedPublicKey string, clockSkew time.Duration, now func() time.Time) (Verifier, error) {
	key, err := decodePublicKey(encodedPublicKey)
	if err != nil {
		return Verifier{}, err
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(keyID) == "" || clockSkew < 0 {
		return Verifier{}, errors.New("proof issuer, key ID, and non-negative clock skew are required")
	}
	return Verifier{issuer: issuer, keyID: keyID, key: key, clockSkew: clockSkew, now: nowOrUTC(now)}, nil
}

func (s Signer) Sign(claims ProofClaims, ttl time.Duration) (string, error) {
	if ttl <= 0 || strings.TrimSpace(claims.Audience) == "" || strings.TrimSpace(claims.Purpose) == "" || strings.TrimSpace(claims.Nonce) == "" {
		return "", errors.New("proof audience, purpose, nonce, and positive TTL are required")
	}
	now := s.now()
	claims.Issuer = s.issuer
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(ttl).Unix()
	headerJSON, err := json.Marshal(proofHeader{Algorithm: "EdDSA", Type: "JWT", KeyID: s.keyID})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	signature := ed25519.Sign(s.key, []byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (v Verifier) Verify(raw, audience, purpose string) (ProofClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return ProofClaims{}, errors.New("malformed proof")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ProofClaims{}, errors.New("malformed proof header")
	}
	var header proofHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "EdDSA" || header.Type != "JWT" || header.KeyID != v.keyID {
		return ProofClaims{}, errors.New("unsupported proof header")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(v.key, []byte(parts[0]+"."+parts[1]), signature) {
		return ProofClaims{}, errors.New("invalid proof signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ProofClaims{}, errors.New("malformed proof payload")
	}
	var claims ProofClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ProofClaims{}, errors.New("malformed proof claims")
	}
	now := v.now()
	if claims.Issuer != v.issuer || claims.Audience != audience || claims.Purpose != purpose || strings.TrimSpace(claims.Nonce) == "" {
		return ProofClaims{}, errors.New("proof scope does not match")
	}
	if claims.ExpiresAt <= now.Add(-v.clockSkew).Unix() || claims.IssuedAt > now.Add(v.clockSkew).Unix() || claims.ExpiresAt <= claims.IssuedAt {
		return ProofClaims{}, errors.New("proof is expired or not yet valid")
	}
	return claims, nil
}

func decodePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid Ed25519 private key")
	}
	return ed25519.PrivateKey(key), nil
}

func decodePublicKey(encoded string) (ed25519.PublicKey, error) {
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid Ed25519 public key")
	}
	return ed25519.PublicKey(key), nil
}

func nowOrUTC(now func() time.Time) func() time.Time {
	if now != nil {
		return func() time.Time { return now().UTC() }
	}
	return func() time.Time { return time.Now().UTC() }
}
