package session

import "github.com/go-chi/chi/v5"

const SessionCookiePath = "/api/v1/auth/sessions"

func RegisterRoutes(router chi.Router, controller *SessionController, status ...*StatusController) {
	router.Post(SessionCookiePath+"/refresh", controller.Refresh)
	router.Post(SessionCookiePath+"/logout", controller.Logout)
	router.Get("/api/v1/auth/context", controller.Context)
	if len(status) > 0 && status[0] != nil {
		router.Handle("/internal/session/status", status[0])
		router.Handle("/internal/session/status/*", status[0])
	}
}
