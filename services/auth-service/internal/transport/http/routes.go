package http

import (
	nethttp "net/http"

	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/domain/account"
	"github.com/Medikong/services/services/auth-service/internal/domain/dev"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
)

type Services struct {
	Accounts account.Service
	Sessions session.Service
	Dev      dev.Service
}

type Handler struct {
	services Services
}

func RegisterRoutes(mux *nethttp.ServeMux, services Services, checks map[string]operational.Check) {
	h := Handler{services: services}
	operational.New(config.ServiceName, checks).Register(mux)
	mux.HandleFunc("POST /auth/signup", h.Signup)
	mux.HandleFunc("POST /auth/login", h.Login)
	mux.HandleFunc("POST /auth/refresh", h.Refresh)
	mux.HandleFunc("POST /auth/logout", h.Logout)
	mux.HandleFunc("POST /auth/introspect", h.Introspect)
	mux.HandleFunc("POST /internal/auth/sessions/{sessionId}/revoke", h.Revoke)
	mux.HandleFunc("POST /internal/dev/test-token", h.IssueTestToken)
}
