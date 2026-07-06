package session

import (
	"net/http"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
)

var ErrMissingBearerToken = oops.Code("auth.missing_token").
	Public("인증 토큰이 필요합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrInvalidAuthorizationHeader = oops.Code("auth.invalid_authorization_header").
	Public("Authorization 헤더는 Bearer 토큰이어야 합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrInvalidToken = oops.Code("auth.invalid_token").
	Public("인증 토큰이 올바르지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrTokenExpired = oops.Code("auth.token_expired").
	Public("인증 토큰이 만료되었습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrTokenRevoked = oops.Code("auth.token_revoked").
	Public("인증 토큰이 폐기되었습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrMissingRefreshToken = oops.Code("auth.missing_refresh_token").
	Public("refreshToken이 필요합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrInvalidRefreshToken = oops.Code("auth.invalid_refresh_token").
	Public("refresh token이 없거나 유효하지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrMissingSessionID = oops.Code("auth.missing_session_id").
	Public("sessionId가 필요합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrMissingUserID = oops.Code("auth.missing_user_id").
	Public("Principal에 userId가 필요합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrSessionNotFound = oops.Code("auth.invalid_session").
	Public("세션이 없거나 만료되었습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrInvalidRole = oops.Code("auth.invalid_role").
	Public("role 값이 올바르지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrInvalidTokenConfig = oops.Code("auth.invalid_token_config").
	Public("JWT 설정이 올바르지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusInternalServerError)

var ErrInternal = oops.Code("common.internal").
	Public("요청 처리 중 오류가 발생했습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusInternalServerError)
