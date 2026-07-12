package application

import "errors"

// Error is intentionally free of driver details. Controllers convert it into
// OpenAPI ProblemDetails.
type Error struct {
	Status    int
	Code      string
	Detail    string
	Retryable bool
}

func (e *Error) Error() string {
	return e.Code
}

func Problem(status int, code, detail string) *Error {
	return &Error{Status: status, Code: code, Detail: detail}
}

func Unavailable() *Error {
	return &Error{
		Status:    503,
		Code:      "AUTH_SERVICE_UNAVAILABLE",
		Detail:    "인증 서비스를 일시적으로 사용할 수 없습니다.",
		Retryable: true,
	}
}

func AsError(err error) *Error {
	var appError *Error
	if errors.As(err, &appError) {
		return appError
	}
	return Unavailable()
}
