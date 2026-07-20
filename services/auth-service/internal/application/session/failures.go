package session

import (
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
)

const unavailableMessage = "인증 서비스를 일시적으로 사용할 수 없습니다."

func unavailable(cause error) error {
	if cause == nil {
		return failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", unavailableMessage)
	}
	var typed *failure.Error
	if errors.As(cause, &typed) {
		return cause
	}
	return failure.Wrap(failure.KindUnavailable, "AUTH_SERVICE_UNAVAILABLE", unavailableMessage, cause)
}

func invalid(code, message string) error {
	return failure.Invalid(code, message)
}

func unauthenticated(code, message string) error {
	return failure.Unauthenticated(code, message)
}

func forbidden(code, message string) error {
	return failure.Forbidden(code, message)
}

func conflict(code, message string) error {
	return failure.Conflict(code, message)
}
