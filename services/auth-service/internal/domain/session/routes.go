package session

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *SessionController) {
	router.Post("/api/v1/auth/sessions/refresh", controller.Refresh)
	router.Post("/api/v1/auth/sessions/logout", controller.Logout)
	router.Get("/api/v1/auth/context", controller.Context)
}
