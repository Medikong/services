package handler

import (
	"net/http"

	"github.com/Medikong/services/services/user-service/internal/config"
)

const serviceName = config.ServiceName

func RegisterRoutes(mux *http.ServeMux) {
	operationalHandler().Register(mux)
}
