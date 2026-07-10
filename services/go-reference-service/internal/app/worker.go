package app

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/grafana/pyroscope-go"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/Medikong/services/packages/go-platform/httpserver"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/config"
	"github.com/Medikong/services/services/go-reference-service/internal/platform/observability"
)

type Worker struct {
	cfg             config.WorkerConfig
	resources       Resources
	metrics         *observability.Metrics
	health          *operational.Handler
	adminHTTP       *http.Server
	runAudit        func(context.Context) error
	cleanup         func(context.Context) (int64, error)
	cleanupInterval time.Duration
	profiler        *pyroscope.Profiler
}

func NewWorker(ctx context.Context, cfg config.WorkerConfig) (*Worker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	metrics, err := observability.NewMetrics(cfg.Service.Name + "-worker")
	if err != nil {
		return nil, err
	}
	resources, err := openWorkerResources(ctx, cfg)
	if err != nil {
		_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
		return nil, err
	}
	if err := checkWorkerSchemas(ctx, resources); err != nil {
		_ = resources.Close()
		_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
		return nil, err
	}
	profileService := cfg.Service
	profileService.Name += "-worker"
	profiler, err := observability.StartProfiler(profileService, cfg.Profile)
	if err != nil {
		_ = resources.Close()
		_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
		return nil, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		if profiler != nil {
			_ = profiler.Stop()
		}
		_ = resources.Close()
		_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
		return nil, oops.In("reference_worker").Code("worker.hostname_failed").Wrap(err)
	}
	auditWorker, err := audit.NewWorker(audit.WorkerConfig{
		Pool:           resources.DB,
		WorkerID:       hostname + ":" + uuid.NewString(),
		BatchSize:      cfg.Audit.BatchSize,
		PollInterval:   cfg.Audit.PollInterval,
		Lease:          cfg.Audit.Lease,
		PublishTimeout: cfg.Audit.PublishTimeout,
		MaxAttempts:    cfg.Audit.MaxAttempts,
		BaseBackoff:    cfg.Audit.BaseBackoff,
		MaxBackoff:     cfg.Audit.MaxBackoff,
		Publish: func(ctx context.Context, event audit.Event) error {
			return audit.Archive(ctx, resources.AuditSink, event)
		},
		OnAttempt: func(ctx context.Context, attempt audit.Attempt) {
			metrics.RecordAuditAttempt(attempt.Result)
			args := []any{
				"event_id", attempt.EventID,
				"attempt", attempt.Attempts,
				"result", attempt.Result,
			}
			if attempt.Err != nil {
				args = append(args, logger.Err(attempt.Err))
			}
			switch attempt.Result {
			case "dead":
				logger.Error(ctx, "audit.outbox.attempted", args...)
			case "retry":
				logger.Warn(ctx, "audit.outbox.attempted", args...)
			default:
				logger.Info(ctx, "audit.outbox.attempted", args...)
			}
		},
	})
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
			return checkAuditDatabase(ctx, resources.DB)
		},
	}
	if resources.AuditSink != resources.DB {
		checks["sink_database"] = func(ctx context.Context) error {
			return checkAuditDatabase(ctx, resources.AuditSink)
		}
	}
	healthState := operational.NewHandler(operational.Config{
		Service:          cfg.Service.Name + "-worker",
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
		cfg:       cfg,
		resources: resources,
		metrics:   metrics,
		health:    healthState,
		adminHTTP: adminHTTP,
		runAudit:  auditWorker.Run,
		cleanup: func(ctx context.Context) (int64, error) {
			return audit.DeleteDeliveredBefore(
				ctx,
				resources.DB,
				time.Now().UTC().Add(-cfg.Audit.Retention),
				cfg.Audit.CleanupLimit,
			)
		},
		cleanupInterval: time.Hour,
		profiler:        profiler,
	}, nil
}

func checkAuditDatabase(ctx context.Context, db *pgxpool.Pool) error {
	if err := db.Ping(ctx); err != nil {
		return oops.In("reference_worker").Code("worker.database_unavailable").Wrap(err)
	}
	return audit.CheckSchema(ctx, db)
}

func checkWorkerSchemas(ctx context.Context, resources Resources) error {
	if err := checkAuditDatabase(ctx, resources.DB); err != nil {
		return err
	}
	if resources.AuditSink != resources.DB {
		return checkAuditDatabase(ctx, resources.AuditSink)
	}
	return nil
}

func (w *Worker) Run(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	var adminResult <-chan error
	if w.adminHTTP != nil {
		listener, err := net.Listen("tcp", w.cfg.AdminAddr)
		if err != nil {
			return w.closeWith(oops.In("reference_worker").Code("worker.admin_listen_failed").Wrap(err))
		}
		results := make(chan error, 1)
		adminResult = results
		go func() { results <- serveHTTP(w.adminHTTP, listener, "worker-admin") }()
	}

	workerResult := make(chan error, 1)
	go func() { workerResult <- w.runAudit(runCtx) }()

	cleanupInterval := w.cleanupInterval
	if cleanupInterval <= 0 {
		cleanupInterval = time.Hour
	}
	cleanupTicker := time.NewTicker(cleanupInterval)
	defer cleanupTicker.Stop()
	for {
		select {
		case err := <-workerResult:
			cancelRun()
			return w.closeWith(oops.Join(err, w.shutdownAdmin()))
		case err := <-adminResult:
			if err == nil {
				err = oops.In("reference_worker").Code("worker.admin_stopped").New("worker admin server stopped unexpectedly")
			}
			cancelRun()
			relayErr, stopped := w.waitForRelay(workerResult)
			cause := oops.Join(err, relayErr)
			if stopped {
				return w.closeWith(cause)
			}
			return w.stopProfilerWith(cause)
		case <-ctx.Done():
			if w.health != nil {
				w.health.BeginDrain()
			}
			cancelRun()
			adminErr := w.shutdownAdmin()
			relayErr, stopped := w.waitForRelay(workerResult)
			cause := oops.Join(adminErr, relayErr)
			if stopped {
				return w.closeWith(cause)
			}
			return w.stopProfilerWith(cause)
		case <-cleanupTicker.C:
			cleanupCtx, cancel := context.WithTimeout(runCtx, w.cfg.Audit.PublishTimeout)
			deleted, err := w.cleanup(cleanupCtx)
			cancel()
			if err != nil {
				if w.metrics != nil {
					w.metrics.RecordAuditCleanup("error", 0)
				}
				logger.Error(runCtx, "audit.outbox.cleanup_failed", logger.Err(err))
				continue
			}
			if w.metrics != nil {
				w.metrics.RecordAuditCleanup("success", deleted)
			}
			if deleted > 0 {
				logger.Info(runCtx, "audit.outbox.cleaned", "deleted", deleted)
			}
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

func (w *Worker) waitForRelay(workerResult <-chan error) (error, bool) {
	timer := time.NewTimer(w.cfg.Lifecycle.ShutdownTimeout)
	defer timer.Stop()
	select {
	case err := <-workerResult:
		return err, true
	case <-timer.C:
		return oops.
			In("reference_worker").
			Code("worker.shutdown_timeout").
			New("audit worker did not stop before shutdown timeout"), false
	}
}

func (w *Worker) closeWith(cause error) error {
	profilerErr := w.stopProfilerWith(cause)
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
