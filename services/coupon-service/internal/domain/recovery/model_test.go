package recovery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecoveryPreservesCorrelationAcrossAttempt(t *testing.T) {
	now := time.Now().UTC()
	recovery, failureEvent, err := RecordFailure(Failure{
		RecoveryID: "rcvy_12345678", RedemptionID: "redm_12345678", OriginalOperationType: OperationConfirm,
		OriginalPayloadRef: "payload-1", OriginalPayloadHash: "sha256:payload",
		BusinessKey: "order-1:coupon-1:confirm", FailureCode: "upstream_timeout", OccurredAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, "EVT.A.19-40", failureEvent.DocumentID)

	attempt, retryEvent, err := recovery.RequestRetry(RetryRequest{
		ExpectedVersion: 0, AttemptID: "att_12345678", NextAttemptAt: now.Add(time.Minute),
		ReasonCode: "manual_retry", OperationRequestRef: "task-1", ApprovalRef: "approval-1", RequestedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, recovery.BusinessKey, attempt.BusinessKey)
	require.Equal(t, "EVT.A.19-39", retryEvent.DocumentID)
	require.NoError(t, recovery.Lease(1, attempt.ID, recovery.BusinessKey, "worker-1", now.Add(2*time.Minute), now.Add(time.Second)))

	resultEvent, err := recovery.RecordResult(ReplayResult{
		ExpectedVersion: 2, AttemptID: attempt.ID, BusinessKey: recovery.BusinessKey,
		Kind: ResultAlreadyApplied, ResultRef: "redemption-1", RecordedAt: now.Add(2 * time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, recovery.Status)
	require.Equal(t, "redemption-1", recovery.ResultRef)
	require.Equal(t, "EVT.A.19-26", resultEvent.DocumentID)
}

func TestRecoveryRejectsStaleAttemptResult(t *testing.T) {
	now := time.Now().UTC()
	recovery := retryingRecovery(now)
	_, err := recovery.RecordResult(ReplayResult{
		ExpectedVersion: recovery.Version, AttemptID: "att_87654321", BusinessKey: recovery.BusinessKey,
		Kind: ResultTransitioned, ResultRef: "redemption-1", RecordedAt: now,
	})
	require.Error(t, err)
	require.Equal(t, StatusRetrying, recovery.Status)
	require.Empty(t, recovery.ResultRef)
}

func TestFailedRecoveryCanRetryThenFinalize(t *testing.T) {
	now := time.Now().UTC()
	recovery := retryingRecovery(now)
	next := now.Add(time.Hour)
	event, err := recovery.RecordResult(ReplayResult{
		ExpectedVersion: recovery.Version, AttemptID: recovery.CurrentAttemptID, BusinessKey: recovery.BusinessKey,
		Kind: ResultFailed, FailureCode: "still_unavailable", Retryable: true, NextAttemptAt: &next, RecordedAt: now,
	})
	require.NoError(t, err)
	require.Equal(t, StatusRetryFailed, recovery.Status)
	require.Equal(t, "EVT.A.19-27", event.DocumentID)

	event, err = recovery.Finalize(Finalization{
		ExpectedVersion: recovery.Version, ReasonCode: "retry_exhausted",
		OperationRequestRef: "task-2", ApprovalRef: "approval-2", FinalizedAt: now.Add(time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, StatusFailedFinal, recovery.Status)
	require.Equal(t, "EVT.A.19-30", event.DocumentID)
}

func retryingRecovery(now time.Time) Recovery {
	attempt := Attempt{
		RecoveryID: "rcvy_12345678", ID: "att_12345678", BusinessKey: "order-1:coupon-1:confirm",
		Status: AttemptRetrying, CreatedAt: now, StartedAt: &now,
	}
	return Recovery{
		ID: "rcvy_12345678", RedemptionID: "redm_12345678", OriginalOperationType: OperationConfirm, OriginalPayloadRef: "payload-1",
		OriginalPayloadHash: "sha256:payload", BusinessKey: attempt.BusinessKey, Status: StatusRetrying,
		CurrentAttemptID: attempt.ID, CurrentAttempt: &attempt, AttemptCount: 1, FailureCode: "upstream_timeout",
		Version: 2, CreatedAt: now, UpdatedAt: now,
	}
}
