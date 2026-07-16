package jwks

import "github.com/go-chi/chi/v5"

func RegisterRoutes(router chi.Router, controller *Controller) {
	router.Get("/.well-known/jwks.json", controller.Get)
}
