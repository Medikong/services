package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

func transportError(err error) error {
	if err == nil {
		return nil
	}
	var contractError *httpcontract.Error
	if errors.As(err, &contractError) {
		return contractError
	}
	code := ""
	if value, ok := oops.AsOops(err); ok {
		code = fmt.Sprint(value.Code())
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return dependencyUnavailable(err)
	case hasErrorCode(err, "read_model_query_invalid", "read_model_cursor_invalid"):
		return httpcontract.InputInvalid("query", "invalid_query_parameter")
	case hasAny(code, "not_found"):
		return notFound(err)
	case hasAny(code, "version_conflict"):
		return &httpcontract.Error{
			Status: http.StatusConflict, Code: "COUPON_VERSION_CONFLICT",
			Title: "쿠폰 상태가 이미 변경되었습니다.", Detail: "최신 상태를 조회한 뒤 다시 요청해 주세요.", Cause: err,
		}
	case hasAny(code, "idempotency_conflict", "payload_conflict"):
		return &httpcontract.Error{
			Status: http.StatusConflict, Code: "COUPON_IDEMPOTENCY_CONFLICT",
			Title: "멱등 키가 다른 요청에 사용되었습니다.", Detail: "새 멱등 키로 다시 요청해 주세요.", Cause: err,
		}
	case hasAny(code, "command_in_progress", "transition_invalid", "already_leased", "lease_lost", "state_conflict"):
		return &httpcontract.Error{
			Status: http.StatusConflict, Code: "COUPON_STATE_CONFLICT",
			Title: "현재 쿠폰 상태에서는 요청을 처리할 수 없습니다.", Detail: "최신 처리 상태를 확인해 주세요.", Cause: err,
		}
	case hasAny(code, "operational_stop"):
		return &httpcontract.Error{
			Status: http.StatusConflict, Code: "COUPON_OPERATION_STOPPED",
			Title: "쿠폰 처리가 운영 정책에 따라 중지되었습니다.", Detail: "운영 안내를 확인해 주세요.", Cause: err,
		}
	case hasAny(code, "rate_limited"):
		return &httpcontract.Error{
			Status: http.StatusTooManyRequests, Code: "COUPON_RATE_LIMITED",
			Title: "쿠폰 요청이 잠시 제한되었습니다.", Detail: "잠시 뒤 다시 시도해 주세요.", Retryable: true, Cause: err,
		}
	case hasErrorCode(err, "forbidden", "approval_rejected", "case_rejected"):
		return &httpcontract.Error{
			Status: http.StatusForbidden, Code: "COUPON_FORBIDDEN",
			Title: "요청 권한 또는 승인 근거가 없습니다.", Detail: "승인·문의 참조와 호출 권한을 확인해 주세요.", Cause: err,
		}
	case isDependencyCode(code):
		return dependencyUnavailable(err)
	default:
		return &httpcontract.Error{
			Status: http.StatusUnprocessableEntity, Code: "COUPON_BUSINESS_RULE_REJECTED",
			Title: "쿠폰 업무 규칙에 따라 요청이 거절되었습니다.", Detail: "현재 상태와 쿠폰 조건을 확인해 주세요.", Cause: err,
		}
	}
}

func notFound(cause error) *httpcontract.Error {
	return &httpcontract.Error{
		Status: http.StatusNotFound, Code: "COUPON_NOT_FOUND",
		Title: "요청한 쿠폰 정보를 찾을 수 없습니다.", Detail: "식별자와 접근 범위를 확인해 주세요.", Cause: cause,
	}
}

func hasErrorCode(err error, candidates ...string) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if value, ok := oops.AsOops(current); ok && hasAny(fmt.Sprint(value.Code()), candidates...) {
			return true
		}
	}
	return false
}

func dependencyUnavailable(cause error) *httpcontract.Error {
	return &httpcontract.Error{
		Status: http.StatusServiceUnavailable, Code: "COUPON_DEPENDENCY_UNAVAILABLE",
		Title: "필수 의존성을 사용할 수 없습니다.", Detail: "잠시 뒤 다시 시도해 주세요.",
		Retryable: true, Cause: cause,
	}
}

func isDependencyCode(code string) bool {
	return hasAny(code,
		"database_", "_database", "dependency_", "_failed", "payload_load",
		"schema_outdated", "schema_unavailable", "pool_required", "redis_", "outbox_", "inbox_",
	)
}

func hasAny(value string, candidates ...string) bool {
	value = strings.ToLower(value)
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
