package handler

import (
	"net/http"
	"strings"

	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/auth-service/internal/config"
	authservice "github.com/Medikong/services/services/auth-service/internal/service"
)

const serviceName = config.ServiceName

type Handler struct {
	service authservice.Service
}

func RegisterRoutes(mux *http.ServeMux, service authservice.Service) {
	h := Handler{service: service}
	operationalHandler().Register(mux)
	mux.HandleFunc("POST /auth/signup", h.Signup)
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.HandleFunc("POST /auth/refresh", h.Refresh)
	mux.HandleFunc("POST /auth/logout", h.Logout)
	mux.HandleFunc("POST /internal/auth/sessions/{sessionId}/revoke", h.Revoke)
	mux.HandleFunc("POST /internal/dev/test-token", h.IssueTestToken)
	mux.HandleFunc("POST /auth/introspect", h.Introspect)
}

func (h Handler) Signup(w http.ResponseWriter, r *http.Request) {
	var input authservice.SignupInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.service.Signup(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	logger.Info(r.Context(), "auth.signup.succeeded", "user_id", result.UserID)
	httpapi.WriteJSON(w, http.StatusCreated, result)
}

func (h Handler) Login(w http.ResponseWriter, r *http.Request) {
	var input authservice.LoginInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.service.Login(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	logger.Info(r.Context(), "auth.login.succeeded", "user_id", result.UserID)
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (h Handler) IssueTestToken(w http.ResponseWriter, r *http.Request) {
	var input authservice.TestTokenInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.service.IssueTestToken(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	logger.Info(r.Context(), "auth.test_token.issued", "user_id", result.UserID)
	httpapi.WriteJSON(w, http.StatusCreated, result)
}

func (h Handler) Introspect(w http.ResponseWriter, r *http.Request) {
	p, err := h.service.Introspect(r.Context(), r.Header.Get(headers.Authorization))
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"principal": p})
}

func (h Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var input authservice.RefreshInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.service.Refresh(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	logger.Info(r.Context(), "auth.refresh.succeeded", "user_id", result.UserID)
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (h Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Logout(r.Context(), r.Header.Get(headers.Authorization)); err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Revoke(r.Context(), r.PathValue("sessionId")); err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func mapAuthError(err error) error {
	switch {
	case err == authservice.ErrInvalidSignup:
		return httpapi.BadRequest("auth.invalid_signup", "가입 요청 값이 올바르지 않습니다.")
	case err == authservice.ErrMissingBearerToken:
		return httpapi.Unauthorized("auth.missing_token", "인증 토큰이 필요합니다.")
	case err == authservice.ErrMissingRefreshToken:
		return httpapi.BadRequest("auth.missing_refresh_token", "refreshToken이 필요합니다.")
	case err == authservice.ErrMissingSessionID:
		return httpapi.BadRequest("auth.missing_session_id", "sessionId가 필요합니다.")
	case err == authservice.ErrMissingUserID:
		return httpapi.Unauthorized("auth.missing_user_id", "Principal에 userId가 필요합니다.")
	case strings.Contains(err.Error(), "already exists"):
		return httpapi.Conflict("auth.email_already_exists", "이미 가입된 이메일입니다.")
	case strings.Contains(err.Error(), "invalid credentials"):
		return httpapi.Unauthorized("auth.invalid_credentials", "이메일 또는 비밀번호가 올바르지 않습니다.")
	case strings.Contains(err.Error(), "session not found"):
		return httpapi.Unauthorized("auth.invalid_session", "세션이 없거나 만료되었습니다.")
	default:
		return err
	}
}
