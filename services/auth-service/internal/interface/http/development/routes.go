package development

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *DevelopmentController) {
	router.Get("/api/v1/dev/auth/verification-messages/{challengeId}", controller.VirtualMessage)
	router.Post("/api/v1/dev/auth/test-tokens/bulk", controller.BulkTokens)
}
