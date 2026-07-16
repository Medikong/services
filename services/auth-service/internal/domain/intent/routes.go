package intent

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, bootstrap *BootstrapController, actionResume *ActionResumeController) {
	router.Post("/api/v1/auth/intents", bootstrap.CreateIntent)
	router.Get("/api/v1/auth/methods", bootstrap.GetMethods)
	router.Post("/api/v1/auth/intents/{intentId}/action-resume", actionResume.Resume)
}
