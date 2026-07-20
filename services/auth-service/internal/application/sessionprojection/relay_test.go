package sessionprojection

import (
	"context"
	"errors"
	"testing"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func TestRunOnceAcknowledgesAppliedAndExpiredChanges(t *testing.T) {
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	applied := validClaimedChange(now.Add(time.Hour), 1)
	expired := validClaimedChange(now.Add(-time.Second), 1)
	repository := &relayRepository{claimed: []ClaimedChange{applied, expired}}
	sink := &relaySink{}
	var attempts []string
	service := newRelayService(t, repository, sink, func(result string) { attempts = append(attempts, result) })
	service.now = func() time.Time { return now }

	result, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result != (Result{Claimed: 2, Applied: 1, Expired: 1}) {
		t.Fatalf("RunOnce() result = %#v", result)
	}
	if len(sink.applied) != 1 || sink.applied[0].SessionID != applied.SessionID {
		t.Fatal("sink did not receive exactly the expected status change")
	}
	if len(repository.delivered) != 2 {
		t.Fatalf("delivered job count = %d, want 2", len(repository.delivered))
	}
	if len(attempts) != 2 || attempts[0] != "applied" || attempts[1] != "expired" {
		t.Fatalf("attempt outcomes = %v", attempts)
	}
}

func TestRunOnceRetriesWithoutAcknowledgingSinkFailure(t *testing.T) {
	now := time.Now().UTC()
	claimed := validClaimedChange(now.Add(time.Hour), 3)
	repository := &relayRepository{claimed: []ClaimedChange{claimed}}
	sink := &relaySink{err: errors.New("redis unavailable")}
	var attempts []string
	service := newRelayService(t, repository, sink, func(result string) { attempts = append(attempts, result) })
	service.now = func() time.Time { return now }

	result, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result != (Result{Claimed: 1, Retried: 1}) {
		t.Fatalf("RunOnce() result = %#v", result)
	}
	if len(repository.delivered) != 0 || len(repository.retried) != 1 {
		t.Fatalf("delivered job count = %d, retry count = %d, want 0 and 1", len(repository.delivered), len(repository.retried))
	}
	if repository.retried[0].delay != 4*time.Second || repository.retried[0].code != "projection_apply_failed" {
		t.Fatal("retry delay or sanitized error code differs")
	}
	if len(attempts) != 1 || attempts[0] != "retry" {
		t.Fatalf("attempt outcomes = %v", attempts)
	}
}

func TestRunOnceRetriesInvalidPersistedChange(t *testing.T) {
	now := time.Now().UTC()
	claimed := validClaimedChange(now.Add(time.Hour), 1)
	claimed.Status = "active"
	repository := &relayRepository{claimed: []ClaimedChange{claimed}}
	sink := &relaySink{}
	service := newRelayService(t, repository, sink, nil)
	service.now = func() time.Time { return now }

	result, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Retried != 1 || repository.retried[0].code != "projection_invalid" || len(sink.applied) != 0 {
		t.Fatal("invalid persisted change was not retried without applying")
	}
}

func TestNewRejectsUnsafeLease(t *testing.T) {
	_, err := New(&relayRepository{}, &relaySink{}, Config{
		WorkerID: "worker", BatchSize: 1, PollInterval: time.Second,
		Lease: time.Second, ApplyTimeout: time.Second,
		BaseBackoff: time.Second, MaxBackoff: time.Second,
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New() error = %v", err)
	}
}

func newRelayService(t *testing.T, repository Repository, sink Sink, onAttempt func(string)) *Service {
	t.Helper()
	service, err := New(repository, sink, Config{
		WorkerID: "worker", BatchSize: 10, PollInterval: time.Second,
		Lease: 10 * time.Second, ApplyTimeout: time.Second,
		BaseBackoff: time.Second, MaxBackoff: 30 * time.Second,
		OnAttempt: onAttempt,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

func validClaimedChange(validUntil time.Time, attempts int) ClaimedChange {
	return ClaimedChange{
		JobID: uuid.New(),
		StatusChange: domainsession.StatusChange{
			SessionID: uuid.New(), UserID: uuid.New(), Status: domainsession.StatusRevoked,
			Version: 1, ValidUntil: validUntil, OccurredAt: validUntil.Add(-time.Minute),
		},
		Attempts: attempts,
	}
}

type relayRepository struct {
	claimed   []ClaimedChange
	delivered []uuid.UUID
	retried   []relayRetry
	markErr   error
}

type relayRetry struct {
	jobID uuid.UUID
	delay time.Duration
	code  string
}

func (r *relayRepository) Claim(context.Context, string, int, time.Duration) ([]ClaimedChange, error) {
	return append([]ClaimedChange(nil), r.claimed...), nil
}

func (r *relayRepository) MarkDelivered(_ context.Context, jobID uuid.UUID, _ string) error {
	if r.markErr != nil {
		return r.markErr
	}
	r.delivered = append(r.delivered, jobID)
	return nil
}

func (r *relayRepository) ReleaseForRetry(_ context.Context, jobID uuid.UUID, _ string, delay time.Duration, code string) error {
	r.retried = append(r.retried, relayRetry{jobID: jobID, delay: delay, code: code})
	return nil
}

func (r *relayRepository) DeleteDeliveredBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

type relaySink struct {
	applied []domainsession.StatusChange
	err     error
}

func (s *relaySink) Apply(_ context.Context, change domainsession.StatusChange) error {
	s.applied = append(s.applied, change)
	return s.err
}
