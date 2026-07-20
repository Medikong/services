package failure

import "strings"

type Kind string

const (
	KindInvalid         Kind = "invalid"
	KindUnauthenticated Kind = "unauthenticated"
	KindForbidden       Kind = "forbidden"
	KindNotFound        Kind = "not_found"
	KindConflict        Kind = "conflict"
	KindUnavailable     Kind = "unavailable"
)

// Error carries the stable public failure contract without transport details.
type Error struct {
	Kind          Kind
	Code          string
	PublicMessage string
	cause         error
}

func New(kind Kind, code, publicMessage string) *Error {
	return &Error{Kind: kind, Code: strings.TrimSpace(code), PublicMessage: strings.TrimSpace(publicMessage)}
}

func Wrap(kind Kind, code, publicMessage string, cause error) *Error {
	err := New(kind, code, publicMessage)
	err.cause = cause
	return err
}

func Invalid(code, publicMessage string) *Error {
	return New(KindInvalid, code, publicMessage)
}

func Unauthenticated(code, publicMessage string) *Error {
	return New(KindUnauthenticated, code, publicMessage)
}

func Forbidden(code, publicMessage string) *Error {
	return New(KindForbidden, code, publicMessage)
}

func NotFound(code, publicMessage string) *Error {
	return New(KindNotFound, code, publicMessage)
}

func Conflict(code, publicMessage string) *Error {
	return New(KindConflict, code, publicMessage)
}

func Unavailable(code, publicMessage string) *Error {
	return New(KindUnavailable, code, publicMessage)
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return e.Code + ": " + e.cause.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
