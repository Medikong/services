package identity

import "errors"

var (
	ErrNotFound     = errors.New("identity not found")
	ErrConflict     = errors.New("identity already exists")
	ErrInvalidPhone = errors.New("invalid phone number")
)
