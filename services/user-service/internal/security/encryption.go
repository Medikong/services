package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

type Sealer struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewSealer(encodedKey string, random io.Reader) (Sealer, error) {
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil || len(key) != 32 {
		return Sealer{}, errors.New("private-name encryption key must be 32 bytes encoded as raw base64")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Sealer{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Sealer{}, err
	}
	if random == nil {
		random = rand.Reader
	}
	return Sealer{aead: aead, random: random}, nil
}

func (s Sealer) Seal(plain string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}
