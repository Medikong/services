package reauth

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

func deliveryExpired() error {
	return failure.Conflict("AUTH_SESSION_DELIVERY_EXPIRED", "Session credential 전달 복구 시간이 만료되었습니다.")
}

func invalidProof() error {
	return failure.Conflict("AUTH_REAUTHENTICATION_PROOF_INVALID", "재인증 권한이 만료되었거나 이미 사용되었습니다.")
}
