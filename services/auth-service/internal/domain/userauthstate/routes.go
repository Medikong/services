package userauthstate

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *UserAuthStateController) {
	router.Put("/api/v1/operator/auth/users/{userId}/account-status", controller.Apply)
}
