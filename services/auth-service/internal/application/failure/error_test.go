package failure

import (
	"errors"
	"testing"
)

func TestConstructorsPreservePublicContract(t *testing.T) {
	tests := []struct {
		name string
		err  *Error
		kind Kind
	}{
		{name: "invalid", err: Invalid("AUTH_INPUT_INVALID", "입력값을 확인해주세요."), kind: KindInvalid},
		{name: "unauthenticated", err: Unauthenticated("AUTH_SESSION_REQUIRED", "인증 정보가 필요합니다."), kind: KindUnauthenticated},
		{name: "forbidden", err: Forbidden("AUTH_FORBIDDEN", "이 작업을 수행할 수 없습니다."), kind: KindForbidden},
		{name: "not found", err: NotFound("AUTH_NOT_FOUND", "대상을 찾을 수 없습니다."), kind: KindNotFound},
		{name: "conflict", err: Conflict("AUTH_CONFLICT", "현재 상태와 충돌합니다."), kind: KindConflict},
		{name: "unavailable", err: Unavailable("AUTH_SERVICE_UNAVAILABLE", "잠시 뒤 다시 시도해주세요."), kind: KindUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var failureErr *Error
			if !errors.As(test.err, &failureErr) {
				t.Fatal("error is not a typed failure")
			}
			if failureErr.Kind != test.kind || failureErr.Code == "" || failureErr.PublicMessage == "" {
				t.Fatalf("failure = %#v", failureErr)
			}
		})
	}
}

func TestWrapPreservesCause(t *testing.T) {
	cause := errors.New("storage unavailable")
	err := Wrap(KindUnavailable, "AUTH_SERVICE_UNAVAILABLE", "잠시 뒤 다시 시도해주세요.", cause)
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause was not preserved")
	}
}
