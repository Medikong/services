package commandworker

import (
	"context"
	"errors"
	"testing"
	"time"

	domaineventing "github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/google/uuid"
)

func TestWorkerCompletesCommandWhenFailureSinkSchedulesFollowUp(t *testing.T) {
	request := domaineventing.CommandRequest{
		ID: uuid.New(), CommandDocumentID: "CMD.A.19-07", AggregateID: "ireq_12345678",
		BusinessKey: "issue:123", CorrelationID: "correlation-1", AttemptCount: 1,
	}
	queue := &commandQueueFake{items: []domaineventing.CommandRequest{request}}
	dispatchErr := errors.New("grant failed")
	sink := &failureSinkFake{handled: true, resultRef: "command:failure-recorded"}
	worker, err := New("worker-1", queue, dispatcherFunc(func(context.Context, domaineventing.CommandRequest) (string, error) {
		return "", dispatchErr
	}), testPolicy(), sink)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	processed, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if processed != 1 || queue.completed != request.ID || queue.failed != uuid.Nil {
		t.Fatalf("RunOnce() processed=%d completed=%s failed=%s", processed, queue.completed, queue.failed)
	}
	if sink.cause != dispatchErr || sink.request.ID != request.ID {
		t.Fatalf("failure sink correlation = %#v, cause=%v", sink.request, sink.cause)
	}
}

func TestWorkerRetainsGenericRetryWhenFailureSinkDeclines(t *testing.T) {
	request := domaineventing.CommandRequest{
		ID: uuid.New(), CommandDocumentID: "CMD.A.19-24", AggregateID: "ucpn_12345678",
		BusinessKey: "expiry:123", CorrelationID: "correlation-2", AttemptCount: 1,
	}
	queue := &commandQueueFake{items: []domaineventing.CommandRequest{request}}
	worker, err := New("worker-1", queue, dispatcherFunc(func(context.Context, domaineventing.CommandRequest) (string, error) {
		return "", errors.New("temporary failure")
	}), testPolicy(), &failureSinkFake{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if queue.failed != request.ID || queue.completed != uuid.Nil {
		t.Fatalf("generic failure completed=%s failed=%s", queue.completed, queue.failed)
	}
}

type dispatcherFunc func(context.Context, domaineventing.CommandRequest) (string, error)

func (f dispatcherFunc) Dispatch(ctx context.Context, request domaineventing.CommandRequest) (string, error) {
	return f(ctx, request)
}

type failureSinkFake struct {
	handled   bool
	resultRef string
	request   domaineventing.CommandRequest
	cause     error
}

func (f *failureSinkFake) HandleCommandFailure(_ context.Context, request domaineventing.CommandRequest, cause error, _ time.Time, _ bool) (string, bool, error) {
	f.request = request
	f.cause = cause
	return f.resultRef, f.handled, nil
}

type commandQueueFake struct {
	items     []domaineventing.CommandRequest
	completed uuid.UUID
	failed    uuid.UUID
}

func (f *commandQueueFake) ClaimCommands(context.Context, string, int, time.Duration) ([]domaineventing.CommandRequest, error) {
	return f.items, nil
}

func (f *commandQueueFake) CompleteCommand(_ context.Context, id uuid.UUID, _ string, _ string) error {
	f.completed = id
	return nil
}

func (f *commandQueueFake) FailCommand(_ context.Context, id uuid.UUID, _ string, _ time.Time, _ string, _ bool) error {
	f.failed = id
	return nil
}

func testPolicy() Policy {
	return Policy{
		BatchSize: 1, Lease: 30 * time.Second, AttemptTimeout: time.Second,
		MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute,
	}
}
