package app

import (
	"context"

	platformlogger "github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/coupon-service/internal/application/commandworker"
	appeventing "github.com/Medikong/services/services/coupon-service/internal/application/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/application/jobs"
	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/Medikong/services/services/coupon-service/internal/application/projection"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	domaineventing "github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/platform/commandfailure"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/Medikong/services/services/coupon-service/internal/platform/observability"
	"github.com/Medikong/services/services/coupon-service/internal/platform/workerstore"
	workertransport "github.com/Medikong/services/services/coupon-service/internal/transport/worker"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

func buildWorkerGroup(ctx context.Context, resources Resources, cfg config.WorkerConfig, metrics *observability.Metrics, external externalPorts) (*workertransport.Group, *workertransport.State, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	parts, err := newComponents(resources, cfg.Domain, external)
	if err != nil {
		return nil, nil, err
	}
	store, err := workerstore.NewPostgresStore(resources.DB)
	if err != nil {
		return nil, nil, err
	}
	projector, err := projection.New(resources.DB, "coupon-read-model-projector-v1")
	if err != nil {
		return nil, nil, err
	}
	externalPublisher, err := appeventing.NewExternalPublisher(parts.external.settlement, parts.external.notifications)
	if err != nil {
		return nil, nil, err
	}
	processor := policy.NewProcessor(resources.DB)
	publisher, err := appeventing.NewMultiPublisher(
		appeventing.NewPolicyPublisher(processor, "coupon-policy-processor-v1"),
		projector,
		externalPublisher,
	)
	if err != nil {
		return nil, nil, err
	}

	workerID := cfg.Service.Name + ":" + uuid.NewString()
	retryPolicy := eventRetryPolicy(cfg.Policy)
	relay, err := appeventing.NewRelay(workerID+":outbox", domaineventing.NewPostgresOutbox(resources.DB), publisher, retryPolicy)
	if err != nil {
		return nil, nil, err
	}

	gateOptions := QuantityGateOptions{}
	if resources.Redis != nil {
		gate, gateErr := campaign.NewRedisGate(resources.Redis, "coupon", cfg.Domain.IdempotencyTTL)
		if gateErr != nil {
			return nil, nil, gateErr
		}
		gateOptions = QuantityGateOptions{
			Gate: gate, FailureMode: cfg.Redis.FailureMode,
			FailureHook: func(operation string, cause error) {
				metrics.RecordRedisGate("error")
				platformlogger.Default().WarnContext(context.Background(), "coupon quantity gate failed", "operation", operation, platformlogger.Err(cause))
			},
		}
	} else if resources.RedisStartupError != nil {
		metrics.RecordRedisGate("db_fallback")
		platformlogger.Default().WarnContext(ctx, "coupon Redis gate unavailable; PostgreSQL remains authoritative", platformlogger.Err(resources.RedisStartupError))
	}
	dispatcher, err := newCommandDispatcher(parts, cfg.Domain, gateOptions)
	if err != nil {
		return nil, nil, err
	}
	failureSink, err := commandfailure.NewPostgresSink(resources.DB, nil, cfg.Policy.MaxAttempts)
	if err != nil {
		return nil, nil, err
	}
	commandJob, err := commandworker.New(workerID+":commands", domaineventing.NewPostgresCommandQueue(resources.DB), dispatcher, commandRetryPolicy(cfg.Policy), failureSink)
	if err != nil {
		return nil, nil, err
	}

	jobPolicy := jobs.Policy{
		BatchSize: cfg.Policy.BatchSize, PageSize: cfg.Policy.BatchSize, Lease: cfg.Policy.Lease,
		AttemptTimeout: cfg.Policy.AttemptTimeout, MaxAttempts: cfg.Policy.MaxAttempts,
		BaseBackoff: cfg.Policy.BaseBackoff, MaxBackoff: cfg.Policy.MaxBackoff,
	}
	bulkJob, err := jobs.NewBulkIssuePlannerWorker(workerID+":bulk", store, parts.bulkRepo, parts.external.audience, jobPolicy, nil)
	if err != nil {
		return nil, nil, err
	}
	issueJob, err := jobs.NewIssueRetryWorker(workerID+":issue", store, parts.issueRepo, jobPolicy, nil)
	if err != nil {
		return nil, nil, err
	}
	recoveryJob, err := jobs.NewRecoveryWorker(workerID+":recovery", store, parts.recoveryRepo, parts.redemptions, jobPolicy, nil)
	if err != nil {
		return nil, nil, err
	}
	expiryJob, err := jobs.NewCouponExpiryWorker(workerID+":expiry", store, parts.operations, jobPolicy, nil)
	if err != nil {
		return nil, nil, err
	}

	state := workertransport.NewState()
	namedJobs := []workertransport.NamedJob{
		observedJob("outbox", relay, metrics),
		observedJob("commands", commandJob, metrics),
		observedJob("bulk_issue", bulkJob, metrics),
		observedJob("issue_retry", issueJob, metrics),
		observedJob("recovery", recoveryJob, metrics),
		observedJob("expiry", expiryJob, metrics),
	}
	group, err := workertransport.NewGroup(namedJobs, cfg.Policy.PollInterval, state, platformlogger.Default())
	if err != nil {
		return nil, nil, err
	}
	return group, state, nil
}

func eventRetryPolicy(value config.WorkerPolicy) appeventing.Policy {
	return appeventing.Policy{
		BatchSize: value.BatchSize, Lease: value.Lease, AttemptTimeout: value.AttemptTimeout,
		MaxAttempts: value.MaxAttempts, BaseBackoff: value.BaseBackoff, MaxBackoff: value.MaxBackoff,
	}
}

func commandRetryPolicy(value config.WorkerPolicy) commandworker.Policy {
	return commandworker.Policy{
		BatchSize: value.BatchSize, Lease: value.Lease, AttemptTimeout: value.AttemptTimeout,
		MaxAttempts: value.MaxAttempts, BaseBackoff: value.BaseBackoff, MaxBackoff: value.MaxBackoff,
	}
}

type metricsJob struct {
	name    string
	job     workertransport.Job
	metrics *observability.Metrics
}

func observedJob(name string, job workertransport.Job, metrics *observability.Metrics) workertransport.NamedJob {
	return workertransport.NamedJob{Name: name, Job: metricsJob{name: name, job: job, metrics: metrics}}
}

func (j metricsJob) RunOnce(ctx context.Context) (int, error) {
	if j.job == nil || j.metrics == nil {
		return 0, oops.In("coupon_worker").Code("coupon.worker_observer_invalid").New("worker observation dependencies are required")
	}
	count, err := j.job.RunOnce(ctx)
	if err != nil && ctx.Err() != nil {
		return count, err
	}
	result := "success"
	if err != nil {
		result = "failed"
	}
	j.metrics.RecordWorker(j.name, result)
	return count, err
}
