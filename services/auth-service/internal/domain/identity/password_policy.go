package identity

import "errors"

// PasswordPolicy is the domain rule for accepting a new password.
type PasswordPolicy struct {
	MinimumLength int
}

func (p PasswordPolicy) Validate(password string) error {
	minimumLength := p.MinimumLength
	if minimumLength <= 0 {
		minimumLength = 12
	}
	if len(password) < minimumLength {
		return errors.New("password does not meet the minimum length")
	}
	return nil
}
