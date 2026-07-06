package account

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
	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/signup", c.Signup)
		r.Post("/login", c.Login)
	})
}

func (c Controller) Signup(w http.ResponseWriter, r *http.Request) {
	var input SignupInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := c.service.Signup(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, result)
}

func (c Controller) Login(w http.ResponseWriter, r *http.Request) {
	var input LoginInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := c.service.Login(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}
