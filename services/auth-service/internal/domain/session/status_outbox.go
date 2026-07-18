package session

import (
	"context"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

const StatusProjectionEventType = outbox.InternalSessionStatusProjectionEventType

type StatusOutboxRepository interface {
	ClaimPublishBatchByType(context.Context, string, string, int, time.Duration) ([]outbox.ClaimedEvent, error)
	MarkPublished(context.Context, uuid.UUID, string) error
	ReleaseForRetry(context.Context, uuid.UUID, string, time.Duration, string) error
	MarkDeadLetter(context.Context, uuid.UUID, string, string) error
}

type StatusProjector interface {
	Project(context.Context, uuid.UUID) error
}

type StatusProjectionRelay struct {
	repository StatusOutboxRepository
	projector  StatusProjector
	config     outbox.Config
}

func NewStatusProjectionRelay(repository StatusOutboxRepository, projector StatusProjector, config outbox.Config) (*StatusProjectionRelay, error) {
	if repository == nil || projector == nil || config.WorkerID == "" || config.BatchSize < 1 || config.PollInterval <= 0 || config.Lease <= 0 || config.MaxAttempts < 1 || config.BaseBackoff < time.Second || config.MaxBackoff < config.BaseBackoff || config.MaxBackoff > 30*time.Second {
		return nil, oops.In("session_status_outbox").Code("relay.invalid").New("invalid session status relay configuration")
	}
	return &StatusProjectionRelay{repository: repository, projector: projector, config: config}, nil
}

func (r *StatusProjectionRelay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.config.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := r.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *StatusProjectionRelay) RunOnce(ctx context.Context) (outbox.Result, error) {
	events, err := r.repository.ClaimPublishBatchByType(
		ctx, r.config.WorkerID, StatusProjectionEventType, r.config.BatchSize, r.config.Lease,
	)
	if err != nil {
		return outbox.Result{}, oops.In("session_status_outbox").Code("relay.claim_failed").Wrap(err)
	}
	result := outbox.Result{Claimed: len(events)}
	for _, claimed := range events {
		if claimed.Type != StatusProjectionEventType || claimed.AggregateType != "Session" || claimed.AggregateID == uuid.Nil {
			if err := r.repository.MarkDeadLetter(ctx, claimed.ID, r.config.WorkerID, "invalid_status_projection_event"); err != nil {
				return result, err
			}
			result.DeadLettered++
			continue
		}
		if err := r.projector.Project(ctx, claimed.AggregateID); err == nil {
			if err := r.repository.MarkPublished(ctx, claimed.ID, r.config.WorkerID); err != nil {
				return result, err
			}
			result.Published++
			continue
		}
		if claimed.Attempts >= r.config.MaxAttempts {
			if err := r.repository.MarkDeadLetter(ctx, claimed.ID, r.config.WorkerID, "status_projection_failed"); err != nil {
				return result, err
			}
			result.DeadLettered++
			continue
		}
		if err := r.repository.ReleaseForRetry(ctx, claimed.ID, r.config.WorkerID, statusBackoff(r.config, claimed.Attempts), "status_projection_failed"); err != nil {
			return result, err
		}
		result.Retried++
	}
	return result, nil
}

func statusBackoff(config outbox.Config, attempts int) time.Duration {
	delay := config.BaseBackoff
	for step := 1; step < attempts && delay < config.MaxBackoff; step++ {
		delay *= 2
	}
	if delay > config.MaxBackoff {
		return config.MaxBackoff
	}
	return delay
}
