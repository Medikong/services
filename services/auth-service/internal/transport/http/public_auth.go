package http

import (
	nethttp "net/http"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/auth-service/internal/domain/account"
)

func (h Handler) Signup(w nethttp.ResponseWriter, r *nethttp.Request) {
	var input account.SignupInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.services.Accounts.Signup(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	logger.Info(r.Context(), "auth.signup.succeeded", "user_id", result.UserID)
	httpapi.WriteJSON(w, nethttp.StatusCreated, result)
}

func (h Handler) Login(w nethttp.ResponseWriter, r *nethttp.Request) {
	var input account.LoginInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.services.Accounts.Login(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	logger.Info(r.Context(), "auth.login.succeeded", "user_id", result.UserID)
	httpapi.WriteJSON(w, nethttp.StatusOK, result)
}
