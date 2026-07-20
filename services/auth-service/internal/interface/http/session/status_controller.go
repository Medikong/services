package session

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
)

const statusRequestTimeout = 200 * time.Millisecond

type StatusController struct {
	service *applicationsession.StatusService
}

func NewStatusController(service *applicationsession.StatusService) *StatusController {
	return &StatusController{service: service}
}

func (c *StatusController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	raw, ok := statusBearer(r)
	if !ok {
		httputil.WriteError(w, r, failure.Unauthenticated("AUTH_SESSION_REQUIRED", statusRequiredMessage))
		return
	}
	if c == nil || c.service == nil {
		httputil.WriteError(w, r, failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", statusUnavailableMessage))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), statusRequestTimeout)
	defer cancel()
	identity, err := c.service.Validate(ctx, raw)
	if ctx.Err() != nil {
		httputil.WriteError(w, r, failure.Unavailable("AUTH_SERVICE_UNAVAILABLE", statusUnavailableMessage))
		return
	}
	if err != nil {
		httputil.WriteError(w, r, err)
		return
	}
	w.Header().Set("X-User-Id", identity.UserID.String())
	w.Header().Set("X-Session-Id", identity.SessionID.String())
	w.Header().Set("X-Token-Id", identity.TokenID)
	w.WriteHeader(http.StatusOK)
}

const (
	statusRequiredMessage    = "인증 정보를 확인한 뒤 다시 시도해주세요."
	statusUnavailableMessage = "인증 서비스를 일시적으로 사용할 수 없습니다."
)

func statusBearer(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
