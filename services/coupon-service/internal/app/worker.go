package app

import (
	"context"
	"net/http"

	"github.com/grafana/pyroscope-go"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/Medikong/services/services/coupon-service/internal/platform/observability"
	workertransport "github.com/Medikong/services/services/coupon-service/internal/transport/worker"
)

type Worker struct {
	cfg       config.WorkerConfig
	resources Resources
	metrics   *observability.Metrics
	health    *operational.Handler
	adminHTTP *http.Server
	group     *workertransport.Group
	profiler  *pyroscope.Profiler
}

func NewWorker(ctx context.Context, cfg config.WorkerConfig) (*Worker, error) {
	if err := allowUnavailableExternalDependencies(cfg.Service.Environment); err != nil {
		return nil, err
	}
	return newWorker(ctx, cfg, unavailableExternalPorts())
}

// NewWorkerWithExternalDependencies composes the audience, delivery, and
// replay adapters used by durable workers.
func NewWorkerWithExternalDependencies(ctx context.Context, cfg config.WorkerConfig, dependencies ExternalDependencies) (*Worker, error) {
	external, err := dependencies.resolve()
	if err != nil {
		return nil, err
	}
	return newWorker(ctx, cfg, external)
}

func newWorker(ctx context.Context, cfg config.WorkerConfig, external externalPorts) (*Worker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	metrics, err := observability.NewMetrics(cfg.Service.Name+"-worker", cfg.Service.Version, cfg.Service.Environment)
	if err != nil {
		return nil, err
	}
	resources, err := openResources(ctx, cfg.Postgres, cfg.Redis)
	if err != nil {
		_ = metrics.Shutdown(context.Background())
		return nil, err
	}
	closeOnError := func(cause error) (*Worker, error) {
		metricCtx, cancel := context.WithTimeout(context.Background(), cfg.Lifecycle.ShutdownTimeout)
		defer cancel()
		return nil, oops.Join(cause, resources.Close(), metrics.Shutdown(metricCtx))
	}
	if err := checkDatabase(ctx, resources.DB); err != nil {
		return closeOnError(err)
	}
	group, state, err := buildWorkerGroup(ctx, resources, cfg, metrics, external)
	if err != nil {
		return closeOnError(err)
	}
	checks := map[string]operational.Check{
		"postgres": func(ctx context.Context) error { return checkDatabase(ctx, resources.DB) },
		"jobs":     state.Ready,
	}
	if cfg.Redis.Enabled && cfg.Redis.FailureMode == config.RedisFailureClosed {
		checks["redis"] = func(ctx context.Context) error { return resources.Redis.Ping(ctx).Err() }
	}
	health := operational.NewHandler(operational.Config{
		Service:          cfg.Service.Name + "-worker",
		ReadinessTimeout: cfg.Lifecycle.ReadinessTimeout,
		Checks:           checks,
		Metrics:          metrics.Handler(),
		SetReady:         metrics.SetReady,
	})
	profileService := cfg.Service
	profileService.Name += "-worker"
	profiler, err := observability.StartProfiler(profileService, cfg.Profile)
	if err != nil {
		return closeOnError(err)
	}
	adminMux := http.NewServeMux()
	health.RegisterAll(adminMux, cfg.Profile.PprofEnabled)
	adminHTTP := httpserver.New(cfg.AdminAddr, adminMux)
	adminHTTP.WriteTimeout = 0
	metrics.SetReady(true)
	return &Worker{
		cfg:       cfg,
		resources: resources,
		metrics:   metrics,
		health:    health,
		adminHTTP: adminHTTP,
		group:     group,
		profiler:  profiler,
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	listener, err := listen(w.cfg.AdminAddr, "worker-admin")
	if err != nil {
		return w.closeWith(err)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	results := make(chan error, 2)
	go func() { results <- serveHTTP(w.adminHTTP, listener, "worker-admin") }()
	go func() { results <- w.group.Run(runCtx) }()

	consumed := 0
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-results:
		consumed = 1
		if runErr == nil {
			runErr = oops.In("coupon_worker").Code("coupon.worker_stopped_early").New("worker component stopped before shutdown was requested")
		}
	}
	w.health.BeginDrain()
	w.metrics.SetReady(false)
	cancelRun()
	shutdownErr := shutdownHTTP(w.adminHTTP, w.cfg.Lifecycle.ShutdownTimeout, "worker-admin")
	for consumed < 2 {
		runErr = oops.Join(runErr, <-results)
		consumed++
	}
	return w.closeWith(oops.Join(runErr, shutdownErr))
}

func (w *Worker) closeWith(cause error) error {
	var profilerErr error
	if w.profiler != nil {
		profilerErr = w.profiler.Stop()
		w.profiler = nil
	}
	metricCtx, cancel := context.WithTimeout(context.Background(), w.cfg.Lifecycle.ShutdownTimeout)
	defer cancel()
	return oops.Join(cause, profilerErr, w.resources.Close(), w.metrics.Shutdown(metricCtx))
}
