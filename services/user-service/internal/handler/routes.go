package handler

import (
	"net/http"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/user-service/internal/config"
	"github.com/Medikong/services/services/user-service/internal/model"
	userservice "github.com/Medikong/services/services/user-service/internal/service"
)

const serviceName = config.ServiceName

type Handler struct {
	service userservice.Service
}

func RegisterRoutes(mux *http.ServeMux, service userservice.Service) {
	h := Handler{service: service}
	operationalHandler().Register(mux)
	mux.HandleFunc("POST /internal/users/ensure", h.Ensure)
	mux.HandleFunc("GET /users/me", h.Me)
	mux.HandleFunc("PATCH /users/me/profile", h.UpdateMyProfile)
	mux.HandleFunc("GET /users/{userId}", h.Get)
}

func (h Handler) Ensure(w http.ResponseWriter, r *http.Request) {
	var input userservice.EnsureInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	user, err := h.service.Ensure(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapUserError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func (h Handler) Me(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	user, err := h.service.Me(r.Context(), p)
	if err != nil {
		httpapi.WriteError(w, r, mapUserError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	user, err := h.service.Get(r.Context(), p, r.PathValue("userId"))
	if err != nil {
		httpapi.WriteError(w, r, mapUserError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func (h Handler) UpdateMyProfile(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	var input model.ProfileUpdate
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	user, err := h.service.UpdateMyProfile(r.Context(), p, input)
	if err != nil {
		httpapi.WriteError(w, r, mapUserError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func mapUserError(err error) error {
	switch err {
	case userservice.ErrMissingUserID:
		return httpapi.BadRequest("user.missing_user_id", "userId가 필요합니다.")
	case userservice.ErrUnauthorized:
		return httpapi.Unauthorized("auth.unauthorized", "인증이 필요합니다.")
	case userservice.ErrForbidden:
		return httpapi.Forbidden("auth.forbidden", "권한이 부족합니다.")
	case userservice.ErrInvalidProfile:
		return httpapi.BadRequest("user.invalid_profile", "프로필 값이 올바르지 않습니다.")
	default:
		return err
	}
}
