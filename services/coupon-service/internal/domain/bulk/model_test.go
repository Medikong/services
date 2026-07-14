package bulk

import (
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/stretchr/testify/require"
)

func TestRegisterKeepsOnlyAudienceReferenceSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC)
	job, event, err := Register(Registration{
		JobID: "bjob_12345678", CampaignID: "camp_12345678", OwnerServiceID: "operations-service",
		AudienceSnapshot: shared.SnapshotRef{
			SourceRef:     shared.ExternalRef{Context: "audience", Type: "definition", ID: "segment-42"},
			SourceVersion: "7", CapturedAt: now, PayloadHash: "sha256:" + strings.Repeat("a", 43),
		},
		EvaluationAsOf: now, OperationRequestRef: "task-1", ApprovalRef: "approval-1", CreatedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, "segment-42", job.AudienceDefinitionRef)
	require.Equal(t, StatusRegistered, job.Status)
	require.False(t, job.PlanningComplete)
	require.Equal(t, "EVT.A.19-16", event.DocumentID)
}

func TestAggregateResultCompletesOnlyAfterAllTargetsAreTerminal(t *testing.T) {
	now := time.Now().UTC()
	job := validJob(now)
	target := int64(3)
	event, err := job.AggregateResult(0, ResultDelta{TargetCount: &target, SucceededCount: 1, ResultRef: "issue-1", RecordedAt: now})
	require.NoError(t, err)
	require.Empty(t, event.DocumentID)
	require.Equal(t, StatusRunning, job.Status)

	event, err = job.AggregateResult(1, ResultDelta{SucceededCount: 1, RejectedCount: 1, ResultRef: "issue-3", RecordedAt: now.Add(time.Second)})
	require.NoError(t, err)
	require.Equal(t, StatusCompletedWithFailures, job.Status)
	require.Equal(t, "EVT.A.19-18", event.DocumentID)
}

func TestAggregateResultDoesNotCompleteWhileAudiencePlanningIsOpen(t *testing.T) {
	now := time.Now().UTC()
	job := validJob(now)
	job.PlanningComplete = false
	target := int64(1)

	event, err := job.AggregateResult(0, ResultDelta{
		TargetCount: &target, SucceededCount: 1, ResultRef: "issue-1", RecordedAt: now,
	})
	require.NoError(t, err)
	require.Empty(t, event.DocumentID)
	require.Equal(t, StatusRunning, job.Status)
	require.Equal(t, int64(1), job.SucceededCount)
}

func TestAggregateResultRejectsOverflowAndStaleVersion(t *testing.T) {
	now := time.Now().UTC()
	job := validJob(now)
	target := int64(1)
	_, err := job.AggregateResult(1, ResultDelta{TargetCount: &target, SucceededCount: 1, ResultRef: "issue-1", RecordedAt: now})
	require.Error(t, err)
	_, err = job.AggregateResult(0, ResultDelta{TargetCount: &target, SucceededCount: 2, ResultRef: "issue-1", RecordedAt: now})
	require.Error(t, err)
	require.Equal(t, int64(0), job.TargetCount)
	require.Equal(t, int64(0), job.SucceededCount)
}

func TestLeaseRejectsConcurrentOwner(t *testing.T) {
	now := time.Now().UTC()
	job := validJob(now)
	require.NoError(t, job.Lease(0, "worker-a", now.Add(time.Minute), now))
	err := job.Lease(1, "worker-b", now.Add(2*time.Minute), now.Add(time.Second))
	require.Error(t, err)
}

func TestValidateRejectsIncompleteTerminalCounts(t *testing.T) {
	job := validJob(time.Now().UTC())
	job.Status = StatusCompleted
	job.TargetCount = 3
	job.SucceededCount = 1
	require.Error(t, job.Validate())
}

func validJob(now time.Time) Job {
	return Job{
		ID: "bjob_12345678", CampaignID: "camp_12345678", OwnerServiceID: "operations-service", AudienceDefinitionRef: "segment-42",
		AudienceSnapshot: shared.SnapshotRef{
			SourceRef:     shared.ExternalRef{Context: "audience", Type: "definition", ID: "segment-42"},
			SourceVersion: "7", CapturedAt: now, PayloadHash: "sha256:" + strings.Repeat("a", 43),
		},
		EvaluationAsOf: now, Status: StatusRegistered, PlanningComplete: true, OperationRequestRef: "task-1", ApprovalRef: "approval-1",
		CreatedAt: now, UpdatedAt: now,
	}
}
