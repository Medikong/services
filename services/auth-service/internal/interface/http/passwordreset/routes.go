package passwordreset

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *PasswordResetController) {
	router.Post("/api/v1/auth/password-resets", controller.Start)
	router.Post("/api/v1/auth/password-resets/{passwordResetId}/challenges", controller.Issue)
	router.Post("/api/v1/auth/password-resets/{passwordResetId}/challenges/{challengeId}/verify", controller.Verify)
	router.Put("/api/v1/auth/password-resets/{passwordResetId}/password", controller.Complete)
}
