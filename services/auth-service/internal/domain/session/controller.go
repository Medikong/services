package session

import (
	"net/http"

	"github.com/go-chi/chi/v5"

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
	r.Post("/v1/auth/refresh", c.Refresh)
	r.Post("/v1/auth/logout", c.Logout)
	r.Post("/v1/auth/introspect", c.Introspect)
	r.Route("/v1/internal/auth/sessions", func(r chi.Router) {
		r.Post("/{sessionId}/revoke", c.Revoke)
	})
}

func (c Controller) Refresh(w http.ResponseWriter, r *http.Request) {
	var input RefreshInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := c.service.Refresh(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (c Controller) Logout(w http.ResponseWriter, r *http.Request) {
	if err := c.service.Logout(r.Context(), r.Header.Get(headers.Authorization)); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (c Controller) Introspect(w http.ResponseWriter, r *http.Request) {
	result, err := c.service.Introspect(r.Context(), r.Header.Get(headers.Authorization))
	if err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"principal": result.Principal})
}

func (c Controller) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := c.service.Revoke(r.Context(), chi.URLParam(r, "sessionId")); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
