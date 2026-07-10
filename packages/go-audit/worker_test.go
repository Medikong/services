package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRunBatchProcessesUpToBatchSizeOneAtATime(t *testing.T) {
	calls := 0
	processed, err := runBatch(context.Background(), 3, func(context.Context) (bool, error) {
		calls++
		return true, nil
	})
	if err != nil {
		t.Fatalf("runBatch() error = %v", err)
	}
	if processed != 3 || calls != 3 {
		t.Fatalf("runBatch() processed=%d calls=%d, want 3 and 3", processed, calls)
	}
}

func TestNewWorkerRejectsLeaseShorterThanPublishBudget(t *testing.T) {
	_, err := NewWorker(WorkerConfig{
		Pool:           &pgxpool.Pool{},
		WorkerID:       "worker-1",
		Lease:          10 * time.Second,
		PublishTimeout: 10 * time.Second,
		Publish:        func(context.Context, Event) error { return nil },
	})
	if err == nil {
		t.Fatal("NewWorker() error = nil, want invalid lease")
	}
}

func TestRunBatchStopsWhenNoRecordIsClaimed(t *testing.T) {
	calls := 0
	processed, err := runBatch(context.Background(), 5, func(context.Context) (bool, error) {
		calls++
		return calls == 1, nil
	})
	if err != nil {
		t.Fatalf("runBatch() error = %v", err)
	}
	if processed != 1 || calls != 2 {
		t.Fatalf("runBatch() processed=%d calls=%d, want 1 and 2", processed, calls)
	}
}

func TestRunBatchReturnsCompletedCountWithError(t *testing.T) {
	wantErr := errors.New("claim failed")
	calls := 0
	processed, err := runBatch(context.Background(), 5, func(context.Context) (bool, error) {
		calls++
		if calls == 2 {
			return false, wantErr
		}
		return true, nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runBatch() error = %v, want %v", err, wantErr)
	}
	if processed != 1 {
		t.Fatalf("runBatch() processed=%d, want 1", processed)
	}
}
