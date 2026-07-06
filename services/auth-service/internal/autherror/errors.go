package autherror

import "errors"

var (
	ErrAlreadyExists      = errors.New("auth account already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionNotFound    = errors.New("session not found")
)
