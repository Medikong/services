package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	platformmiddleware "github.com/Medikong/services/packages/go-platform/httpmiddleware"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/domain/authentication"
	"github.com/Medikong/services/services/auth-service/internal/domain/development"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/Medikong/services/services/auth-service/internal/domain/operator"
	"github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
)

type RouterConfig struct {
	ServiceName              string
	RequestTimeout           time.Duration
	DevelopmentRoutesEnabled bool
}

type Controllers struct {
	Bootstrap     *intent.BootstrapController
	SignIn        *authentication.SignInController
	Session       *session.SessionController
	Registration  *registration.RegistrationController
	PasswordReset *passwordreset.PasswordResetController
	Identity      *identity.IdentityManagementController
	Operator      *operator.OperatorController
	ActionResume  *intent.ActionResumeController
	UserAuthState *userauthstate.UserAuthStateController
	Development   *development.DevelopmentController
	JWKS          http.HandlerFunc
}

func NewRouter(cfg RouterConfig, health *operational.Handler, controllers Controllers) (http.Handler, error) {
	if cfg.ServiceName == "" || cfg.RequestTimeout <= 0 || health == nil ||
		controllers.Bootstrap == nil || controllers.SignIn == nil || controllers.Session == nil ||
		controllers.Registration == nil || controllers.PasswordReset == nil || controllers.Identity == nil ||
		controllers.Operator == nil || controllers.ActionResume == nil || controllers.UserAuthState == nil ||
		controllers.JWKS == nil || (cfg.DevelopmentRoutesEnabled && controllers.Development == nil) {
		return nil, oops.In("auth_router").Code("router.dependencies_required").
			New("router configuration and handlers are required")
	}

	router := chi.NewRouter()
	router.Use(httputil.IDMiddleware)
	router.Use(func(next http.Handler) http.Handler {
		return platformmiddleware.Stack(platformmiddleware.Config{
			ServiceName:  cfg.ServiceName,
			RoutePattern: RoutePattern,
		}, next)
	})
	router.Use(recoverHTTP)
	router.Use(timeoutHTTP(cfg.RequestTimeout))
	router.Use(rejectWhileDraining(health))
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteError(w, r, httpapi.NotFound("AUTH_ROUTE_NOT_FOUND").
			Public("요청 경로를 확인해주세요.").
			New("auth route not found"))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteError(w, r, httpapi.MethodNotAllowed("AUTH_METHOD_NOT_ALLOWED").
			Public("요청 방식을 확인해주세요.").
			New("auth method not allowed"))
	})

	RegisterJWKSRoute(router, controllers.JWKS)
	intent.RegisterRoutes(router, controllers.Bootstrap, controllers.ActionResume)
	authentication.RegisterRoutes(router, controllers.SignIn)
	registration.RegisterRoutes(router, controllers.Registration)
	passwordreset.RegisterRoutes(router, controllers.PasswordReset)
	identity.RegisterRoutes(router, controllers.Identity)
	session.RegisterRoutes(router, controllers.Session)
	operator.RegisterRoutes(router, controllers.Operator)
	userauthstate.RegisterRoutes(router, controllers.UserAuthState)
	if cfg.DevelopmentRoutesEnabled {
		development.RegisterRoutes(router, controllers.Development)
	}
	return router, nil
}

func RoutePattern(r *http.Request) string {
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	if pattern == "" {
		return "unmatched"
	}
	return pattern
}

func rejectWhileDraining(health *operational.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !health.Draining() {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Retry-After", "1")
			writeUnavailable(w, r, "서비스가 종료 준비 중입니다.")
		})
	}
}

func timeoutHTTP(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			writer := &trackingWriter{ResponseWriter: w}
			defer func() {
				timedOut := ctx.Err() == context.DeadlineExceeded
				cancel()
				if timedOut && !writer.wroteHeader {
					err := oops.In("auth_router").Code("router.request_timeout").New("request timed out")
					httpapi.ReportError(r.Context(), err, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE")
					writeUnavailable(writer, r.WithContext(ctx), "요청 처리 시간이 초과되었습니다.")
				}
			}()
			next.ServeHTTP(writer, r.WithContext(ctx))
		})
	}
}

func recoverHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &trackingWriter{ResponseWriter: w}
		defer func() {
			if recover() == nil {
				return
			}
			err := oops.In("auth_router").Code("router.panic").New("panic recovered")
			httpapi.ReportError(r.Context(), err, http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE")
			if !writer.wroteHeader {
				writeUnavailable(writer, r, "요청 처리 중 오류가 발생했습니다.")
			}
		}()
		next.ServeHTTP(writer, r)
	})
}

func writeUnavailable(w http.ResponseWriter, r *http.Request, detail string) {
	httputil.WriteError(w, r, httpapi.Error(http.StatusServiceUnavailable, "AUTH_SERVICE_UNAVAILABLE").
		Public(detail).
		New("auth service unavailable"))
}

type trackingWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *trackingWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *trackingWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
