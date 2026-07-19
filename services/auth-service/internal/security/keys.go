// Package security owns opaque credential, replay-payload, and JWT
// cryptography used by auth application services. It never logs or persists
// plaintext credentials.
package security

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
)

var strictRawURLEncoding = base64.RawURLEncoding.Strict()

type Keys struct {
	CredentialHMAC []byte
	ReplayKey      []byte
	JWTKey         []byte
	JWTKeyID       string
	JWTIssuer      string
	JWTAudiences   []string
	JWTVerifyKeys  map[string]*rsa.PublicKey
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
	if _, err := parseRSAPrivateKey(k.JWTKey); err != nil {
		return fmt.Errorf("JWT private key is invalid: %w", err)
	}
	if strings.TrimSpace(k.JWTKeyID) == "" || strings.TrimSpace(k.JWTIssuer) == "" || len(k.JWTAudiences) == 0 {
		return errors.New("JWT key ID, issuer, and audience are required")
	}
	for keyID, publicKey := range k.JWTVerifyKeys {
		if strings.TrimSpace(keyID) == "" || keyID == k.JWTKeyID || publicKey == nil {
			return errors.New("retiring JWT keys require a distinct key ID and RSA public key")
		}
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
	Issuer    string   `json:"iss"`
	Subject   string   `json:"sub"`
	SessionID string   `json:"sid"`
	Audience  []string `json:"aud"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
	TokenID   string   `json:"jti"`
}

func (k Keys) SignAccessToken(userID, sessionID string, ttl time.Duration) (string, time.Time, error) {
	if !isNonZeroUUID(userID) || !isNonZeroUUID(sessionID) {
		return "", time.Time{}, oops.In("auth_security").Code("jwt.identifier_invalid").
			New("access token subject and session ID must be non-zero UUIDs")
	}
	privateKey, err := parseRSAPrivateKey(k.JWTKey)
	if err != nil {
		return "", time.Time{}, err
	}
	now := k.now()
	expiresAt := now.Add(ttl)
	claims := Claims{
		Issuer: k.JWTIssuer, Subject: userID, SessionID: sessionID,
		Audience: append([]string(nil), k.JWTAudiences...),
		IssuedAt: now.Unix(), ExpiresAt: expiresAt.Unix(), TokenID: uuid.NewString(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, err
	}
	headerJSON, err := json.Marshal(map[string]string{"alg": "RS256", "kid": k.JWTKeyID, "typ": "JWT"})
	if err != nil {
		return "", time.Time{}, err
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	unsigned := header + "." + encodedPayload
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(k.random(), privateKey, cryptoHashSHA256, digest[:])
	if err != nil {
		return "", time.Time{}, err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), expiresAt, nil
}

func (k Keys) VerifyAccessToken(raw string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("malformed JWT")
	}
	headerPayload, err := strictRawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, errors.New("malformed JWT header")
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerPayload, &header); err != nil || header.Algorithm != "RS256" || header.Type != "JWT" || header.KeyID == "" {
		return Claims{}, errors.New("invalid JWT protected header")
	}
	publicKey, err := k.verificationKey(header.KeyID)
	if err != nil {
		return Claims{}, err
	}
	signature, err := strictRawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, errors.New("malformed JWT signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(publicKey, cryptoHashSHA256, digest[:], signature); err != nil {
		return Claims{}, errors.New("invalid JWT signature")
	}
	payload, err := strictRawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("malformed JWT payload")
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, err
	}
	now := k.now().Unix()
	if claims.Issuer != k.JWTIssuer || !isNonZeroUUID(claims.Subject) || !isNonZeroUUID(claims.SessionID) || !isNonZeroUUID(claims.TokenID) || claims.IssuedAt > now+30 || claims.ExpiresAt <= now || claims.ExpiresAt <= claims.IssuedAt || !containsAudience(claims.Audience, k.JWTAudiences) {
		return Claims{}, errors.New("expired or invalid JWT")
	}
	return claims, nil
}

func isNonZeroUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil
}

const cryptoHashSHA256 = crypto.SHA256

func parseRSAPrivateKey(value []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(value)
	if block == nil {
		return nil, errors.New("PEM block is required")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		privateKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key must be RSA")
		}
		return privateKey, privateKey.Validate()
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return privateKey, privateKey.Validate()
}

func ParseRSAPublicKeyPEM(value []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(value)
	if block == nil {
		return nil, errors.New("PEM block is required")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err == nil {
		publicKey, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("public key must be RSA")
		}
		return publicKey, nil
	}
	publicKey, pkcs1Err := x509.ParsePKCS1PublicKey(block.Bytes)
	if pkcs1Err != nil {
		return nil, err
	}
	return publicKey, nil
}

func (k Keys) verificationKey(keyID string) (*rsa.PublicKey, error) {
	if keyID == k.JWTKeyID {
		privateKey, err := parseRSAPrivateKey(k.JWTKey)
		if err != nil {
			return nil, err
		}
		return &privateKey.PublicKey, nil
	}
	if key := k.JWTVerifyKeys[keyID]; key != nil {
		return key, nil
	}
	return nil, errors.New("unknown JWT key ID")
}

func containsAudience(actual, allowed []string) bool {
	for _, candidate := range actual {
		for _, expected := range allowed {
			if candidate == expected {
				return true
			}
		}
	}
	return false
}

type JWK struct {
	KeyType   string `json:"kty"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

type JWKSet struct {
	Keys []JWK `json:"keys"`
}

func (k Keys) JWKS() (JWKSet, error) {
	active, err := k.verificationKey(k.JWTKeyID)
	if err != nil {
		return JWKSet{}, err
	}
	result := JWKSet{Keys: []JWK{rsaJWK(k.JWTKeyID, active)}}
	keyIDs := make([]string, 0, len(k.JWTVerifyKeys))
	for keyID, publicKey := range k.JWTVerifyKeys {
		if keyID != k.JWTKeyID && publicKey != nil {
			keyIDs = append(keyIDs, keyID)
		}
	}
	sort.Strings(keyIDs)
	for _, keyID := range keyIDs {
		result.Keys = append(result.Keys, rsaJWK(keyID, k.JWTVerifyKeys[keyID]))
	}
	return result, nil
}

func rsaJWK(keyID string, key *rsa.PublicKey) JWK {
	exponent := make([]byte, 4)
	binary.BigEndian.PutUint32(exponent, uint32(key.E))
	for len(exponent) > 1 && exponent[0] == 0 {
		exponent = exponent[1:]
	}
	return JWK{
		KeyType: "RSA", Use: "sig", Algorithm: "RS256", KeyID: keyID,
		Modulus:  base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		Exponent: base64.RawURLEncoding.EncodeToString(exponent),
	}
}
