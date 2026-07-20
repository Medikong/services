package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/grafana/pyroscope-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/packages/go-platform/redisutil"
	authmigration "github.com/Medikong/services/services/auth-service/internal/infrastructure/migration"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/observability"
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

func NewServer(ctx context.Context, cfg config.ServerConfig, options ServerOptions) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	metrics, err := observability.NewMetrics(cfg.Service.Name, cfg.Service.Version, cfg.Service.Environment)
	if err != nil {
		return nil, err
	}
	resources, err := openServerResources(ctx, cfg)
	if err != nil {
		_ = metrics.Shutdown(context.Background())
		return nil, err
	}
	var redisClient *redis.Client
	if cfg.SessionStatus.Enabled {
		redisClient, err = redisutil.Open(ctx, cfg.SessionStatus.Redis)
		if err != nil {
			_ = resources.Close()
			_ = metrics.Shutdown(context.Background())
			return nil, oops.In("auth_server").Code("redis.open_failed").Wrap(err)
		}
	}
	cleanup := func() {
		if redisClient != nil {
			_ = redisClient.Close()
		}
		_ = resources.Close()
		_ = metrics.Shutdown(context.Background())
	}
	if err := checkServerDatabase(ctx, resources.DB, cfg.Development); err != nil {
		cleanup()
		return nil, err
	}
	checks := map[string]operational.Check{
		"database": func(ctx context.Context) error {
			return checkServerDatabase(ctx, resources.DB, cfg.Development)
		},
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

	adapters, err := wireRepositories(cfg, resources.DB, redisClient)
	if err != nil {
		cleanup()
		return nil, err
	}
	useCases, err := wireUseCases(cfg, options, adapters)
	if err != nil {
		cleanup()
		return nil, err
	}
	publicHandler, err := wireHTTP(cfg, health, metrics, useCases)
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
		cfg:        cfg,
		db:         resources.DB,
		redis:      redisClient,
		metrics:    metrics,
		health:     health,
		publicHTTP: httpserver.New(cfg.HTTP.PublicAddr, publicHandler),
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
	if err := authmigration.CheckSchema(ctx, db); err != nil {
		return err
	}
	if development.VirtualAdaptersEnabled {
		return authmigration.CheckDevelopmentSchema(ctx, db)
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
