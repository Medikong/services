package jobs

import (
	"context"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type BulkIssuePlannerWorker struct {
	workerID string
	store    BulkStore
	jobs     BulkLeaser
	audience ports.BulkAudiencePort
	policy   Policy
	classify FailureClassifier
	now      func() time.Time
}

type BulkLeaser interface {
	Lease(context.Context, string, int64, string, time.Time, time.Time) (bulk.Job, error)
}

func NewBulkIssuePlannerWorker(workerID string, store BulkStore, repository BulkLeaser, audience ports.BulkAudiencePort, policy Policy, classifier FailureClassifier) (*BulkIssuePlannerWorker, error) {
	if strings.TrimSpace(workerID) == "" || store == nil || repository == nil || audience == nil {
		return nil, oops.In("coupon_bulk_planner_worker").Code("coupon.bulk_worker_dependencies_required").New("bulk worker id, store, repository, and audience port are required")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if classifier == nil {
		classifier = DefaultFailureClassifier{}
	}
	return &BulkIssuePlannerWorker{
		workerID: workerID, store: store, jobs: repository, audience: audience,
		policy: policy, classify: classifier, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (w *BulkIssuePlannerWorker) RunOnce(ctx context.Context) (int, error) {
	now := w.now().UTC()
	leases, err := w.store.ClaimBulkJobs(ctx, w.workerID, now, w.policy.BatchSize, w.policy.Lease)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, lease := range leases {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := w.runBulkPage(ctx, lease); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (w *BulkIssuePlannerWorker) runBulkPage(ctx context.Context, lease BulkLease) error {
	now := w.now().UTC()
	leased, err := w.jobs.Lease(ctx, lease.Job.ID, lease.Job.Version, w.workerID, now.Add(w.policy.Lease), now)
	if err != nil {
		return w.recordBulkFailure(ctx, lease, err, now)
	}
	attemptCtx, cancel := attemptContext(ctx, w.policy.AttemptTimeout)
	page, err := w.audience.Page(attemptCtx, leased.AudienceDefinitionRef, leased.EvaluationAsOf, lease.Cursor, w.policy.PageSize)
	cancel()
	if err != nil {
		lease.Job = leased
		return w.recordBulkFailure(ctx, lease, err, now)
	}
	if page.Snapshot != leased.AudienceSnapshot {
		lease.Job = leased
		return w.recordBulkFailure(ctx, lease, oops.In("coupon_bulk_planner_worker").Code("coupon.bulk_audience_snapshot_mismatch").New("audience page does not belong to the registered immutable snapshot"), now)
	}
	if page.NextCursor != "" && page.NextCursor == lease.Cursor {
		lease.Job = leased
		return w.recordBulkFailure(ctx, lease, oops.In("coupon_bulk_planner_worker").Code("coupon.bulk_audience_cursor_stalled").New("audience cursor did not advance"), now)
	}
	targets, err := bulkTargets(leased.ID, page.UserIDs)
	if err != nil {
		lease.Job = leased
		return w.recordBulkFailure(ctx, lease, err, now)
	}
	_, err = w.store.CommitBulkPage(ctx, BulkPageCommit{
		BulkJobID: leased.ID, ExpectedVersion: leased.Version, WorkerID: w.workerID,
		CurrentCursor: lease.Cursor, NextCursor: page.NextCursor, PageNumber: lease.PageNumber + 1,
		PlannedTargetCount: lease.PlannedTargetCount, Targets: targets,
		Finished: page.NextCursor == "", OccurredAt: now,
	})
	return err
}

func (w *BulkIssuePlannerWorker) recordBulkFailure(ctx context.Context, lease BulkLease, cause error, now time.Time) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failure := w.classify.Classify(cause)
	attempt := lease.Job.AttemptCount
	if attempt < 1 {
		attempt = 1
	}
	terminal := !failure.Retryable || attempt >= w.policy.MaxAttempts
	next := now.Add(retryDelay(attempt, w.policy.BaseBackoff, w.policy.MaxBackoff))
	if err := w.store.FailBulkJob(ctx, lease.Job.ID, w.workerID, next, failure, now, terminal); err != nil {
		return oops.Join(cause, err)
	}
	return nil
}

func bulkTargets(jobID string, userIDs []string) ([]BulkTarget, error) {
	seen := make(map[string]struct{}, len(userIDs))
	targets := make([]BulkTarget, 0, len(userIDs))
	for _, raw := range userIDs {
		userID := strings.TrimSpace(raw)
		if userID == "" {
			return nil, oops.In("coupon_bulk_planner_worker").Code("coupon.bulk_audience_user_invalid").New("audience page contains an empty user id")
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		businessKey := jobID + ":" + userID
		targets = append(targets, BulkTarget{
			UserID: userID, BusinessKey: businessKey,
			IssueRequestID: "ireq_" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("issue_request\x00CMD.A.19-13\x00"+businessKey)).String(),
		})
	}
	return targets, nil
}
