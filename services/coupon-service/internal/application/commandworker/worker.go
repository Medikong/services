package commandworker

import (
	"context"
	"time"

	appeventing "github.com/Medikong/services/services/coupon-service/internal/application/eventing"
	domaineventing "github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/samber/oops"
)

type Dispatcher interface {
	Dispatch(context.Context, domaineventing.CommandRequest) (string, error)
}

// FailureSink converts command-specific execution failures into a durable
// follow-up command. Returning handled=true means the original queue item can
// be completed because the follow-up now owns the business outcome.
type FailureSink interface {
	HandleCommandFailure(context.Context, domaineventing.CommandRequest, error, time.Time, bool) (string, bool, error)
}

type Policy struct {
	BatchSize      int
	Lease          time.Duration
	AttemptTimeout time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

type Worker struct {
	workerID   string
	queue      domaineventing.CommandQueue
	dispatcher Dispatcher
	failure    FailureSink
	policy     Policy
	now        func() time.Time
}

func New(workerID string, queue domaineventing.CommandQueue, dispatcher Dispatcher, policy Policy, failures ...FailureSink) (*Worker, error) {
	if workerID == "" || queue == nil || dispatcher == nil || policy.BatchSize < 1 || policy.Lease <= 0 ||
		policy.AttemptTimeout <= 0 || policy.MaxAttempts < 1 || policy.BaseBackoff <= 0 || policy.MaxBackoff < policy.BaseBackoff {
		return nil, oops.In("coupon_command_worker").Code("coupon.command_worker_config_invalid").New("command worker configuration is invalid")
	}
	if len(failures) > 1 {
		return nil, oops.In("coupon_command_worker").Code("coupon.command_worker_config_invalid").New("command worker accepts at most one failure sink")
	}
	var failure FailureSink
	if len(failures) == 1 {
		failure = failures[0]
	}
	return &Worker{workerID: workerID, queue: queue, dispatcher: dispatcher, failure: failure, policy: policy, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	items, err := w.queue.ClaimCommands(ctx, w.workerID, w.policy.BatchSize, w.policy.Lease)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		attemptCtx, cancel := context.WithTimeout(ctx, w.policy.AttemptTimeout)
		resultRef, dispatchErr := w.dispatcher.Dispatch(attemptCtx, item)
		cancel()
		if dispatchErr == nil {
			if err := w.queue.CompleteCommand(ctx, item.ID, w.workerID, resultRef); err != nil {
				return 0, err
			}
			continue
		}
		terminal := item.AttemptCount >= w.policy.MaxAttempts
		next := w.now().Add(appeventing.RetryDelay(item.AttemptCount, w.policy.BaseBackoff, w.policy.MaxBackoff))
		if w.failure != nil {
			resultRef, handled, failureErr := w.failure.HandleCommandFailure(ctx, item, dispatchErr, next, terminal)
			if failureErr != nil {
				return 0, oops.Join(dispatchErr, failureErr)
			}
			if handled {
				if err := w.queue.CompleteCommand(ctx, item.ID, w.workerID, resultRef); err != nil {
					return 0, err
				}
				continue
			}
		}
		if recordErr := w.queue.FailCommand(ctx, item.ID, w.workerID, next, "COUPON_COMMAND_EXECUTION_FAILED", terminal); recordErr != nil {
			return 0, oops.Join(dispatchErr, recordErr)
		}
	}
	return len(items), nil
}
