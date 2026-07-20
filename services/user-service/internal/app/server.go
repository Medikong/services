package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/grafana/pyroscope-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"

	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/user-service/internal/development"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
	"github.com/Medikong/services/services/user-service/internal/platform/config"
	"github.com/Medikong/services/services/user-service/internal/platform/observability"
	"github.com/Medikong/services/services/user-service/internal/security"
	userhttp "github.com/Medikong/services/services/user-service/internal/transport/http"
)

type Server struct {
	cfg        config.ServerConfig
	db         *pgxpool.Pool
	metrics    *observability.Metrics
	health     *operational.Handler
	publicHTTP *http.Server
	adminHTTP  *http.Server
	profiler   *pyroscope.Profiler
}

func NewServer(ctx context.Context, cfg config.ServerConfig) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	metrics, err := observability.NewMetrics(cfg.Service.Name, cfg.Service.Version, cfg.Service.Environment)
	if err != nil {
		return nil, err
	}
	db, err := platformdb.OpenPostgres(ctx, cfg.Postgres)
	if err != nil {
		_ = metrics.Shutdown(context.Background())
		return nil, oops.In("user_server").Code("database.open_failed").Wrap(err)
	}
	cleanup := func() {
		db.Close()
		_ = metrics.Shutdown(context.Background())
	}
	if err := checkServerDatabase(ctx, db); err != nil {
		cleanup()
		return nil, err
	}
	repository, err := user.NewUserRepository(db)
	if err != nil {
		cleanup()
		return nil, err
	}
	sealer, err := security.NewSealer(cfg.Proof.PrivateNameEncryptionKey, nil)
	if err != nil {
		cleanup()
		return nil, err
	}
	authVerifier, err := security.NewVerifier("auth-service", cfg.Proof.AuthProofKeyID, cfg.Proof.AuthProofPublicKey, cfg.Proof.ClockSkew, nil)
	if err != nil {
		cleanup()
		return nil, err
	}
	mediaVerifier, err := security.NewVerifier("media-service", cfg.Proof.MediaProofKeyID, cfg.Proof.MediaProofPublicKey, cfg.Proof.ClockSkew, nil)
	if err != nil {
		cleanup()
		return nil, err
	}
	userSigner, err := security.NewSigner("user-service", cfg.Proof.UserSigningKeyID, cfg.Proof.UserSigningPrivateKey, nil)
	if err != nil {
		cleanup()
		return nil, err
	}
	service, err := user.NewUserService(repository, sealer, authVerifier, mediaVerifier, userSigner, user.UserServiceConfig{
		RequiredAgreements: cfg.RequiredAgreements,
		IdempotencyTTL:     cfg.IdempotencyTTL,
		ProofTTL:           cfg.Proof.ProofTTL,
	})
	if err != nil {
		cleanup()
		return nil, err
	}
	health := operational.NewHandler(operational.Config{
		Service:          cfg.Service.Name,
		ReadinessTimeout: cfg.Lifecycle.ReadinessTimeout,
		Checks: map[string]operational.Check{
			"database": func(ctx context.Context) error { return checkServerDatabase(ctx, db) },
		},
		Metrics:  metrics.Handler(),
		SetReady: metrics.SetReady,
	})
	userHandler, err := user.NewUserHandler(service, metrics, user.UserHandlerConfig{AllowedOrigins: cfg.HTTP.AllowedOrigins})
	if err != nil {
		cleanup()
		return nil, err
	}
	proofHandler, err := newDevelopmentProofHandler(cfg, metrics)
	if err != nil {
		cleanup()
		return nil, err
	}
	router, err := userhttp.NewRouter(userhttp.RouterConfig{
		ServiceName:        cfg.Service.Name,
		ServiceVersion:     cfg.Service.Version,
		ServiceEnvironment: cfg.Service.Environment,
		RequestTimeout:     cfg.HTTP.RequestTimeout,
		Metrics:            metrics.HTTP(),
	}, userHandler, proofHandler, health)
	if err != nil {
		cleanup()
		return nil, err
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
		cfg: cfg, db: db, metrics: metrics, health: health,
		publicHTTP: httpserver.New(cfg.HTTP.PublicAddr, router), adminHTTP: adminHTTP, profiler: profiler,
	}, nil
}

func newDevelopmentProofHandler(cfg config.ServerConfig, metrics *observability.Metrics) (*development.ProofHandler, error) {
	if !cfg.Development.Enabled {
		return nil, nil
	}
	authSigner, err := security.NewSigner("auth-service", cfg.Proof.AuthProofKeyID, cfg.Development.AuthSigningPrivateKey, nil)
	if err != nil {
		return nil, err
	}
	mediaSigner, err := security.NewSigner("media-service", cfg.Proof.MediaProofKeyID, cfg.Development.MediaSigningPrivateKey, nil)
	if err != nil {
		return nil, err
	}
	return development.NewProofHandler(development.ProofHandlerConfig{
		AccessToken: cfg.Development.AccessToken,
		AuthSigner:  authSigner,
		MediaSigner: mediaSigner,
		ProofTTL:    cfg.Proof.ProofTTL,
	}, metrics)
}

func checkServerDatabase(ctx context.Context, db *pgxpool.Pool) error {
	if err := db.Ping(ctx); err != nil {
		return oops.In("user_server").Code("server.database_unavailable").Wrap(err)
	}
	return user.CheckSchema(ctx, db)
}

func (s *Server) BeginDrain() {
	s.health.BeginDrain()
}

func (s *Server) Run(ctx context.Context) error {
	publicListener, err := net.Listen("tcp", s.cfg.HTTP.PublicAddr)
	if err != nil {
		return s.closeWith(oops.In("user_server").Code("server.http_listen_failed").Wrap(err))
	}
	adminListener, err := net.Listen("tcp", s.cfg.HTTP.AdminAddr)
	if err != nil {
		_ = publicListener.Close()
		return s.closeWith(oops.In("user_server").Code("server.admin_listen_failed").Wrap(err))
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
	return oops.In("user_server").Code("server.http_serve_failed").With("server", name).Wrap(err)
}

func shutdownHTTP(ctx context.Context, server *http.Server, name string) error {
	if err := server.Shutdown(ctx); err != nil {
		closeErr := server.Close()
		return oops.Join(
			oops.In("user_server").Code("server.http_shutdown_failed").With("server", name).Wrap(err),
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
	metricCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Lifecycle.ShutdownTimeout)
	metricErr := s.metrics.Shutdown(metricCtx)
	cancel()
	return oops.Join(cause, profilerErr, metricErr)
}
