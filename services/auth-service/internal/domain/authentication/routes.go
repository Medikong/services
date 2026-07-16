package authentication

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *SignInController) {
	router.Post("/api/v1/auth/signins/email", controller.Email)
	router.Post("/api/v1/auth/signins/phone/challenges", controller.PhoneIssue)
	router.Post("/api/v1/auth/signins/phone/challenges/{challengeId}/verify", controller.PhoneVerify)
}
