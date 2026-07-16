package domain

import "github.com/Medikong/services/packages/go-platform/httpapi"

func Problem(status int, code, message string) error {
	return httpapi.Error(status, code).
		Public(message).
		New(message)
}

func Unavailable() error {
	return Problem(503, "AUTH_SERVICE_UNAVAILABLE", "인증 서비스를 일시적으로 사용할 수 없습니다.")
}
