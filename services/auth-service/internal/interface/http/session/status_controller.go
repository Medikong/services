package session

import (
	"net/http"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
)

type StatusController struct {
	service *applicationsession.StatusService
}

func NewStatusController(service *applicationsession.StatusService) *StatusController {
	return &StatusController{service: service}
}

func (c *StatusController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		httputil.WriteError(w, r, failure.Unauthenticated("AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다."))
		return
	}
	identity, err := c.service.Validate(r.Context(), parts[1])
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	w.Header().Set("X-User-Id", identity.UserID.String())
	w.Header().Set("X-Session-Id", identity.SessionID.String())
	w.Header().Set("X-Token-Id", identity.TokenID)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
