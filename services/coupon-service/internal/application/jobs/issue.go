package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/samber/oops"
)

type IssueRetryWorker struct {
	workerID string
	store    IssueStore
	requests IssueProcessor
	policy   Policy
	classify FailureClassifier
	now      func() time.Time
}

type IssueProcessor interface {
	MarkProcessing(context.Context, string, int64, issuerequest.Command) (issuerequest.Mutation, error)
}

func NewIssueRetryWorker(workerID string, store IssueStore, requests IssueProcessor, policy Policy, classifier FailureClassifier) (*IssueRetryWorker, error) {
	if strings.TrimSpace(workerID) == "" || store == nil || requests == nil {
		return nil, oops.In("coupon_issue_retry_worker").Code("coupon.issue_worker_dependencies_required").New("issue worker id, store, and repository are required")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if classifier == nil {
		classifier = DefaultFailureClassifier{}
	}
	return &IssueRetryWorker{workerID: workerID, store: store, requests: requests, policy: policy, classify: classifier, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (w *IssueRetryWorker) RunOnce(ctx context.Context) (int, error) {
	now := w.now().UTC()
	leases, err := w.store.ClaimIssueRequests(ctx, w.workerID, now, w.policy.BatchSize, w.policy.Lease)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, lease := range leases {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := w.runIssue(ctx, lease); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (w *IssueRetryWorker) runIssue(ctx context.Context, lease IssueLease) error {
	now := w.now().UTC()
	current := lease.Request
	if current.Status == issuerequest.StatusPending || current.Status == issuerequest.StatusRetryPending {
		digest := sha256.Sum256([]byte(current.ID + "\x00" + current.BusinessKey + "\x00" + string(current.Status)))
		attemptCtx, cancel := attemptContext(ctx, w.policy.AttemptTimeout)
		mutation, err := w.requests.MarkProcessing(attemptCtx, current.ID, current.Version, issuerequest.Command{
			OperationType: fmt.Sprintf("coupon.issue.processing.%d", current.RetryCount), BusinessKey: current.BusinessKey,
			RequestHash: hex.EncodeToString(digest[:]), CorrelationID: "issue-retry:" + current.ID,
			CausationID: "IssueRetryWorker", TraceID: "issue-retry:" + current.ID, OccurredAt: now,
			LeaseUntil: now.Add(w.policy.Lease), ExpiresAt: now.Add(w.policy.Lease + w.policy.MaxBackoff + w.policy.AttemptTimeout),
		})
		cancel()
		if err != nil {
			return w.recordIssueFailure(ctx, lease, err, now)
		}
		lease.Request = mutation.Request
	}
	if lease.Request.Status != issuerequest.StatusProcessing {
		return nil
	}
	if err := w.store.EnqueueIssueCommand(ctx, lease, now); err != nil {
		return w.recordIssueFailure(ctx, lease, err, now)
	}
	return nil
}

func (w *IssueRetryWorker) recordIssueFailure(ctx context.Context, lease IssueLease, cause error, now time.Time) error {
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
	if err := w.store.FailIssueRequest(ctx, lease, w.workerID, next, failure, now, terminal); err != nil {
		return oops.Join(cause, err)
	}
	return nil
}
