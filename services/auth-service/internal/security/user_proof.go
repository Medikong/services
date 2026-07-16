package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type UserStatusProof struct {
	Issuer         string `json:"iss"`
	Audience       string `json:"aud"`
	Purpose        string `json:"purpose"`
	StatusChangeID string `json:"statusChangeId"`
	UserID         string `json:"userId"`
	AccountStatus  string `json:"accountStatus"`
	UserVersion    int64  `json:"userVersion"`
	ChangedAt      int64  `json:"changedAt"`
	IssuedAt       int64  `json:"iat"`
	ExpiresAt      int64  `json:"exp"`
	Nonce          string `json:"nonce"`
}

// UserProofClaims is the signed cross-service proof format shared with User.
// Optional fields are purpose-specific and are validated by the caller.
type UserProofClaims struct {
	Issuer         string `json:"iss"`
	Audience       string `json:"aud"`
	Purpose        string `json:"purpose"`
	RegistrationID string `json:"registrationId,omitempty"`
	UserID         string `json:"userId,omitempty"`
	StatusChangeID string `json:"statusChangeId,omitempty"`
	AccountStatus  string `json:"accountStatus,omitempty"`
	UserVersion    int64  `json:"userVersion,omitempty"`
	ChangedAt      int64  `json:"changedAt,omitempty"`
	EmailVerified  bool   `json:"emailVerified,omitempty"`
	PhoneVerified  bool   `json:"phoneVerified,omitempty"`
	IssuedAt       int64  `json:"iat"`
	ExpiresAt      int64  `json:"exp"`
	Nonce          string `json:"nonce"`
}

type UserProofSigner struct {
	issuer string
	keyID  string
	key    ed25519.PrivateKey
	now    func() time.Time
}

func NewUserProofSigner(issuer, keyID, encodedPrivateKey string, now func() time.Time) (UserProofSigner, error) {
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encodedPrivateKey))
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return UserProofSigner{}, errors.New("invalid Auth proof private key")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(keyID) == "" {
		return UserProofSigner{}, errors.New("Auth proof issuer and key ID are required")
	}
	if now == nil {
		now = time.Now
	}
	return UserProofSigner{issuer: issuer, keyID: keyID, key: ed25519.PrivateKey(key), now: now}, nil
}

func (s UserProofSigner) SignRegistrationCompletion(registrationID string, ttl time.Duration) (string, error) {
	if strings.TrimSpace(registrationID) == "" || ttl <= 0 {
		return "", errors.New("registration ID and positive proof TTL are required")
	}
	nonce, err := randomProofNonce()
	if err != nil {
		return "", err
	}
	return s.sign(UserProofClaims{
		Audience:       "user-service",
		Purpose:        "create_user",
		RegistrationID: registrationID,
		EmailVerified:  true,
		PhoneVerified:  true,
		Nonce:          nonce,
	}, ttl)
}

func (s UserProofSigner) sign(claims UserProofClaims, ttl time.Duration) (string, error) {
	now := s.now().UTC()
	claims.Issuer = s.issuer
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(ttl).Unix()
	header, err := json.Marshal(struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		KeyID     string `json:"kid"`
	}{Algorithm: "EdDSA", Type: "JWT", KeyID: s.keyID})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(s.key, []byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

type UserProofVerifier struct {
	issuer    string
	keyID     string
	key       ed25519.PublicKey
	clockSkew time.Duration
	now       func() time.Time
}

func NewUserProofVerifier(issuer, keyID, encodedPublicKey string, clockSkew time.Duration, now func() time.Time) (UserProofVerifier, error) {
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encodedPublicKey))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return UserProofVerifier{}, errors.New("invalid User proof public key")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(keyID) == "" || clockSkew < 0 {
		return UserProofVerifier{}, errors.New("User proof issuer, key ID, and non-negative clock skew are required")
	}
	if now == nil {
		now = time.Now
	}
	return UserProofVerifier{issuer: issuer, keyID: keyID, key: ed25519.PublicKey(key), clockSkew: clockSkew, now: now}, nil
}

func (v UserProofVerifier) VerifyUserStatus(raw string) (UserStatusProof, error) {
	claims, err := v.verify(raw, "auth-service", "apply_user_status")
	if err != nil {
		return UserStatusProof{}, err
	}
	return UserStatusProof{
		Issuer: claims.Issuer, Audience: claims.Audience, Purpose: claims.Purpose,
		StatusChangeID: claims.StatusChangeID, UserID: claims.UserID,
		AccountStatus: claims.AccountStatus, UserVersion: claims.UserVersion,
		ChangedAt: claims.ChangedAt, IssuedAt: claims.IssuedAt, ExpiresAt: claims.ExpiresAt,
		Nonce: claims.Nonce,
	}, nil
}

func (v UserProofVerifier) VerifyUserCreation(raw string) (UserProofClaims, error) {
	return v.verify(raw, "auth-service", "complete_registration")
}

func (v UserProofVerifier) verify(raw, audience, purpose string) (UserProofClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return UserProofClaims{}, errors.New("malformed User proof")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return UserProofClaims{}, errors.New("malformed User proof header")
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
		KeyID     string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || header.Algorithm != "EdDSA" || header.Type != "JWT" || header.KeyID != v.keyID {
		return UserProofClaims{}, errors.New("unsupported User proof header")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(v.key, []byte(parts[0]+"."+parts[1]), signature) {
		return UserProofClaims{}, errors.New("invalid User proof signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return UserProofClaims{}, errors.New("malformed User proof payload")
	}
	var claims UserProofClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return UserProofClaims{}, errors.New("malformed User proof claims")
	}
	now := v.now().UTC()
	if claims.Issuer != v.issuer || claims.Audience != audience || claims.Purpose != purpose || strings.TrimSpace(claims.Nonce) == "" {
		return UserProofClaims{}, errors.New("User proof scope does not match")
	}
	if claims.ExpiresAt <= now.Add(-v.clockSkew).Unix() || claims.IssuedAt > now.Add(v.clockSkew).Unix() || claims.ExpiresAt <= claims.IssuedAt {
		return UserProofClaims{}, errors.New("User proof is expired or not yet valid")
	}
	return claims, nil
}

func randomProofNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
