// Package security owns opaque credential, replay-payload, and JWT
// cryptography used by auth application services. It never logs or persists
// plaintext credentials.
package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Keys struct {
	CredentialHMAC []byte
	ReplayKey      []byte
	JWTKey         []byte
	JWTIssuer      string
	VirtualKey     []byte
	Random         io.Reader
	Now            func() time.Time
}

func (k Keys) Validate(virtualEnabled bool) error {
	if len(k.CredentialHMAC) < 32 {
		return errors.New("credential HMAC key must be at least 32 bytes")
	}
	if len(k.ReplayKey) != 32 {
		return errors.New("replay encryption key must be exactly 32 bytes")
	}
	if len(k.JWTKey) < 32 || strings.TrimSpace(k.JWTIssuer) == "" {
		return errors.New("JWT key and issuer are required")
	}
	if virtualEnabled && len(k.VirtualKey) < 32 {
		return errors.New("virtual message key must be at least 32 bytes")
	}
	return nil
}

func (k Keys) random() io.Reader {
	if k.Random != nil {
		return k.Random
	}
	return rand.Reader
}

func (k Keys) now() time.Time {
	if k.Now != nil {
		return k.Now().UTC()
	}
	return time.Now().UTC()
}

func (k Keys) Hash(values ...string) []byte {
	h := hmac.New(sha256.New, k.CredentialHMAC)
	for _, value := range values {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum(nil)
}

func (k Keys) Equal(expected []byte, values ...string) bool {
	return hmac.Equal(expected, k.Hash(values...))
}

func (k Keys) Opaque(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(k.random(), raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (k Keys) VerificationCode() (string, error) {
	raw := make([]byte, 4)
	if _, err := io.ReadFull(k.random(), raw); err != nil {
		return "", err
	}
	value := uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
	return fmt.Sprintf("%06d", value%1000000), nil
}

func (k Keys) Seal(value any) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return seal(k.ReplayKey, k.random(), plain)
}

func (k Keys) Open(ciphertext []byte, target any) error {
	plain, err := open(k.ReplayKey, ciphertext)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, target)
}

func (k Keys) SealVirtual(value any) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return seal(k.VirtualKey, k.random(), plain)
}

func (k Keys) OpenVirtual(ciphertext []byte, target any) error {
	plain, err := open(k.VirtualKey, ciphertext)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, target)
}

func seal(key []byte, random io.Reader, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func open(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("ciphertext is too short")
	}
	return gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
}

func (k Keys) CSRF(credentialID uuid.UUID, rawCredential string) string {
	return base64.RawURLEncoding.EncodeToString(k.Hash("csrf", credentialID.String(), rawCredential))
}

type Claims struct {
	Issuer            string   `json:"iss"`
	Subject           string   `json:"sub"`
	SessionID         string   `json:"sid"`
	Roles             []string `json:"roles"`
	PermissionVersion int64    `json:"permission_version"`
	IssuedAt          int64    `json:"iat"`
	ExpiresAt         int64    `json:"exp"`
	TokenID           string   `json:"jti"`
}

func (k Keys) SignAccessToken(userID, sessionID string, roles []string, version int64, ttl time.Duration) (string, time.Time, error) {
	now := k.now()
	expiresAt := now.Add(ttl)
	claims := Claims{
		Issuer:            k.JWTIssuer,
		Subject:           userID,
		SessionID:         sessionID,
		Roles:             roles,
		PermissionVersion: version,
		IssuedAt:          now.Unix(),
		ExpiresAt:         expiresAt.Unix(),
		TokenID:           uuid.NewString(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte("{\"alg\":\"HS256\",\"typ\":\"JWT\"}"))
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	unsigned := header + "." + encodedPayload
	h := hmac.New(sha256.New, k.JWTKey)
	_, _ = h.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil)), expiresAt, nil
}

func (k Keys) VerifyAccessToken(raw string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("malformed JWT")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, errors.New("malformed JWT signature")
	}
	h := hmac.New(sha256.New, k.JWTKey)
	_, _ = h.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, h.Sum(nil)) {
		return Claims{}, errors.New("invalid JWT signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("malformed JWT payload")
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, err
	}
	if claims.Issuer != k.JWTIssuer || claims.Subject == "" || claims.SessionID == "" || claims.ExpiresAt <= k.now().Unix() {
		return Claims{}, fmt.Errorf("expired or invalid JWT")
	}
	return claims, nil
}
