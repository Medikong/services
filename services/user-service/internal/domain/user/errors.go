package user

import (
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
)

var (
	ErrNotFound = httpapi.Error(404, "USER_NOT_FOUND").
			In("user").Public("사용자를 찾을 수 없습니다.").New("user not found")
	ErrAccountNotActive = httpapi.Error(409, "USER_ACCOUNT_NOT_ACTIVE").
				In("user").Public("활성 상태의 사용자만 프로필을 변경할 수 있습니다.").New("user account is not active")
	ErrVersionConflict = httpapi.Error(409, "USER_VERSION_CONFLICT").
				In("user").Public("사용자 정보가 다른 요청에서 먼저 변경되었습니다.").New("user version conflict")
	ErrIdempotencyConflict = httpapi.Error(409, "USER_IDEMPOTENCY_CONFLICT").
				In("user").Public("같은 멱등 키에 다른 요청이 사용되었습니다.").New("user idempotency conflict")
	ErrRegistrationConflict = httpapi.Error(409, "USER_REGISTRATION_CONFLICT").
				In("user").Public("같은 registrationId에 다른 가입 요청이 이미 처리되었습니다.").New("user registration conflict")
	ErrTransitionInvalid = httpapi.Error(409, "USER_ACCOUNT_STATUS_TRANSITION_INVALID").
				In("user").Public("허용되지 않은 계정 상태 전이입니다.").New("user status transition invalid")
	ErrInputInvalid = httpapi.Error(422, "USER_INPUT_INVALID").
			In("user").Public("요청 값이 올바르지 않습니다.").New("user input is invalid")
	ErrProfilePolicyViolation = httpapi.Error(422, "USER_PROFILE_POLICY_VIOLATION").
					In("user").Public("프로필 정책을 만족하지 않습니다.").New("user profile policy is violated")
	ErrRegistrationProofInvalid = httpapi.Error(403, "USER_REGISTRATION_PROOF_INVALID").
					In("user").Public("가입 검증 증거가 유효하지 않습니다.").New("registration proof is invalid")
	ErrRequiredAgreementInvalid = httpapi.Error(422, "USER_REQUIRED_AGREEMENT_INVALID").
					In("user").Public("필수 동의 항목이나 버전이 올바르지 않습니다.").New("required agreement is invalid")
	ErrProfileMediaProofInvalid = httpapi.Error(403, "USER_PROFILE_MEDIA_PROOF_INVALID").
					In("user").Public("프로필 이미지 자산 증거가 유효하지 않습니다.").New("profile media proof is invalid")
)

func categorizedError(category error, cause error) error {
	if cause == nil {
		return category
	}
	return oops.In("user_service").With("cause", cause.Error()).Wrap(category)
}

func inputError(cause error) error {
	return categorizedError(ErrInputInvalid, cause)
}

func profilePolicyError(cause error) error {
	return categorizedError(ErrProfilePolicyViolation, cause)
}
