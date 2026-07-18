package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type recordingStatusOutbox struct {
	events       []outbox.ClaimedEvent
	marked       []uuid.UUID
	released     []uuid.UUID
	releaseDelay time.Duration
	deadLettered []uuid.UUID
	claimedType  string
}

func (r *recordingStatusOutbox) ClaimPublishBatchByType(_ context.Context, _, eventType string, _ int, _ time.Duration) ([]outbox.ClaimedEvent, error) {
	r.claimedType = eventType
	return r.events, nil
}

func (r *recordingStatusOutbox) MarkPublished(_ context.Context, eventID uuid.UUID, _ string) error {
	r.marked = append(r.marked, eventID)
	return nil
}

func (r *recordingStatusOutbox) ReleaseForRetry(_ context.Context, eventID uuid.UUID, _ string, delay time.Duration, _ string) error {
	r.released = append(r.released, eventID)
	r.releaseDelay = delay
	return nil
}

func (r *recordingStatusOutbox) MarkDeadLetter(_ context.Context, eventID uuid.UUID, _, _ string) error {
	r.deadLettered = append(r.deadLettered, eventID)
	return nil
}

type recordingStatusProjector struct {
	sessions []uuid.UUID
	err      error
}

func (p *recordingStatusProjector) Project(_ context.Context, sessionID uuid.UUID) error {
	p.sessions = append(p.sessions, sessionID)
	return p.err
}

func Test_StatusProjectionRelay_marks_event_published_after_cache_projection(t *testing.T) {
	// Given
	eventID, sessionID := uuid.New(), uuid.New()
	repository := &recordingStatusOutbox{events: []outbox.ClaimedEvent{{
		Event: outbox.Event{ID: eventID, Type: StatusProjectionEventType, AggregateType: "Session", AggregateID: sessionID}, Attempts: 1,
	}}}
	projector := &recordingStatusProjector{}
	relay, err := NewStatusProjectionRelay(repository, projector, outbox.Config{
		WorkerID: "status-worker", BatchSize: 10, PollInterval: time.Second, Lease: time.Minute,
		MaxAttempts: 5, BaseBackoff: time.Second, MaxBackoff: 30 * time.Second,
	})
	require.NoError(t, err)

	// When
	result, err := relay.RunOnce(context.Background())

	// Then
	require.NoError(t, err)
	require.Equal(t, StatusProjectionEventType, repository.claimedType)
	require.Equal(t, []uuid.UUID{sessionID}, projector.sessions)
	require.Equal(t, []uuid.UUID{eventID}, repository.marked)
	require.Equal(t, 1, result.Published)
}

func Test_StatusProjectionRelay_retries_failed_projection_with_bounded_backoff(t *testing.T) {
	// Given
	eventID := uuid.New()
	repository := &recordingStatusOutbox{events: []outbox.ClaimedEvent{{
		Event: outbox.Event{ID: eventID, Type: StatusProjectionEventType, AggregateType: "Session", AggregateID: uuid.New()}, Attempts: 6,
	}}}
	relay, err := NewStatusProjectionRelay(repository, &recordingStatusProjector{err: errors.New("redis unavailable")}, outbox.Config{
		WorkerID: "status-worker", BatchSize: 10, PollInterval: time.Second, Lease: time.Minute,
		MaxAttempts: 10, BaseBackoff: time.Second, MaxBackoff: 30 * time.Second,
	})
	require.NoError(t, err)

	// When
	result, err := relay.RunOnce(context.Background())

	// Then
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{eventID}, repository.released)
	require.Equal(t, 30*time.Second, repository.releaseDelay)
	require.Equal(t, 1, result.Retried)
}
