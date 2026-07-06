package http

import (
	nethttp "net/http"

	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/auth-service/internal/session"
)

func (h Handler) Refresh(w nethttp.ResponseWriter, r *nethttp.Request) {
	var input session.RefreshInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.services.Sessions.Refresh(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	logger.Info(r.Context(), "auth.refresh.succeeded", "user_id", result.UserID)
	httpapi.WriteJSON(w, nethttp.StatusOK, result)
}

func (h Handler) Logout(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := h.services.Sessions.Logout(r.Context(), r.Header.Get(headers.Authorization)); err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	httpapi.WriteJSON(w, nethttp.StatusOK, map[string]string{"status": "logged_out"})
}

func (h Handler) Revoke(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := h.services.Sessions.Revoke(r.Context(), r.PathValue("sessionId")); err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	httpapi.WriteJSON(w, nethttp.StatusOK, map[string]string{"status": "revoked"})
}
