package dev

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/packages/go-platform/httpapi"
)

type Controller struct {
	service Service
}

func NewController(service Service) Controller {
	return Controller{service: service}
}

func (c Controller) RegisterRoutes(r chi.Router) {
	r.Route("/v1/internal/dev", func(r chi.Router) {
		r.Post("/test-token", c.IssueTestToken)
	})
}

func (c Controller) IssueTestToken(w http.ResponseWriter, r *http.Request) {
	var input TestTokenInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := c.service.IssueTestToken(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, result)
}
