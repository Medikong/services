package registration

import "errors"

var (
	ErrNotFound        = errors.New("registration not found")
	ErrVersionConflict = errors.New("registration version conflict")
)
