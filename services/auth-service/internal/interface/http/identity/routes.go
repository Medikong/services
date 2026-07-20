package identity

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *IdentityManagementController) {
	router.Post("/api/v1/auth/reauthentications/email", controller.Reauthenticate)
	router.Post("/api/v1/auth/method-links", controller.StartLink)
	router.Post("/api/v1/auth/method-links/{linkIntentId}/challenges", controller.IssueLink)
	router.Post("/api/v1/auth/method-links/{linkIntentId}/complete", controller.CompleteLink)
	router.Post("/api/v1/auth/phone-replacements", controller.StartReplacement)
	router.Post("/api/v1/auth/phone-replacements/{replacementId}/challenges", controller.IssueReplacement)
	router.Post("/api/v1/auth/phone-replacements/{replacementId}/complete", controller.CompleteReplacement)
}
