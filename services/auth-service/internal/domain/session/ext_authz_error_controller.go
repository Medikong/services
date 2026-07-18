package session

import (
	"net/http"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
)

func writeExtAuthzUnauthorized(w http.ResponseWriter, r *http.Request) {
	err := httpapi.Unauthorized("AUTH_SESSION_REQUIRED").
		Public("인증 정보를 확인한 뒤 다시 시도해주세요.").
		New("ext_authz rejected request")
	httputil.WriteError(w, r, err)
}

func writeExtAuthzUnavailable(w http.ResponseWriter, r *http.Request) {
	err := httpapi.Error(http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE").
		Public("인증 서비스를 일시적으로 사용할 수 없습니다.").
		New("ext_authz dependency state is unavailable")
	httputil.WriteError(w, r, err)
}
