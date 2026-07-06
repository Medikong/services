package session

import (
	"net/http"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
)

var ErrMissingBearerToken = oops.Code("auth.missing_token").
	Public("인증 토큰이 필요합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrMissingRefreshToken = oops.Code("auth.missing_refresh_token").
	Public("refreshToken이 필요합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrMissingSessionID = oops.Code("auth.missing_session_id").
	Public("sessionId가 필요합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrMissingUserID = oops.Code("auth.missing_user_id").
	Public("Principal에 userId가 필요합니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrSessionNotFound = oops.Code("auth.invalid_session").
	Public("세션이 없거나 만료되었습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrInternal = oops.Code("common.internal").
	Public("요청 처리 중 오류가 발생했습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusInternalServerError)
