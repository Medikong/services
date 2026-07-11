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

	"github.com/Medikong/services/packages/go-audit"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/auth"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/observability"
	authhttp "github.com/Medikong/services/services/auth-service/internal/transport/http"
)

type Server struct {
	cfg        config.ServerConfig
	resources  Resources
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
	metrics, err := observability.NewMetrics(cfg.Service.Name)
	if err != nil {
		return nil, err
	}
	resources, err := openServerResources(ctx, cfg)
	if err != nil {
		_ = metrics.Shutdown(context.Background())
		return nil, err
	}
	if err := checkServerDatabase(ctx, resources.DB, cfg.Development); err != nil {
		_ = resources.Close()
		_ = metrics.Shutdown(context.Background())
		return nil, err
	}
	healthState := operational.NewHandler(operational.Config{
		Service:          cfg.Service.Name,
		ReadinessTimeout: cfg.Lifecycle.ReadinessTimeout,
		Checks: map[string]operational.Check{
			"database": func(ctx context.Context) error {
				return checkServerDatabase(ctx, resources.DB, cfg.Development)
			},
		},
		Metrics:  metrics.Handler(),
		SetReady: metrics.SetReady,
	})
	router, err := authhttp.NewRouter(cfg, resources.DB, healthState, metrics)
	if err != nil {
		_ = resources.Close()
		_ = metrics.Shutdown(context.Background())
		return nil, err
	}
	profiler, err := observability.StartProfiler(cfg.Service, cfg.Profile)
	if err != nil {
		_ = resources.Close()
		_ = metrics.Shutdown(context.Background())
		return nil, err
	}
	adminMux := http.NewServeMux()
	healthState.RegisterAll(adminMux, cfg.Profile.PprofEnabled)
	adminHTTP := httpserver.New(cfg.HTTP.AdminAddr, adminMux)
	adminHTTP.WriteTimeout = 0
	metrics.SetReady(true)
	return &Server{
		cfg:        cfg,
		resources:  resources,
		metrics:    metrics,
		health:     healthState,
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
		<-timer.C
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
	resourceErr := s.resources.Close()
	metricCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Lifecycle.ShutdownTimeout)
	metricErr := s.metrics.Shutdown(metricCtx)
	cancel()
	return oops.Join(cause, profilerErr, resourceErr, metricErr)
}
