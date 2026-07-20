// Package outbox owns the at-least-once side of auth domain-event
// delivery. Concrete Context destinations are injected through Publisher;
// no broker URL or credential is guessed in this service.
package outbox

import (
	"context"
	"errors"
	"sync"
	"time"

	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
)

type Publisher interface {
	Publish(context.Context, domainoutbox.Event) error
}

type ClaimedEvent struct {
	domainoutbox.Event
	Attempts int
}

// Repository is the relay worker's persistence view. Transactional append
// ports belong to each application use case instead of the domain model.
type Repository interface {
	ClaimPublishBatch(context.Context, string, int, time.Duration) ([]ClaimedEvent, error)
	MarkPublished(context.Context, uuid.UUID, string) error
	ReleaseForRetry(context.Context, uuid.UUID, string, time.Duration, string) error
	MarkDeadLetter(context.Context, uuid.UUID, string, string) error
}

type Config struct {
	WorkerID     string
	BatchSize    int
	PollInterval time.Duration
	Lease        time.Duration
	MaxAttempts  int
	BaseBackoff  time.Duration
	MaxBackoff   time.Duration
}

type Result struct {
	Claimed, Published, Retried, DeadLettered int
}

type Service struct {
	repository Repository
	publisher  Publisher
	config     Config
}

func New(repository Repository, publisher Publisher, config Config) (*Service, error) {
	if repository == nil || publisher == nil || config.WorkerID == "" || config.BatchSize < 1 || config.PollInterval <= 0 || config.Lease <= 0 || config.MaxAttempts < 1 || config.BaseBackoff <= 0 || config.MaxBackoff < config.BaseBackoff {
		return nil, errors.New("invalid auth outbox relay configuration")
	}
	return &Service{repository: repository, publisher: publisher, config: config}, nil
}

// Run performs durable retries until the worker context is cancelled.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := s.RunOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce claims a lease before each external call. A successful publisher
// acknowledgement is the only path that marks an event as published.
func (s *Service) RunOnce(ctx context.Context) (Result, error) {
	events, err := s.repository.ClaimPublishBatch(ctx, s.config.WorkerID, s.config.BatchSize, s.config.Lease)
	if err != nil {
		return Result{}, err
	}
	result := Result{Claimed: len(events)}
	for _, claimed := range events {
		err := s.publisher.Publish(ctx, claimed.Event)
		if err == nil {
			if err := s.repository.MarkPublished(ctx, claimed.ID, s.config.WorkerID); err != nil {
				return result, err
			}
			result.Published++
			continue
		}
		if claimed.Attempts >= s.config.MaxAttempts {
			if err := s.repository.MarkDeadLetter(ctx, claimed.ID, s.config.WorkerID, "publisher_failed"); err != nil {
				return result, err
			}
			result.DeadLettered++
			continue
		}
		if err := s.repository.ReleaseForRetry(ctx, claimed.ID, s.config.WorkerID, backoff(s.config, claimed.Attempts), "publisher_failed"); err != nil {
			return result, err
		}
		result.Retried++
	}
	return result, nil
}

func backoff(config Config, attempts int) time.Duration {
	delay := config.BaseBackoff
	for step := 1; step < attempts && delay < config.MaxBackoff; step++ {
		delay *= 2
	}
	if delay > config.MaxBackoff {
		return config.MaxBackoff
	}
	return delay
}

// RecordingPublisher is a test adapter. It models a Context acknowledgement
// without a real address or credential and copies events before returning.
type RecordingPublisher struct {
	mu     sync.Mutex
	Events []domainoutbox.Event
	Err    error
}

func (p *RecordingPublisher) Publish(_ context.Context, event domainoutbox.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Err != nil {
		return p.Err
	}
	event.Payload = append([]byte(nil), event.Payload...)
	p.Events = append(p.Events, event)
	return nil
}
