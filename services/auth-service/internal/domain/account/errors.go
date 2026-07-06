package account

import (
	"net/http"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/samber/oops"
)

var ErrInvalidSignup = oops.Code("auth.invalid_signup").
	Public("가입 요청 값이 올바르지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrInvalidLogin = oops.Code("auth.invalid_login").
	Public("로그인 요청 값이 올바르지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrInvalidEmail = oops.Code("auth.invalid_email").
	Public("이메일 형식이 올바르지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrInvalidPassword = oops.Code("auth.invalid_password").
	Public("비밀번호 형식이 올바르지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusBadRequest)

var ErrEmailAlreadyExists = oops.Code("auth.email_already_exists").
	Public("이미 가입된 이메일입니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusConflict)

var ErrInvalidCredentials = oops.Code("auth.invalid_credentials").
	Public("이메일 또는 비밀번호가 올바르지 않습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusUnauthorized)

var ErrInternal = oops.Code("common.internal").
	Public("요청 처리 중 오류가 발생했습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusInternalServerError)
