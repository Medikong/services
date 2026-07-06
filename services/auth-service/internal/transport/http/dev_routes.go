package http

import (
	nethttp "net/http"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/auth-service/internal/domain/dev"
)

func (h Handler) IssueTestToken(w nethttp.ResponseWriter, r *nethttp.Request) {
	var input dev.TestTokenInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.services.Dev.IssueTestToken(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	logger.Info(r.Context(), "auth.test_token.issued", "user_id", result.UserID)
	httpapi.WriteJSON(w, nethttp.StatusCreated, result)
}
