package passwordreset

import "errors"

var (
	ErrNotFound        = errors.New("password reset not found")
	ErrVersionConflict = errors.New("password reset version conflict")
)
