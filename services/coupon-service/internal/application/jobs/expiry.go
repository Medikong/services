package jobs

import (
	"context"
	"strings"
	"time"

	operationsapp "github.com/Medikong/services/services/coupon-service/internal/application/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
	"github.com/samber/oops"
)

type CouponExpirer interface {
	ExpireUserCoupon(context.Context, operationsapp.ExpireUserCouponInput, operationsapp.Metadata) (usercoupon.Mutation, error)
}

type CouponExpiryWorker struct {
	workerID string
	store    ExpiryStore
	expirer  CouponExpirer
	policy   Policy
	classify FailureClassifier
	now      func() time.Time
}

func NewCouponExpiryWorker(workerID string, store ExpiryStore, expirer CouponExpirer, policy Policy, classifier FailureClassifier) (*CouponExpiryWorker, error) {
	if strings.TrimSpace(workerID) == "" || store == nil || expirer == nil {
		return nil, oops.In("coupon_expiry_worker").Code("coupon.expiry_worker_dependencies_required").New("expiry worker id, store, and operations service are required")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if classifier == nil {
		classifier = DefaultFailureClassifier{}
	}
	return &CouponExpiryWorker{workerID: workerID, store: store, expirer: expirer, policy: policy, classify: classifier, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (w *CouponExpiryWorker) RunOnce(ctx context.Context) (int, error) {
	now := w.now().UTC()
	leases, err := w.store.ClaimExpirations(ctx, w.workerID, now, w.policy.BatchSize, w.policy.Lease)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, lease := range leases {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := w.runExpiration(ctx, lease); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (w *CouponExpiryWorker) runExpiration(ctx context.Context, lease ExpiryLease) error {
	now := w.now().UTC()
	leaseUntil := now.Add(w.policy.Lease)
	attemptCtx, cancel := attemptContext(ctx, w.policy.AttemptTimeout)
	_, err := w.expirer.ExpireUserCoupon(attemptCtx, operationsapp.ExpireUserCouponInput{
		UserCouponID: lease.Coupon.ID, ExpectedVersion: lease.Coupon.Version, AsOf: lease.Coupon.ExpiresAt,
	}, operationsapp.Metadata{
		BusinessKey: "expiry:" + lease.Coupon.ID, CorrelationID: "expiry:" + lease.Coupon.ID,
		CausationID: "CouponExpiryWorker", TraceID: "expiry:" + lease.Coupon.ID,
		RequestedAt: now, LeaseUntil: leaseUntil,
		ExpiresAt: leaseUntil.Add(w.policy.MaxBackoff + w.policy.AttemptTimeout),
	})
	cancel()
	if err == nil {
		return w.store.CompleteExpiration(ctx, lease.Coupon.ID, w.workerID, now)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failure := w.classify.Classify(err)
	terminal := !failure.Retryable || lease.Attempt >= w.policy.MaxAttempts
	next := now.Add(retryDelay(lease.Attempt, w.policy.BaseBackoff, w.policy.MaxBackoff))
	if recordErr := w.store.FailExpiration(ctx, lease, w.workerID, next, failure, now, terminal); recordErr != nil {
		return oops.Join(err, recordErr)
	}
	return nil
}
