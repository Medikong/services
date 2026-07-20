package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/grafana/pyroscope-go"
	"github.com/samber/oops"

	platformmiddleware "github.com/Medikong/services/packages/go-platform/httpmiddleware"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/coupon-service/internal/application/commanding"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/Medikong/services/services/coupon-service/internal/platform/observability"
	couponhttp "github.com/Medikong/services/services/coupon-service/internal/transport/http"
	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

type Server struct {
	cfg            config.ServerConfig
	resources      Resources
	metrics        *observability.Metrics
	health         *operational.Handler
	publicHTTP     *http.Server
	adminHTTP      *http.Server
	profiler       *pyroscope.Profiler
	commandSource  commanding.OperationsCommandSource
	commandIngress commanding.OperationsCommandSubmitter
}

type serverRuntimeResult struct {
	name string
	err  error
}

func NewServer(ctx context.Context, cfg config.ServerConfig) (*Server, error) {
	if err := allowUnavailableExternalDependencies(cfg.Service.Environment); err != nil {
		return nil, err
	}
	return newServer(ctx, cfg, unavailableExternalPorts())
}

// NewServerWithExternalDependencies composes production adapters without
// making unverified external data a silent success.
func NewServerWithExternalDependencies(ctx context.Context, cfg config.ServerConfig, dependencies ExternalDependencies) (*Server, error) {
	external, err := dependencies.resolve()
	if err != nil {
		return nil, err
	}
	return newServer(ctx, cfg, external)
}

func newServer(ctx context.Context, cfg config.ServerConfig, external externalPorts) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	metrics, err := observability.NewMetrics(cfg.Service.Name, cfg.Service.Version, cfg.Service.Environment)
	if err != nil {
		return nil, err
	}
	resources, err := openResources(ctx, cfg.Postgres, cfg.Redis)
	if err != nil {
		_ = metrics.Shutdown(context.Background())
		return nil, err
	}
	closeOnError := func(cause error) (*Server, error) {
		metricCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout)
		defer cancel()
		return nil, oops.Join(cause, resources.Close(), metrics.Shutdown(metricCtx))
	}
	if err := checkDatabase(ctx, resources.DB); err != nil {
		return closeOnError(err)
	}

	checks := map[string]operational.Check{
		"postgres": func(ctx context.Context) error { return checkDatabase(ctx, resources.DB) },
	}
	if cfg.Redis.Enabled && cfg.Redis.FailureMode == config.RedisFailureClosed {
		checks["redis"] = func(ctx context.Context) error { return resources.Redis.Ping(ctx).Err() }
	}
	health := operational.NewHandler(operational.Config{
		Service:          cfg.Service.Name,
		ReadinessTimeout: cfg.Lifecycle.ReadinessTimeout,
		Checks:           checks,
		Metrics:          metrics.Handler(),
		SetReady:         metrics.SetReady,
	})
	backend, err := newHTTPBackendWithPorts(resources, cfg, external)
	if err != nil {
		return closeOnError(err)
	}
	if external.commandSource == nil {
		return closeOnError(oops.In("coupon_server").Code("coupon.operations_command_source_required").New("operations command source is required"))
	}
	router, err := couponhttp.NewRouter(backend, couponhttp.Options{AllowedOrigins: cfg.HTTP.AllowedOrigins})
	if err != nil {
		return closeOnError(err)
	}
	publicHandler := platformmiddleware.Stack(platformmiddleware.Config{
		ServiceName:        cfg.Service.Name,
		ServiceVersion:     cfg.Service.Version,
		ServiceEnvironment: cfg.Service.Environment,
		Metrics:            metrics.HTTP(),
		RoutePattern:       platformmiddleware.ChiRoutePattern(router),
	}, router)
	publicHandler = httpcontract.Timeout(cfg.HTTP.RequestTimeout)(publicHandler)
	publicHandler = health.RejectWhileDraining(publicHandler)

	profiler, err := observability.StartProfiler(cfg.Service, cfg.Profile)
	if err != nil {
		return closeOnError(err)
	}
	adminMux := http.NewServeMux()
	health.RegisterAll(adminMux, cfg.Profile.PprofEnabled)
	adminHTTP := httpserver.New(cfg.HTTP.AdminAddr, adminMux)
	adminHTTP.WriteTimeout = 0
	metrics.SetReady(true)
	return &Server{
		cfg:            cfg,
		resources:      resources,
		metrics:        metrics,
		health:         health,
		publicHTTP:     httpserver.New(cfg.HTTP.PublicAddr, publicHandler),
		adminHTTP:      adminHTTP,
		profiler:       profiler,
		commandSource:  external.commandSource,
		commandIngress: backend.components.commandIngress,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	publicListener, err := listen(s.cfg.HTTP.PublicAddr, "public")
	if err != nil {
		return s.closeWith(err)
	}
	adminListener, err := listen(s.cfg.HTTP.AdminAddr, "admin")
	if err != nil {
		_ = publicListener.Close()
		return s.closeWith(err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	results := make(chan serverRuntimeResult, 3)
	go func() {
		results <- serverRuntimeResult{name: "public", err: serveHTTP(s.publicHTTP, publicListener, "public")}
	}()
	go func() {
		results <- serverRuntimeResult{name: "admin", err: serveHTTP(s.adminHTTP, adminListener, "admin")}
	}()
	go func() {
		results <- serverRuntimeResult{
			name: "operations-command-source",
			err:  runOperationsCommandSource(runCtx, s.commandSource, s.commandIngress),
		}
	}()

	consumed := 0
	var runErr error
	select {
	case <-ctx.Done():
	case result := <-results:
		consumed = 1
		runErr = result.err
		if runErr == nil && ctx.Err() == nil {
			runErr = oops.In("coupon_server").Code("coupon.server_stopped_early").With("component", result.name).New("server component stopped before shutdown was requested")
		}
	}
	cancelRun()
	s.health.BeginDrain()
	s.metrics.SetReady(false)
	if ctx.Err() != nil && s.cfg.HTTP.DrainDelay > 0 {
		timer := time.NewTimer(s.cfg.HTTP.DrainDelay)
		<-timer.C
	}
	shutdownErr := oops.Join(
		shutdownHTTP(s.publicHTTP, s.cfg.Lifecycle.ShutdownTimeout, "public"),
		shutdownHTTP(s.adminHTTP, s.cfg.Lifecycle.ShutdownTimeout, "admin"),
	)
	runErr = collectServerRuntimeResults(results, consumed, 3, s.cfg.Lifecycle.ShutdownTimeout, runErr)
	return s.closeWith(oops.Join(runErr, shutdownErr))
}

func runOperationsCommandSource(ctx context.Context, source commanding.OperationsCommandSource, ingress commanding.OperationsCommandSubmitter) error {
	err := source.Run(ctx, ingress)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}

func collectServerRuntimeResults(results <-chan serverRuntimeResult, consumed, expected int, timeout time.Duration, cause error) error {
	if consumed >= expected {
		return cause
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for consumed < expected {
		select {
		case result := <-results:
			cause = oops.Join(cause, result.err)
			consumed++
		case <-timer.C:
			return oops.Join(cause, oops.In("coupon_server").Code("coupon.server_component_shutdown_timeout").
				With("remaining_components", expected-consumed).New("server components did not stop before the shutdown timeout"))
		}
	}
	return cause
}

func (s *Server) closeWith(cause error) error {
	var profilerErr error
	if s.profiler != nil {
		profilerErr = s.profiler.Stop()
		s.profiler = nil
	}
	metricCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Lifecycle.ShutdownTimeout)
	defer cancel()
	return oops.Join(cause, profilerErr, s.resources.Close(), s.metrics.Shutdown(metricCtx))
}
