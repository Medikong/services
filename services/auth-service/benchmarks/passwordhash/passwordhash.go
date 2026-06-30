package passwordhash

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const LegacyPasswordScheme = "pbkdf2_sha256"

func DeriveLegacyPBKDF2(password string, salt []byte, iterations int) ([]byte, error) {
	if iterations < 1 {
		return nil, fmt.Errorf("iterations must be positive: %d", iterations)
	}
	return pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
}

func VerifyLegacyPBKDF2(password string, passwordHash string) (bool, error) {
	scheme, iterations, salt, expected, err := ParseLegacyPBKDF2(passwordHash)
	if err != nil {
		return false, err
	}
	if scheme != LegacyPasswordScheme {
		return false, fmt.Errorf("unsupported password hash scheme: %s", scheme)
	}

	actual, err := DeriveLegacyPBKDF2(password, salt, iterations)
	if err != nil {
		return false, err
	}
	return hmac.Equal(actual, expected), nil
}

func ParseLegacyPBKDF2(passwordHash string) (string, int, []byte, []byte, error) {
	parts := strings.Split(passwordHash, "$")
	if len(parts) != 4 {
		return "", 0, nil, nil, errors.New("password hash must contain scheme, iterations, salt, and digest")
	}

	iterations, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, nil, nil, fmt.Errorf("invalid password hash iterations: %w", err)
	}

	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", 0, nil, nil, fmt.Errorf("invalid password hash salt: %w", err)
	}

	expected, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return "", 0, nil, nil, fmt.Errorf("invalid password hash digest: %w", err)
	}

	return parts[0], iterations, salt, expected, nil
}
