package sessionprojection

import (
	"context"
	"errors"
	"strings"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
)

var ErrInvalidConfig = errors.New("invalid session status projection relay configuration")

type Service struct {
	repository Repository
	sink       Sink
	config     Config
	now        func() time.Time
}

func New(repository Repository, sink Sink, config Config) (*Service, error) {
	if repository == nil || sink == nil || strings.TrimSpace(config.WorkerID) == "" ||
		config.BatchSize < 1 || config.PollInterval <= 0 || config.Lease <= 0 ||
		config.ApplyTimeout <= 0 || config.Lease < 2*config.ApplyTimeout ||
		config.BaseBackoff <= 0 || config.MaxBackoff < config.BaseBackoff {
		return nil, ErrInvalidConfig
	}
	return &Service{
		repository: repository,
		sink:       sink,
		config:     config,
		now:        func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	for {
		result, err := s.RunOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if result.Claimed >= s.config.BatchSize {
			continue
		}

		timer := time.NewTimer(s.config.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

// RunOnce marks a job delivered only after the Redis sink acknowledges the
// versioned state. Transient sink failures are retried without an attempt cap.
func (s *Service) RunOnce(ctx context.Context) (Result, error) {
	changes, err := s.repository.Claim(ctx, s.config.WorkerID, s.config.BatchSize, s.config.Lease)
	if err != nil {
		return Result{}, err
	}
	result := Result{Claimed: len(changes)}
	for _, claimed := range changes {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}

		if !claimed.ValidUntil.After(s.now()) {
			if err := s.repository.MarkDelivered(ctx, claimed.JobID, s.config.WorkerID); err != nil {
				return result, err
			}
			result.Expired++
			s.record("expired")
			continue
		}

		errorCode := "projection_apply_failed"
		applyErr := claimed.StatusChange.Validate()
		if errors.Is(applyErr, domainsession.ErrInvalidStatusChange) {
			errorCode = "projection_invalid"
		} else if applyErr == nil {
			applyCtx, cancel := context.WithTimeout(ctx, s.config.ApplyTimeout)
			applyErr = s.sink.Apply(applyCtx, claimed.StatusChange)
			cancel()
		}
		if applyErr == nil {
			if err := s.repository.MarkDelivered(ctx, claimed.JobID, s.config.WorkerID); err != nil {
				return result, err
			}
			result.Applied++
			s.record("applied")
			continue
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err := s.repository.ReleaseForRetry(
			ctx,
			claimed.JobID,
			s.config.WorkerID,
			retryBackoff(claimed.Attempts, s.config.BaseBackoff, s.config.MaxBackoff),
			errorCode,
		); err != nil {
			return result, err
		}
		result.Retried++
		s.record("retry")
	}
	return result, nil
}

func (s *Service) record(result string) {
	if s.config.OnAttempt != nil {
		s.config.OnAttempt(result)
	}
}

func retryBackoff(attempt int, base, maximum time.Duration) time.Duration {
	if attempt <= 1 {
		return base
	}
	delay := base
	for step := 1; step < attempt; step++ {
		if delay >= maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}
