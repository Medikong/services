package app

import (
	"net/http"

	"github.com/Medikong/services/packages/go-platform/operational"
	httpinterface "github.com/Medikong/services/services/auth-service/internal/interface/http"
	httpauthentication "github.com/Medikong/services/services/auth-service/internal/interface/http/authentication"
	httpdevelopment "github.com/Medikong/services/services/auth-service/internal/interface/http/development"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/interface/http/httputil"
	httpidentity "github.com/Medikong/services/services/auth-service/internal/interface/http/identity"
	httpintent "github.com/Medikong/services/services/auth-service/internal/interface/http/intent"
	httpjwks "github.com/Medikong/services/services/auth-service/internal/interface/http/jwks"
	httpoperator "github.com/Medikong/services/services/auth-service/internal/interface/http/operator"
	httppasswordreset "github.com/Medikong/services/services/auth-service/internal/interface/http/passwordreset"
	httpregistration "github.com/Medikong/services/services/auth-service/internal/interface/http/registration"
	httpsession "github.com/Medikong/services/services/auth-service/internal/interface/http/session"
	httpuserauthstate "github.com/Medikong/services/services/auth-service/internal/interface/http/userauthstate"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/observability"
)

func wireHTTP(cfg config.ServerConfig, health *operational.Handler, metrics *observability.Metrics, useCases serverUseCases) (http.Handler, error) {
	credentials, err := httpauth.New(httpauth.Config{
		SessionCookieName:  cfg.Auth.SessionCookieName,
		AuthFlowCookieName: cfg.Auth.AuthFlowCookieName,
		CookieSecure:       cfg.Auth.CookieSecure,
		DevelopmentEnabled: cfg.Development.Enabled,
		DevelopmentRoute:   cfg.Development.RouteEnabled,
		DevelopmentToken:   cfg.Development.AccessToken,
	}, httpsession.SessionCookiePath)
	if err != nil {
		return nil, err
	}
	csrf := httputil.NewCSRF(cfg.Auth.AllowedOrigins)
	router := httpinterface.NewRouter(httpinterface.RouterConfig{
		ServiceName:        cfg.Service.Name,
		ServiceVersion:     cfg.Service.Version,
		ServiceEnvironment: cfg.Service.Environment,
		RequestTimeout:     cfg.HTTP.RequestTimeout,
		Metrics:            metrics.HTTP(),
	}, health)

	httpjwks.RegisterRoutes(router, httpjwks.NewController(useCases.keys))
	httpintent.RegisterRoutes(
		router,
		httpintent.NewBootstrap(credentials, useCases.bootstrap),
		httpintent.NewActionResume(credentials, useCases.sessions, useCases.actionResume),
	)
	httpauthentication.RegisterRoutes(
		router,
		httpauthentication.NewSignIn(credentials, csrf, useCases.emailSignIn, useCases.phoneSignIn),
	)
	httpregistration.RegisterRoutes(
		router,
		httpregistration.NewRegistration(credentials, csrf, useCases.registration),
	)
	httppasswordreset.RegisterRoutes(
		router,
		httppasswordreset.NewPasswordReset(credentials, csrf, useCases.passwordReset),
	)
	httpidentity.RegisterRoutes(
		router,
		httpidentity.NewIdentityManagement(
			credentials,
			csrf,
			useCases.sessions,
			useCases.reauthentication,
			useCases.identity,
		),
	)
	sessionController := httpsession.NewSession(credentials, csrf, useCases.sessions)
	if useCases.sessionStatus != nil {
		httpsession.RegisterRoutes(router, sessionController, httpsession.NewStatusController(useCases.sessionStatus))
	} else {
		httpsession.RegisterRoutes(router, sessionController)
	}
	httpoperator.RegisterRoutes(
		router,
		httpoperator.NewOperator(credentials, useCases.sessions, useCases.operator),
	)
	httpuserauthstate.RegisterRoutes(
		router,
		httpuserauthstate.NewUserAuthState(credentials, useCases.sessions, useCases.userAuthState),
	)
	if useCases.development != nil {
		httpdevelopment.RegisterRoutes(
			router,
			httpdevelopment.NewDevelopment(credentials, useCases.development, useCases.sessions),
		)
	}
	return router, nil
}
