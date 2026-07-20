package app

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/grafana/pyroscope-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/messaging/outbox"
	authmigration "github.com/Medikong/services/services/auth-service/internal/infrastructure/migration"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/observability"
)

type Worker struct {
	cfg                  config.WorkerConfig
	resources            Resources
	metrics              *observability.Metrics
	health               *operational.Handler
	adminHTTP            *http.Server
	runAudit             func(context.Context) error
	runDomainOutbox      func(context.Context) error
	runDelivery          func(context.Context) error
	runSessionProjection func(context.Context) error
	kafkaPublisher       *outbox.KafkaPublisher
	cleanup              func(context.Context) (int64, error)
	cleanupInterval      time.Duration
	profiler             *pyroscope.Profiler
}

func NewWorker(ctx context.Context, cfg config.WorkerConfig) (*Worker, error) {
	return NewWorkerWithPublisher(ctx, cfg, nil)
}

// NewWorkerWithPublisher keeps a narrow injection point for integration tests.
// The normal executable creates the configured Kafka publisher itself.
func NewWorkerWithPublisher(ctx context.Context, cfg config.WorkerConfig, publisher outbox.Publisher) (*Worker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	metrics, err := observability.NewMetrics(workerServiceName(cfg.Service.Name), cfg.Service.Version, cfg.Service.Environment)
	if err != nil {
		return nil, err
	}
	resources, err := openWorkerResources(ctx, cfg)
	if err != nil {
		_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
		return nil, err
	}
	if err := checkWorkerSchemas(ctx, resources, cfg.Development); err != nil {
		_ = resources.Close()
		_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
		return nil, err
	}
	profileService := cfg.Service
	profileService.Name = workerServiceName(profileService.Name)
	profiler, err := observability.StartProfiler(profileService, cfg.Profile)
	if err != nil {
		_ = resources.Close()
		_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
		return nil, err
	}
	wiring, err := wireWorker(ctx, cfg, resources, metrics, publisher)
	if err != nil {
		if profiler != nil {
			_ = profiler.Stop()
		}
		_ = resources.Close()
		_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
		return nil, err
	}
	checks := map[string]operational.Check{
		"source_database": func(ctx context.Context) error {
			return checkWorkerSourceDatabase(ctx, resources.DB, cfg.Development)
		},
	}
	if resources.AuditSink != resources.DB {
		checks["sink_database"] = func(ctx context.Context) error {
			return checkAuditDatabase(ctx, resources.AuditSink)
		}
	}
	if wiring.kafkaPublisher != nil {
		checks["broker"] = wiring.kafkaPublisher.Ping
	}
	if resources.Redis != nil {
		checks["redis"] = func(ctx context.Context) error { return resources.Redis.Ping(ctx).Err() }
	}
	healthState := operational.NewHandler(operational.Config{
		Service:          workerServiceName(cfg.Service.Name),
		ReadinessTimeout: cfg.Lifecycle.ReadinessTimeout,
		Checks:           checks,
		Metrics:          metrics.Handler(),
		SetReady:         metrics.SetReady,
	})
	adminMux := http.NewServeMux()
	healthState.RegisterAll(adminMux, cfg.Profile.PprofEnabled)
	adminHTTP := httpserver.New(cfg.AdminAddr, adminMux)
	adminHTTP.WriteTimeout = 0
	metrics.SetReady(true)
	return &Worker{
		cfg:                  cfg,
		resources:            resources,
		metrics:              metrics,
		health:               healthState,
		adminHTTP:            adminHTTP,
		runAudit:             wiring.runAudit,
		runDomainOutbox:      wiring.runDomainOutbox,
		runDelivery:          wiring.runDelivery,
		runSessionProjection: wiring.runSessionProjection,
		kafkaPublisher:       wiring.kafkaPublisher,
		cleanup:              wiring.cleanup,
		cleanupInterval:      time.Hour,
		profiler:             profiler,
	}, nil
}

func workerServiceName(baseServiceName string) string {
	return baseServiceName + "-worker"
}

func checkWorkerSchemas(ctx context.Context, resources Resources, development config.DevelopmentConfig) error {
	if err := checkWorkerSourceDatabase(ctx, resources.DB, development); err != nil {
		return err
	}
	if resources.AuditSink != resources.DB {
		return checkAuditDatabase(ctx, resources.AuditSink)
	}
	return nil
}

func checkWorkerSourceDatabase(ctx context.Context, db *pgxpool.Pool, development config.DevelopmentConfig) error {
	if err := checkAuditDatabase(ctx, db); err != nil {
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

func checkAuditDatabase(ctx context.Context, db *pgxpool.Pool) error {
	if err := db.Ping(ctx); err != nil {
		return oops.In("auth_worker").Code("worker.database_unavailable").Wrap(err)
	}
	return audit.CheckSchema(ctx, db)
}

func (w *Worker) Run(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	var adminResult <-chan error
	if w.adminHTTP != nil {
		listener, err := net.Listen("tcp", w.cfg.AdminAddr)
		if err != nil {
			return w.closeWith(oops.In("auth_worker").Code("worker.admin_listen_failed").Wrap(err))
		}
		results := make(chan error, 1)
		adminResult = results
		go func() { results <- serveHTTP(w.adminHTTP, listener, "worker-admin") }()
	}
	backgroundCount := 1
	backgroundResult := make(chan error, 4)
	go func() { backgroundResult <- runResilient(runCtx, "audit", w.cfg.Audit.PollInterval, w.runAudit) }()
	if w.runDomainOutbox != nil {
		backgroundCount++
		go func() {
			backgroundResult <- runResilient(runCtx, "domain_outbox", w.cfg.Audit.PollInterval, w.runDomainOutbox)
		}()
	}
	if w.runDelivery != nil {
		backgroundCount++
		go func() {
			backgroundResult <- runResilient(runCtx, "verification_delivery", w.cfg.Delivery.PollInterval, w.runDelivery)
		}()
	}
	if w.runSessionProjection != nil {
		backgroundCount++
		go func() {
			backgroundResult <- runResilient(runCtx, "session_projection", w.cfg.Audit.PollInterval, w.runSessionProjection)
		}()
	}

	cleanupInterval := w.cleanupInterval
	if cleanupInterval <= 0 {
		cleanupInterval = time.Hour
	}
	cleanupTicker := time.NewTicker(cleanupInterval)
	defer cleanupTicker.Stop()
	for {
		select {
		case err := <-backgroundResult:
			backgroundCount--
			if err == nil {
				err = oops.In("auth_worker").Code("worker.background_stopped").New("worker background relay stopped unexpectedly")
			}
			cancelRun()
			return w.closeWith(oops.Join(err, w.shutdownAdmin(), w.waitForBackground(backgroundResult, backgroundCount)))
		case err := <-adminResult:
			if err == nil {
				err = oops.In("auth_worker").Code("worker.admin_stopped").New("worker admin server stopped unexpectedly")
			}
			cancelRun()
			return w.closeWith(oops.Join(err, w.waitForBackground(backgroundResult, backgroundCount)))
		case <-ctx.Done():
			if w.health != nil {
				w.health.BeginDrain()
			}
			cancelRun()
			adminErr := w.shutdownAdmin()
			return w.closeWith(oops.Join(adminErr, w.waitForBackground(backgroundResult, backgroundCount)))
		case <-cleanupTicker.C:
			cleanupCtx, cancel := context.WithTimeout(runCtx, w.cfg.Audit.PublishTimeout)
			deleted, err := w.cleanup(cleanupCtx)
			cancel()
			if err != nil {
				logger.Error(runCtx, "worker.cleanup_failed", logger.Err(err))
				continue
			}
			if deleted > 0 {
				logger.Info(runCtx, "worker.records_cleaned", "deleted", deleted)
			}
		}
	}
}

func runResilient(ctx context.Context, name string, retryDelay time.Duration, run func(context.Context) error) error {
	if retryDelay <= 0 {
		retryDelay = time.Second
	}
	for {
		err := run(ctx)
		if ctx.Err() != nil {
			return nil
		}
		logger.Warn(ctx, "worker.background_retrying", "component", name, logger.Err(err))
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (w *Worker) shutdownAdmin() error {
	if w.adminHTTP == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), w.cfg.Lifecycle.ShutdownTimeout)
	defer cancel()
	return shutdownHTTP(shutdownCtx, w.adminHTTP, "worker-admin")
}

func (w *Worker) waitForBackground(result <-chan error, count int) error {
	if count == 0 {
		return nil
	}
	timer := time.NewTimer(w.cfg.Lifecycle.ShutdownTimeout)
	defer timer.Stop()
	var combined error
	for count > 0 {
		select {
		case err := <-result:
			combined = oops.Join(combined, err)
			count--
		case <-timer.C:
			return oops.Join(combined, oops.In("auth_worker").Code("worker.shutdown_timeout").New("worker relay did not stop before shutdown timeout"))
		}
	}
	return combined
}

func (w *Worker) closeWith(cause error) error {
	profilerErr := w.stopProfilerWith(cause)
	if w.kafkaPublisher != nil {
		w.kafkaPublisher.Close()
		w.kafkaPublisher = nil
	}
	resourceErr := w.resources.Close()
	metricErr := shutdownMetrics(w.metrics, w.cfg.Lifecycle.ShutdownTimeout)
	return oops.Join(profilerErr, resourceErr, metricErr)
}

func (w *Worker) stopProfilerWith(cause error) error {
	var profilerErr error
	if w.profiler != nil {
		profilerErr = w.profiler.Stop()
		w.profiler = nil
	}
	return oops.Join(cause, profilerErr)
}

func shutdownMetrics(metrics *observability.Metrics, timeout time.Duration) error {
	if metrics == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return metrics.Shutdown(ctx)
}
