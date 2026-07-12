package security

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

type PasswordPolicy struct {
	MinimumLength int
}

func (p PasswordPolicy) Validate(password string) error {
	if p.MinimumLength <= 0 {
		p.MinimumLength = 12
	}
	if len(password) < p.MinimumLength {
		return errors.New("password does not meet the minimum length")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
