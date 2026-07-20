package registration

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *RegistrationController) {
	router.Post("/api/v1/auth/registrations", controller.Start)
	router.Post("/api/v1/auth/registrations/{registrationId}/challenges", controller.IssueChallenge)
	router.Post("/api/v1/auth/registrations/{registrationId}/challenges/{challengeId}/verify", controller.VerifyChallenge)
	router.Post("/api/v1/auth/registrations/{registrationId}/complete", controller.Complete)
	router.Get("/api/v1/auth/registrations/{registrationId}", controller.Status)
}
