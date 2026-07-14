package jobs

import (
	"context"
	"strings"
	"time"

	redemptionapp "github.com/Medikong/services/services/coupon-service/internal/application/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/samber/oops"
)

type RedemptionReplayer interface {
	Replay(context.Context, redemptionapp.ReplayInput, redemptionapp.Metadata) (redemptionapp.RecoveryResultCommand, error)
}

type RecoveryWorker struct {
	workerID string
	store    RecoveryStore
	repo     RecoveryLeaser
	replayer RedemptionReplayer
	policy   Policy
	classify FailureClassifier
	now      func() time.Time
}

type RecoveryLeaser interface {
	Lease(context.Context, string, int64, string, string, string, time.Time, time.Time) (recovery.Recovery, error)
}

func NewRecoveryWorker(workerID string, store RecoveryStore, repository RecoveryLeaser, replayer RedemptionReplayer, policy Policy, classifier FailureClassifier) (*RecoveryWorker, error) {
	if strings.TrimSpace(workerID) == "" || store == nil || repository == nil || replayer == nil {
		return nil, oops.In("coupon_recovery_worker").Code("coupon.recovery_worker_dependencies_required").New("recovery worker id, store, repository, and replayer are required")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if classifier == nil {
		classifier = DefaultFailureClassifier{}
	}
	return &RecoveryWorker{workerID: workerID, store: store, repo: repository, replayer: replayer, policy: policy, classify: classifier, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (w *RecoveryWorker) RunOnce(ctx context.Context) (int, error) {
	now := w.now().UTC()
	leases, err := w.store.ClaimRecoveries(ctx, w.workerID, now, w.policy.BatchSize, w.policy.Lease)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, lease := range leases {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := w.runRecovery(ctx, lease); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (w *RecoveryWorker) runRecovery(ctx context.Context, lease RecoveryLease) error {
	now := w.now().UTC()
	current := lease.Recovery
	if current.Status == recovery.StatusRetryPending {
		leased, err := w.repo.Lease(ctx, current.ID, current.Version, current.CurrentAttemptID, current.BusinessKey, w.workerID, now.Add(w.policy.Lease), now)
		if err != nil {
			return w.recordRecoveryFailure(ctx, lease, err, now)
		}
		current = leased
		lease.Recovery = leased
	}
	if current.Status != recovery.StatusRetrying || current.CurrentAttempt == nil {
		return nil
	}
	leaseUntil := now.Add(w.policy.Lease)
	attemptCtx, cancel := attemptContext(ctx, w.policy.AttemptTimeout)
	_, err := w.replayer.Replay(attemptCtx, redemptionapp.ReplayInput{
		RecoveryID: current.ID, AttemptID: current.CurrentAttemptID, BusinessKey: current.BusinessKey,
		RedemptionID:          current.RedemptionID,
		OriginalOperationType: current.OriginalOperationType, OriginalPayloadRef: current.OriginalPayloadRef,
		OriginalPayloadHash: current.OriginalPayloadHash,
	}, redemptionapp.Metadata{
		BusinessKey: current.BusinessKey, CorrelationID: "recovery:" + current.ID,
		CausationID: current.CurrentAttemptID, TraceID: "recovery:" + current.ID, RequestedAt: now, LeaseUntil: leaseUntil,
		ExpiresAt: leaseUntil.Add(w.policy.MaxBackoff + w.policy.AttemptTimeout),
	})
	cancel()
	if err != nil {
		return w.recordRecoveryFailure(ctx, lease, err, now)
	}
	// Replay persists EVT.A.19-41 with the immutable recovery correlation in the
	// CouponRedemption transaction. POLICY.A.19-22 owns the later CMD.A.19-33.
	return nil
}

func (w *RecoveryWorker) recordRecoveryFailure(ctx context.Context, lease RecoveryLease, cause error, now time.Time) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failure := w.classify.Classify(cause)
	attempt := lease.WorkerAttempt
	if attempt < 1 {
		attempt = 1
	}
	terminal := !failure.Retryable || attempt >= w.policy.MaxAttempts
	next := now.Add(retryDelay(attempt, w.policy.BaseBackoff, w.policy.MaxBackoff))
	if err := w.store.FailRecovery(ctx, lease, w.workerID, next, failure, now, terminal); err != nil {
		return oops.Join(cause, err)
	}
	return nil
}
