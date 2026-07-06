package dev

import (
	"net/http"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
)

var ErrInternal = oops.Code("common.internal").
	Public("요청 처리 중 오류가 발생했습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusInternalServerError)

var ErrDisabled = oops.Code("auth.dev_test_token_disabled").
	Public("dev test-token endpoint가 비활성화되어 있습니다.").
	With(httpapi.OopsHTTPStatusCodeKey, http.StatusForbidden)
