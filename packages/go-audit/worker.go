package audit

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type Publish func(context.Context, Event) error

type Attempt struct {
	EventID  string
	Attempts int
	Result   string
	Err      error
}

type WorkerConfig struct {
	Pool           *pgxpool.Pool
	WorkerID       string
	BatchSize      int
	PollInterval   time.Duration
	Lease          time.Duration
	PublishTimeout time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	Publish        Publish
	OnAttempt      func(context.Context, Attempt)
}

type Worker struct {
	config WorkerConfig
}

func NewWorker(config WorkerConfig) (*Worker, error) {
	if err := validateWorkerConfig(&config); err != nil {
		return nil, err
	}
	return &Worker{config: config}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		processed, err := w.RunOnce(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) && ctx.Err() != nil {
				return nil
			}
			return err
		}
		if processed == w.config.BatchSize {
			continue
		}
		timer := time.NewTimer(w.config.PollInterval)
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

func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	return runBatch(ctx, w.config.BatchSize, w.runNext)
}

func runBatch(ctx context.Context, batchSize int, runNext func(context.Context) (bool, error)) (int, error) {
	processed := 0
	for processed < batchSize {
		didProcess, err := runNext(ctx)
		if err != nil {
			return processed, err
		}
		if !didProcess {
			break
		}
		processed++
	}
	return processed, nil
}

func (w *Worker) runNext(ctx context.Context) (bool, error) {
	claimCtx, cancel := context.WithTimeout(ctx, w.config.PublishTimeout)
	records, err := Claim(claimCtx, w.config.Pool, w.config.WorkerID, 1, w.config.Lease)
	cancel()
	if err != nil {
		return false, err
	}
	if len(records) == 0 {
		return false, nil
	}
	record := records[0]
	publishCtx, cancel := context.WithTimeout(ctx, w.config.PublishTimeout)
	err = w.config.Publish(publishCtx, record.Event)
	cancel()
	if err == nil {
		markCtx, cancel := context.WithTimeout(ctx, w.config.PublishTimeout)
		markErr := MarkDelivered(markCtx, w.config.Pool, w.config.WorkerID, record.Event.ID)
		cancel()
		if markErr != nil {
			return false, markErr
		}
		w.record(ctx, record, "delivered", nil)
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	markCtx, cancel := context.WithTimeout(ctx, w.config.PublishTimeout)
	markErr := MarkFailed(
		markCtx,
		w.config.Pool,
		w.config.WorkerID,
		record.Event.ID,
		record.Attempts,
		w.config.MaxAttempts,
		backoff(record.Attempts, w.config.BaseBackoff, w.config.MaxBackoff),
		err,
	)
	cancel()
	if markErr != nil {
		return false, markErr
	}
	result := "retry"
	if record.Attempts >= w.config.MaxAttempts {
		result = "dead"
	}
	w.record(ctx, record, result, err)
	return true, nil
}

func validateWorkerConfig(config *WorkerConfig) error {
	errBuilder := oops.In("audit_worker").Code("audit.invalid_worker_config")
	switch {
	case config.Pool == nil:
		return errBuilder.New("postgres pool is required")
	case config.WorkerID == "":
		return errBuilder.New("worker id is required")
	case config.Publish == nil:
		return errBuilder.New("audit publish function is required")
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 50
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.Lease <= 0 {
		config.Lease = 30 * time.Second
	}
	if config.PublishTimeout <= 0 {
		config.PublishTimeout = 10 * time.Second
	}
	if config.Lease < 2*config.PublishTimeout {
		return errBuilder.New("lease must be at least twice the publish timeout")
	}
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 10
	}
	if config.BaseBackoff <= 0 {
		config.BaseBackoff = time.Second
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = time.Minute
	}
	if config.MaxBackoff < config.BaseBackoff {
		return errBuilder.New("max backoff must be greater than or equal to base backoff")
	}
	return nil
}

func backoff(attempt int, base time.Duration, maximum time.Duration) time.Duration {
	if attempt <= 1 {
		return base
	}
	value := base
	for range attempt - 1 {
		if value >= maximum/2 {
			return maximum
		}
		value *= 2
	}
	if value > maximum {
		return maximum
	}
	return value
}

func (w *Worker) record(ctx context.Context, record Record, result string, err error) {
	if w.config.OnAttempt != nil {
		w.config.OnAttempt(ctx, Attempt{
			EventID:  record.Event.ID.String(),
			Attempts: record.Attempts,
			Result:   result,
			Err:      err,
		})
	}
}
