package issuerequest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIssueRequestHappyAndRetryPaths(t *testing.T) {
	request := Request{Status: StatusAccepted, Version: 1}
	pending, err := request.MarkPending()
	require.NoError(t, err)
	processing, err := pending.MarkProcessing()
	require.NoError(t, err)
	next := time.Now().UTC().Add(time.Minute)
	failed, err := processing.RecordFailure("database_timeout", true, &next)
	require.NoError(t, err)
	retry, err := failed.Retry(next)
	require.NoError(t, err)
	require.Equal(t, StatusRetryPending, retry.Status)
	require.Equal(t, 1, retry.RetryCount)
	processing, err = retry.MarkProcessing()
	require.NoError(t, err)
	completed, err := processing.Complete("user-coupon-1")
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, completed.Status)
	_, err = completed.FinalizeFailure("too_late")
	require.ErrorIs(t, err, ErrInvalidTransition)
}

func TestIssueRequestRejectsMissingResultAndFailure(t *testing.T) {
	request := Request{Status: StatusAccepted, Version: 1}
	_, err := request.Reject("")
	require.ErrorIs(t, err, ErrInvalidTransition)
	pending, err := request.MarkPending()
	require.NoError(t, err)
	processing, err := pending.MarkProcessing()
	require.NoError(t, err)
	_, err = processing.Complete("")
	require.ErrorIs(t, err, ErrInvalidTransition)
}

func TestIssueFailureWaitsForApprovedFinalizationWhenRetryStops(t *testing.T) {
	request := Request{Status: StatusProcessing, Version: 3}
	failed, err := request.RecordFailure("payload_invalid", false, nil)
	require.NoError(t, err)
	require.Equal(t, StatusFailedRetryable, failed.Status)
	require.Nil(t, failed.NextAttemptAt)
	finalized, err := failed.FinalizeFailure("payload_invalid")
	require.NoError(t, err)
	require.Equal(t, StatusFailedFinal, finalized.Status)
}
