package app

import (
	"context"
	"crypto/rsa"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/grafana/pyroscope-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-audit"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/packages/go-platform/redisutil"
	"github.com/Medikong/services/services/auth-service/internal/auth"
	"github.com/Medikong/services/services/auth-service/internal/domain/authentication"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/development"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/Medikong/services/services/auth-service/internal/domain/jwks"
	"github.com/Medikong/services/services/auth-service/internal/domain/operator"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/passwordreset"
	"github.com/Medikong/services/services/auth-service/internal/domain/policy"
	"github.com/Medikong/services/services/auth-service/internal/domain/reauth"
	"github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/httpauth"
	"github.com/Medikong/services/services/auth-service/internal/platform/observability"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/Medikong/services/services/auth-service/internal/transport/httputil"
	"github.com/redis/go-redis/v9"
)

type Server struct {
	cfg        config.ServerConfig
	db         *pgxpool.Pool
	redis      *redis.Client
	metrics    *observability.Metrics
	health     *operational.Handler
	publicHTTP *http.Server
	adminHTTP  *http.Server
	profiler   *pyroscope.Profiler
}

type ServerOptions struct {
	ApprovalPort              operator.ApprovalPort
	AuthorizationDecisionPort userauthstate.AuthorizationDecisionPort
}

func NewServer(ctx context.Context, cfg config.ServerConfig, options ServerOptions) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	metrics, err := observability.NewMetrics(cfg.Service.Name)
	if err != nil {
		return nil, err
	}
	db, err := platformdb.OpenPostgres(ctx, cfg.Postgres)
	if err != nil {
		_ = metrics.Shutdown(context.Background())
		return nil, oops.In("auth_server").Code("database.open_failed").Wrap(err)
	}
	var redisClient *redis.Client
	if cfg.SessionStatus.Enabled {
		redisClient, err = redisutil.Open(ctx, cfg.SessionStatus.Redis)
		if err != nil {
			db.Close()
			_ = metrics.Shutdown(context.Background())
			return nil, oops.In("auth_server").Code("redis.open_failed").Wrap(err)
		}
	}
	cleanup := func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
		db.Close()
		_ = metrics.Shutdown(context.Background())
	}
	if err := checkServerDatabase(ctx, db, cfg.Development); err != nil {
		cleanup()
		return nil, err
	}
	checks := map[string]operational.Check{
		"database": func(ctx context.Context) error { return checkServerDatabase(ctx, db, cfg.Development) },
	}
	if redisClient != nil {
		checks["redis"] = func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }
	}
	health := operational.NewHandler(operational.Config{
		Service:          cfg.Service.Name,
		ReadinessTimeout: cfg.Lifecycle.ReadinessTimeout,
		Checks:           checks,
		Metrics:          metrics.Handler(),
		SetReady:         metrics.SetReady,
	})

	retiringKeys := make(map[string]*rsa.PublicKey, len(cfg.Auth.JWTRetiringPublicKeys))
	for keyID, encoded := range cfg.Auth.JWTRetiringPublicKeys {
		publicKey, parseErr := security.ParseRSAPublicKeyPEM([]byte(encoded))
		if parseErr != nil {
			cleanup()
			return nil, oops.In("auth_server").Code("server.jwt_retiring_key_invalid").
				With("key_id", keyID).Wrap(parseErr)
		}
		retiringKeys[keyID] = publicKey
	}
	keys := security.Keys{
		CredentialHMAC: []byte(cfg.Auth.CredentialHMACKey),
		ReplayKey:      []byte(cfg.Auth.ReplayEncryptionKey),
		JWTKey:         []byte(cfg.Auth.JWTPrivateKeyPEM),
		JWTKeyID:       cfg.Auth.JWTKeyID,
		JWTIssuer:      cfg.Auth.JWTIssuer,
		JWTAudiences:   cfg.Auth.JWTAudiences,
		JWTVerifyKeys:  retiringKeys,
		VirtualKey:     []byte(cfg.Development.VirtualMessageKey),
	}
	if err := keys.Validate(cfg.Development.VirtualAdaptersEnabled); err != nil {
		cleanup()
		return nil, err
	}

	intentRepository := intent.NewPostgresRepository(db)
	idempotencyRepository := idempotency.NewPostgresRepository(db)
	identityRepository := identity.NewPostgresRepository(db)
	userAuthStateRepository := userauthstate.NewPostgresRepository(db)
	registrationRepository := registration.NewPostgresRepository(db)
	challengeRepository := challenge.NewPostgresRepository(db, challenge.PostgresOptions{
		VirtualProjectionEnabled: cfg.Development.VirtualAdaptersEnabled,
	})
	outboxRepository := outbox.NewPostgresRepository(db)
	sessionRepository := session.NewPostgresRepository(db)
	var sessionProjection *session.StatusProjection
	if redisClient != nil {
		sessionProjection, err = session.NewStatusProjection(
			sessionRepository, redisClient, cfg.SessionStatus.Timeout, cfg.SessionStatus.DBFallbackTimeout,
		)
		if err != nil {
			cleanup()
			return nil, err
		}
	}
	sessionProjectionWriters := make([]session.StatusProjectionWriter, 0, 1)
	userStateProjectionWriters := make([]userauthstate.StatusProjectionWriter, 0, 1)
	if sessionProjection != nil {
		sessionProjectionWriters = append(sessionProjectionWriters, sessionProjection)
		userStateProjectionWriters = append(userStateProjectionWriters, sessionProjection)
	}
	passwordResetRepository := passwordreset.NewPostgresRepository(db)

	bootstrapService := intent.NewBootstrapService(
		db,
		keys,
		intent.BootstrapConfig{IntentTTL: cfg.Auth.IntentTTL},
		intentRepository,
		idempotencyRepository,
	)
	sessionService := session.NewService(db, keys, session.Config{
		AccessTTL:            cfg.Auth.AccessTTL,
		RefreshTTL:           cfg.Auth.RefreshTTL,
		SessionTTL:           cfg.Auth.SessionTTL,
		RememberMeSessionTTL: cfg.Auth.RememberMeSessionTTL,
		RecoveryTTL:          cfg.Auth.RecoveryTTL,
	}, sessionRepository, userAuthStateRepository, idempotencyRepository, outboxRepository, sessionProjectionWriters...)
	emailSignInService := authentication.NewEmailService(
		db, bootstrapService, identityRepository, intentRepository, sessionService,
	)
	phoneSignInService := authentication.NewPhoneService(
		db, keys, bootstrapService, intentRepository, identityRepository,
		challengeRepository, outboxRepository, sessionService,
		cfg.Development.VirtualAdaptersEnabled, cfg.Auth.ChallengeTTL,
	)
	passwordResetService := passwordreset.NewService(db, keys, passwordreset.Config{
		ResetTTL:              cfg.Auth.ProofTTL,
		ChallengeTTL:          cfg.Auth.ChallengeTTL,
		VirtualAdapterEnabled: cfg.Development.VirtualAdaptersEnabled,
	}, bootstrapService, passwordResetRepository, identityRepository, challengeRepository,
		idempotencyRepository, sessionRepository, outboxRepository, sessionProjectionWriters...)
	reauthService := reauth.NewReauthService(
		db, keys, identityRepository, reauth.NewPostgresRepository(db), sessionRepository,
		idempotencyRepository, sessionService, cfg.Auth.ProofTTL, cfg.Auth.RecoveryTTL,
	)
	identityLinkService := identity.NewLinkService(
		db, keys, reauthService, identityRepository, challengeRepository, sessionRepository,
		sessionService, idempotencyRepository, outboxRepository,
		cfg.Development.VirtualAdaptersEnabled, cfg.Auth.ChallengeTTL, cfg.Auth.RecoveryTTL,
	)
	approvalPort := options.ApprovalPort
	if approvalPort == nil {
		approvalPort = operator.DenyApprovalPort{}
	}
	operatorService := operator.NewService(
		db, keys, operator.NewPostgresRepository(db), policy.NewPostgresRepository(db),
		idempotencyRepository, outboxRepository, operator.Config{StrongAuthTTL: cfg.Auth.ProofTTL},
		approvalPort, options.AuthorizationDecisionPort,
	)
	userProofVerifier, err := security.NewUserProofVerifier(
		cfg.Auth.UserProofIssuer,
		cfg.Auth.UserProofKeyID,
		cfg.Auth.UserProofPublicKey,
		cfg.Auth.ProofClockSkew,
		nil,
	)
	if err != nil {
		cleanup()
		return nil, err
	}
	authProofSigner, err := security.NewUserProofSigner(
		config.ServiceName,
		cfg.Auth.AuthProofKeyID,
		cfg.Auth.AuthProofPrivateKey,
		nil,
	)
	if err != nil {
		cleanup()
		return nil, err
	}
	registrationService := registration.NewService(
		db,
		keys,
		registration.Config{
			RegistrationTTL:       cfg.Auth.RegistrationTTL,
			StatusTokenRetention:  cfg.Auth.ProofTTL,
			ChallengeTTL:          cfg.Auth.ChallengeTTL,
			VirtualAdapterEnabled: cfg.Development.VirtualAdaptersEnabled,
		},
		bootstrapService,
		registrationRepository,
		challengeRepository,
		identityRepository,
		idempotencyRepository,
		outboxRepository,
		userAuthStateRepository,
		intentRepository,
		sessionService,
		registration.ProofConfig{Signer: authProofSigner, Verifier: userProofVerifier},
	)
	actionResumeService := intent.NewActionResumeService(db, keys, intentRepository, idempotencyRepository)
	userAuthStateService := userauthstate.NewService(
		db,
		userAuthStateRepository,
		sessionRepository,
		userProofVerifier,
		options.AuthorizationDecisionPort,
		userauthstate.Config{StrongAuthTTL: cfg.Auth.ProofTTL},
		userStateProjectionWriters...,
	)

	credentials := httpauth.New(cfg.Auth, cfg.Development)
	csrf := httputil.NewCSRF(cfg.Auth.AllowedOrigins)
	bootstrapController := intent.NewBootstrap(credentials, bootstrapService)
	signInController := authentication.NewSignIn(credentials, csrf, emailSignInService, phoneSignInService)
	sessionController := session.NewSession(credentials, csrf, sessionService)
	registrationController := registration.NewRegistration(credentials, csrf, registrationService)
	passwordResetController := passwordreset.NewPasswordReset(credentials, csrf, passwordResetService)
	identityController := identity.NewIdentityManagement(credentials, csrf, sessionService, reauthService, identityLinkService)
	operatorController := operator.NewOperator(credentials, sessionService, operatorService)
	actionResumeController := intent.NewActionResume(credentials, sessionService, actionResumeService)
	userAuthStateController := userauthstate.NewUserAuthState(credentials, sessionService, userAuthStateService)
	jwksController := jwks.NewController(keys)
	var developmentController *development.DevelopmentController
	if cfg.Development.RouteEnabled {
		virtualMessageService := development.NewVirtualMessageService(
			db, keys, bootstrapService, challengeRepository, registrationRepository,
			passwordResetRepository, identityRepository,
		)
		developmentController = development.NewDevelopment(credentials, virtualMessageService, sessionService)
	}

	router := newRouter(cfg.Service.Name, cfg.HTTP.RequestTimeout, health)
	jwks.RegisterRoutes(router, jwksController)
	intent.RegisterRoutes(router, bootstrapController, actionResumeController)
	authentication.RegisterRoutes(router, signInController)
	registration.RegisterRoutes(router, registrationController)
	passwordreset.RegisterRoutes(router, passwordResetController)
	identity.RegisterRoutes(router, identityController)
	if sessionProjection != nil {
		session.RegisterRoutes(router, sessionController, session.NewExtAuthzController(keys, sessionProjection))
	} else {
		session.RegisterRoutes(router, sessionController)
	}
	operator.RegisterRoutes(router, operatorController)
	userauthstate.RegisterRoutes(router, userAuthStateController)
	if developmentController != nil {
		development.RegisterRoutes(router, developmentController)
	}
	profiler, err := observability.StartProfiler(cfg.Service, cfg.Profile)
	if err != nil {
		cleanup()
		return nil, err
	}
	adminMux := http.NewServeMux()
	health.RegisterAll(adminMux, cfg.Profile.PprofEnabled)
	adminHTTP := httpserver.New(cfg.HTTP.AdminAddr, adminMux)
	adminHTTP.WriteTimeout = 0
	metrics.SetReady(true)
	return &Server{
		cfg:        cfg,
		db:         db,
		redis:      redisClient,
		metrics:    metrics,
		health:     health,
		publicHTTP: httpserver.New(cfg.HTTP.PublicAddr, router),
		adminHTTP:  adminHTTP,
		profiler:   profiler,
	}, nil
}

func checkServerDatabase(ctx context.Context, db *pgxpool.Pool, development config.DevelopmentConfig) error {
	if err := db.Ping(ctx); err != nil {
		return oops.In("auth_server").Code("server.database_unavailable").Wrap(err)
	}
	if err := audit.CheckSchema(ctx, db); err != nil {
		return err
	}
	if err := auth.CheckSchema(ctx, db); err != nil {
		return err
	}
	if development.VirtualAdaptersEnabled {
		return auth.CheckDevelopmentSchema(ctx, db)
	}
	return nil
}

func (s *Server) Run(ctx context.Context) error {
	publicListener, err := net.Listen("tcp", s.cfg.HTTP.PublicAddr)
	if err != nil {
		return s.closeWith(oops.In("auth_server").Code("server.http_listen_failed").Wrap(err))
	}
	adminListener, err := net.Listen("tcp", s.cfg.HTTP.AdminAddr)
	if err != nil {
		_ = publicListener.Close()
		return s.closeWith(oops.In("auth_server").Code("server.admin_listen_failed").Wrap(err))
	}
	results := make(chan error, 2)
	go func() { results <- serveHTTP(s.publicHTTP, publicListener, "public") }()
	go func() { results <- serveHTTP(s.adminHTTP, adminListener, "admin") }()

	consumed := 0
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-results:
		consumed = 1
	}
	s.health.BeginDrain()
	if ctx.Err() != nil && s.cfg.HTTP.DrainDelay > 0 {
		timer := time.NewTimer(s.cfg.HTTP.DrainDelay)
		select {
		case <-timer.C:
		case <-time.After(s.cfg.Lifecycle.ShutdownTimeout):
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Lifecycle.ShutdownTimeout)
	shutdownErr := oops.Join(
		shutdownHTTP(shutdownCtx, s.publicHTTP, "public"),
		shutdownHTTP(shutdownCtx, s.adminHTTP, "admin"),
	)
	cancel()
	for consumed < 2 {
		if err := <-results; err != nil {
			runErr = oops.Join(runErr, err)
		}
		consumed++
	}
	return s.closeWith(oops.Join(runErr, shutdownErr))
}

func serveHTTP(server *http.Server, listener net.Listener, name string) error {
	err := server.Serve(listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return oops.In("auth_server").Code("server.http_serve_failed").With("server", name).Wrap(err)
}

func shutdownHTTP(ctx context.Context, server *http.Server, name string) error {
	if err := server.Shutdown(ctx); err != nil {
		closeErr := server.Close()
		if closeErr != nil {
			closeErr = oops.In("auth_server").Code("server.http_close_failed").With("server", name).Wrap(closeErr)
		}
		return oops.Join(
			oops.In("auth_server").Code("server.http_shutdown_failed").With("server", name).Wrap(err),
			closeErr,
		)
	}
	return nil
}

func (s *Server) closeWith(cause error) error {
	var profilerErr error
	if s.profiler != nil {
		profilerErr = s.profiler.Stop()
		s.profiler = nil
	}
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
	if s.redis != nil {
		_ = s.redis.Close()
		s.redis = nil
	}
	metricCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Lifecycle.ShutdownTimeout)
	metricErr := s.metrics.Shutdown(metricCtx)
	cancel()
	return oops.Join(cause, profilerErr, metricErr)
}
