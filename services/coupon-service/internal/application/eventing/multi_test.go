package eventing

import (
	"context"
	"testing"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
)

type recordingPublisher struct {
	calls int
	err   error
}

func (p *recordingPublisher) Publish(context.Context, policy.Envelope) error {
	p.calls++
	return p.err
}

func TestMultiPublisherStopsAtFirstFailure(t *testing.T) {
	first := &recordingPublisher{err: context.DeadlineExceeded}
	second := &recordingPublisher{}
	publisher, err := NewMultiPublisher(first, second)
	if err != nil {
		t.Fatalf("NewMultiPublisher() error = %v", err)
	}
	if err := publisher.Publish(context.Background(), policy.Envelope{}); err == nil {
		t.Fatal("Publish() error = nil")
	}
	if first.calls != 1 || second.calls != 0 {
		t.Fatalf("calls = (%d, %d), want (1, 0)", first.calls, second.calls)
	}
}
