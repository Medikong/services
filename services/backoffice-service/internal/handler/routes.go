package handler

import (
	"errors"
	"net/http"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/services/backoffice-service/internal/config"
	"github.com/Medikong/services/services/backoffice-service/internal/model"
	backofficeservice "github.com/Medikong/services/services/backoffice-service/internal/service"
)

const serviceName = config.ServiceName

type Handler struct {
	service backofficeservice.Service
}

func RegisterRoutes(mux *http.ServeMux, service backofficeservice.Service) {
	h := Handler{service: service}
	operationalHandler().Register(mux)
	mux.HandleFunc("POST /admin/drops/prepare", h.PrepareDrop)
	mux.HandleFunc("GET /admin/drops/{dropId}/readiness", h.Readiness)
}

func (h Handler) PrepareDrop(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	var input model.PrepareDropInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	readiness, err := h.service.PrepareDrop(r.Context(), p, input)
	if err != nil {
		httpapi.WriteError(w, r, mapBackofficeError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, readiness)
}

func (h Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	readiness, err := h.service.Readiness(r.Context(), p, r.PathValue("dropId"))
	if err != nil {
		httpapi.WriteError(w, r, mapBackofficeError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, readiness)
}

func mapBackofficeError(err error) error {
	switch {
	case errors.Is(err, backofficeservice.ErrForbidden):
		return httpapi.Forbidden("auth.forbidden", "운영자 권한이 필요합니다.")
	case errors.Is(err, backofficeservice.ErrInvalidPrepareRequest):
		return httpapi.BadRequest("backoffice.invalid_prepare_request", "드롭 준비 요청 값이 올바르지 않습니다.")
	default:
		return err
	}
}
