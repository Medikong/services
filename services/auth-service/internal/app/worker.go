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
	"github.com/Medikong/services/services/auth-service/internal/auth"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/observability"
)

type Worker struct {
	cfg                 config.WorkerConfig
	resources           Resources
	metrics             *observability.Metrics
	health              *operational.Handler
	adminHTTP           *http.Server
	runAudit            func(context.Context) error
	runDomainOutbox     func(context.Context) error
	runStatusProjection func(context.Context) error
	cleanup             func(context.Context) (int64, error)
	cleanupInterval     time.Duration
	profiler            *pyroscope.Profiler
}

func NewWorker(ctx context.Context, cfg config.WorkerConfig) (*Worker, error) {
	return NewWorkerWithPublisher(ctx, cfg, nil)
}

// NewWorkerWithPublisher binds the durable auth outbox only when a trusted
// Context adapter is supplied by the deployment composition root. The normal
// executable deliberately passes nil until a real topic/credential exists.
func NewWorkerWithPublisher(ctx context.Context, cfg config.WorkerConfig, publisher outbox.Publisher) (*Worker, error) {
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
	if err := checkWorkerSchemas(ctx, resources, cfg.Development); err != nil {
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
		return nil, oops.In("auth_worker").Code("worker.hostname_failed").Wrap(err)
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
			args := []any{"event_id", attempt.EventID, "attempt", attempt.Attempts, "result", attempt.Result}
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
	var runDomainOutbox func(context.Context) error
	if publisher != nil {
		relay, relayErr := outbox.New(outbox.NewPostgresRepository(resources.DB), publisher, outbox.Config{
			WorkerID: hostname + ":auth-outbox:" + uuid.NewString(), BatchSize: cfg.Audit.BatchSize,
			PollInterval: cfg.Audit.PollInterval, Lease: cfg.Audit.Lease, MaxAttempts: cfg.Audit.MaxAttempts,
			BaseBackoff: cfg.Audit.BaseBackoff, MaxBackoff: cfg.Audit.MaxBackoff,
		})
		if relayErr != nil {
			if profiler != nil {
				_ = profiler.Stop()
			}
			_ = resources.Close()
			_ = shutdownMetrics(metrics, cfg.Lifecycle.ShutdownTimeout)
			return nil, relayErr
		}
		runDomainOutbox = relay.Run
	}
	statusService := session.NewStatusService(session.StatusServiceOptions{
		Cache:  session.NewRedisStatusCache(resources.SessionStatusRedis),
		Source: session.NewPostgresStatusSource(resources.DB, cfg.Auth.AccessTTL),
		Config: session.StatusServiceConfig{
			ActiveTTL: cfg.Auth.SessionStatusCacheTTL, AccessTTL: cfg.Auth.AccessTTL,
			FallbackTimeout: cfg.Auth.SessionStatusDBTimeout, MaxFallbacks: cfg.Auth.SessionStatusMaxDBLookups,
		},
	})
	statusRelay, err := session.NewStatusProjectionRelay(outbox.NewPostgresRepository(resources.DB), statusService, outbox.Config{
		WorkerID: hostname + ":session-status:" + uuid.NewString(), BatchSize: cfg.Audit.BatchSize,
		PollInterval: time.Second, Lease: cfg.Audit.Lease, MaxAttempts: cfg.Audit.MaxAttempts,
		BaseBackoff: time.Second, MaxBackoff: 30 * time.Second,
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
			return checkWorkerSourceDatabase(ctx, resources.DB, cfg.Development)
		},
		"session_status_redis": func(ctx context.Context) error {
			return resources.SessionStatusRedis.Ping(ctx).Err()
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
		cfg:                 cfg,
		resources:           resources,
		metrics:             metrics,
		health:              healthState,
		adminHTTP:           adminHTTP,
		runAudit:            auditWorker.Run,
		runDomainOutbox:     runDomainOutbox,
		runStatusProjection: statusRelay.Run,
		cleanup: func(ctx context.Context) (int64, error) {
			return audit.DeleteDeliveredBefore(ctx, resources.DB, time.Now().UTC().Add(-cfg.Audit.Retention), cfg.Audit.CleanupLimit)
		},
		cleanupInterval: time.Hour,
		profiler:        profiler,
	}, nil
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
	if err := auth.CheckSchema(ctx, db); err != nil {
		return err
	}
	if development.VirtualAdaptersEnabled {
		return auth.CheckDevelopmentSchema(ctx, db)
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
	backgroundCount := 2
	backgroundResult := make(chan error, 3)
	go func() { backgroundResult <- w.runAudit(runCtx) }()
	go func() { backgroundResult <- w.runStatusProjection(runCtx) }()
	if w.runDomainOutbox != nil {
		backgroundCount++
		go func() { backgroundResult <- w.runDomainOutbox(runCtx) }()
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
				logger.Error(runCtx, "audit.outbox.cleanup_failed", logger.Err(err))
				continue
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
