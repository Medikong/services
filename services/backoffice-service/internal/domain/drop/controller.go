package drop

import (
	"errors"
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
	r.Route("/v1/admin/drops", func(r chi.Router) {
		r.Post("/prepare", c.PrepareDrop)
		r.Get("/{dropId}/readiness", c.Readiness)
	})
}

func (c Controller) PrepareDrop(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	var input PrepareDropInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	readiness, err := c.service.PrepareDrop(r.Context(), p, input)
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, readiness)
}

func (c Controller) Readiness(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	readiness, err := c.service.Readiness(r.Context(), p, chi.URLParam(r, "dropId"))
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, readiness)
}

func mapControllerError(err error) error {
	switch {
	case errors.Is(err, ErrForbidden):
		return httpapi.Forbidden("auth.forbidden", "운영자 권한이 필요합니다.")
	case errors.Is(err, ErrInvalidPrepareRequest):
		return httpapi.BadRequest("backoffice.invalid_prepare_request", "드롭 준비 요청 값이 올바르지 않습니다.")
	default:
		return err
	}
}
