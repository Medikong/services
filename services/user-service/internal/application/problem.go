package application

import (
	"errors"
	"net/http"
)

type Problem struct {
	Status  int
	Code    string
	Message string
	Cause   error
}

func (p *Problem) Error() string {
	if p.Cause != nil {
		return p.Cause.Error()
	}
	return p.Code
}

func (p *Problem) Unwrap() error {
	return p.Cause
}

func NewProblem(status int, code, message string, cause error) error {
	return &Problem{Status: status, Code: code, Message: message, Cause: cause}
}

func AsProblem(err error) *Problem {
	var problem *Problem
	if errors.As(err, &problem) {
		return problem
	}
	return &Problem{
		Status:  http.StatusInternalServerError,
		Code:    "common.internal",
		Message: "요청 처리 중 오류가 발생했습니다.",
		Cause:   err,
	}
}
