package http

import (
	"errors"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/auth-service/internal/account"
	"github.com/Medikong/services/services/auth-service/internal/autherror"
	"github.com/Medikong/services/services/auth-service/internal/session"
)

func mapAuthError(err error) error {
	switch {
	case errors.Is(err, account.ErrInvalidSignup):
		return httpapi.BadRequest("auth.invalid_signup", "가입 요청 값이 올바르지 않습니다.")
	case errors.Is(err, session.ErrMissingBearerToken):
		return httpapi.Unauthorized("auth.missing_token", "인증 토큰이 필요합니다.")
	case errors.Is(err, session.ErrMissingRefreshToken):
		return httpapi.BadRequest("auth.missing_refresh_token", "refreshToken이 필요합니다.")
	case errors.Is(err, session.ErrMissingSessionID):
		return httpapi.BadRequest("auth.missing_session_id", "sessionId가 필요합니다.")
	case errors.Is(err, session.ErrMissingUserID):
		return httpapi.Unauthorized("auth.missing_user_id", "Principal에 userId가 필요합니다.")
	case errors.Is(err, autherror.ErrAlreadyExists):
		return httpapi.Conflict("auth.email_already_exists", "이미 가입된 이메일입니다.")
	case errors.Is(err, autherror.ErrInvalidCredentials):
		return httpapi.Unauthorized("auth.invalid_credentials", "이메일 또는 비밀번호가 올바르지 않습니다.")
	case errors.Is(err, autherror.ErrSessionNotFound):
		return httpapi.Unauthorized("auth.invalid_session", "세션이 없거나 만료되었습니다.")
	default:
		return err
	}
}
