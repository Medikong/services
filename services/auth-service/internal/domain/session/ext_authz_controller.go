package session

import (
	"net/http"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
)

type ExtAuthzController struct {
	keys       security.Keys
	projection *StatusProjection
}

func NewExtAuthzController(keys security.Keys, projection *StatusProjection) *ExtAuthzController {
	return &ExtAuthzController{keys: keys, projection: projection}
}

func (c *ExtAuthzController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		httputil.WriteError(w, r, domain.Problem(http.StatusUnauthorized, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다."))
		return
	}
	claims, err := c.keys.VerifyAccessToken(parts[1])
	if err != nil {
		httputil.WriteError(w, r, domain.Problem(http.StatusUnauthorized, "AUTH_SESSION_REQUIRED", "유효한 인증 정보가 필요합니다."))
		return
	}
	allowed, err := c.projection.Check(r.Context(), claims)
	if err != nil {
		httputil.WriteError(w, r, domain.Unavailable())
		return
	}
	if !allowed {
		httputil.WriteError(w, r, domain.Problem(http.StatusUnauthorized, "AUTH_SESSION_REVOKED", "Session을 사용할 수 없습니다."))
		return
	}
	w.Header().Set("X-User-Id", claims.Subject)
	w.Header().Set("X-Session-Id", claims.SessionID)
	w.Header().Set("X-Token-Id", claims.TokenID)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
