package operator

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *OperatorController) {
	router.Get("/api/v1/operator/auth/users/{userId}", controller.User)
	router.Get("/api/v1/operator/auth/policies", controller.Policies)
	router.Patch("/api/v1/operator/auth/policies/{policyName}", controller.UpdatePolicy)
	router.Post("/api/v1/operator/auth/manual-actions", controller.Manual)
}
