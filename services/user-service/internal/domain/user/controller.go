package user

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
)

type Controller struct {
	service Service
}

func NewController(service Service) Controller {
	return Controller{service: service}
}

func (c Controller) RegisterRoutes(r chi.Router) {
	r.Post("/v1/internal/users/ensure", c.Ensure)
	r.Route("/v1/users", func(r chi.Router) {
		r.Get("/me", c.Me)
		r.Patch("/me/profile", c.UpdateMyProfile)
		r.Get("/{userId}", c.Get)
	})
}

func (c Controller) Ensure(w http.ResponseWriter, r *http.Request) {
	var input EnsureInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	user, err := c.service.Ensure(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func (c Controller) Me(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	user, err := c.service.Me(r.Context(), p)
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func (c Controller) Get(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	user, err := c.service.Get(r.Context(), p, chi.URLParam(r, "userId"))
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func (c Controller) UpdateMyProfile(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	var input ProfileUpdate
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	user, err := c.service.UpdateMyProfile(r.Context(), p, input)
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, user)
}

func mapControllerError(err error) error {
	switch err {
	case ErrMissingUserID:
		return httpapi.BadRequest("user.missing_user_id", "userId가 필요합니다.")
	case ErrUnauthorized:
		return httpapi.Unauthorized("auth.unauthorized", "인증이 필요합니다.")
	case ErrForbidden:
		return httpapi.Forbidden("auth.forbidden", "권한이 부족합니다.")
	case ErrInvalidProfile:
		return httpapi.BadRequest("user.invalid_profile", "프로필 값이 올바르지 않습니다.")
	default:
		return err
	}
}
