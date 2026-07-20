package intent

import (
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
)

const unavailableMessage = "인증 서비스를 일시적으로 사용할 수 없습니다."

func unavailable(cause error) error {
	return failure.Wrap(failure.KindUnavailable, "AUTH_SERVICE_UNAVAILABLE", unavailableMessage, cause)
}

func preserveFailure(err error) error {
	var typed *failure.Error
	if errors.As(err, &typed) {
		return err
	}
	return unavailable(err)
}
