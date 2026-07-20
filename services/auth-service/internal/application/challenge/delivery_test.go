package challenge

import (
	"context"
	"testing"
	"time"

	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/google/uuid"
)

func TestDeliveryServiceAppliesRetryAndTerminalDecisions(t *testing.T) {
	now := time.Date(2026, time.July, 19, 9, 0, 0, 0, time.UTC)
	expired := claimed(now, 1)
	expired.ExpiresAt = now
	tests := []struct {
		name          string
		delivery      ClaimedDelivery
		secret        DeliverySecret
		openErr       error
		result        SendResult
		wantDelivered bool
		wantRetry     bool
		wantFailed    bool
		wantCode      string
	}{
		{
			name: "delivered", delivery: claimed(now, 1),
			secret: DeliverySecret{Code: "******", Destination: "masked-destination"},
			result: SendResult{RequestID: "provider-request"}, wantDelivered: true,
		},
		{
			name: "retryable", delivery: claimed(now, 2),
			secret: DeliverySecret{Code: "******", Destination: "masked-destination"},
			result: SendResult{Retry: true, Code: "provider_unavailable"}, wantRetry: true, wantCode: "provider_unavailable",
		},
		{
			name: "attempts exhausted", delivery: claimed(now, 3),
			secret: DeliverySecret{Code: "******", Destination: "masked-destination"},
			result: SendResult{Retry: true, Code: "provider_unavailable"}, wantFailed: true, wantCode: "provider_unavailable",
		},
		{
			name: "expired", delivery: expired,
			secret: DeliverySecret{Code: "******", Destination: "masked-destination"},
			result: SendResult{Retry: true, Code: "provider_unavailable"}, wantFailed: true, wantCode: "provider_unavailable",
		},
		{
			name: "invalid payload", delivery: claimed(now, 1),
			secret:     DeliverySecret{Code: "short", Destination: "masked-destination"},
			wantFailed: true, wantCode: "payload_invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &deliveryRepositoryFake{claimed: []ClaimedDelivery{test.delivery}}
			service := newTestDeliveryService(t, repository,
				openerFunc(func([]byte) (DeliverySecret, error) { return test.secret, test.openErr }),
				senderFunc(func(context.Context, DeliveryRequest) SendResult { return test.result }),
			)
			service.now = func() time.Time { return now }
			if err := service.runOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if repository.delivered != test.wantDelivered || repository.retried != test.wantRetry || repository.failed != test.wantFailed || repository.code != test.wantCode {
				t.Fatalf("state delivered=%t retry=%t failed=%t code=%q", repository.delivered, repository.retried, repository.failed, repository.code)
			}
			if test.wantRetry && repository.delay != 20*time.Millisecond {
				t.Fatalf("retry delay=%s, want 20ms", repository.delay)
			}
		})
	}
}

func TestDeliveryBackoffIsBounded(t *testing.T) {
	config := DeliveryConfig{BaseBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond}
	if got := deliveryBackoff(config, 20); got != config.MaxBackoff {
		t.Fatalf("deliveryBackoff()=%s, want %s", got, config.MaxBackoff)
	}
}

func claimed(now time.Time, attempts int) ClaimedDelivery {
	return ClaimedDelivery{
		ID: uuid.New(), Channel: domainchallenge.ChannelEmailCode,
		Ciphertext: []byte("encrypted"), ExpiresAt: now.Add(time.Minute), Attempts: attempts,
	}
}

func newTestDeliveryService(t *testing.T, repository DeliveryRepository, opener PayloadOpener, sender Sender) *DeliveryService {
	t.Helper()
	service, err := NewDeliveryService(repository, opener, sender, DeliveryConfig{
		WorkerID: "test-worker", RequestTimeout: time.Second, PollInterval: time.Second,
		Lease: 2 * time.Second, BatchSize: 1, MaxAttempts: 3,
		BaseBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type openerFunc func([]byte) (DeliverySecret, error)

func (f openerFunc) OpenDelivery(ciphertext []byte) (DeliverySecret, error) {
	return f(ciphertext)
}

type senderFunc func(context.Context, DeliveryRequest) SendResult

func (f senderFunc) Send(ctx context.Context, request DeliveryRequest) SendResult {
	return f(ctx, request)
}

type deliveryRepositoryFake struct {
	claimed                    []ClaimedDelivery
	delivered, retried, failed bool
	delay                      time.Duration
	code                       string
}

func (r *deliveryRepositoryFake) Claim(context.Context, string, int, time.Duration) ([]ClaimedDelivery, error) {
	return r.claimed, nil
}

func (r *deliveryRepositoryFake) MarkDelivered(context.Context, uuid.UUID, string, string) error {
	r.delivered = true
	return nil
}

func (r *deliveryRepositoryFake) Retry(_ context.Context, _ uuid.UUID, _ string, delay time.Duration, code string) error {
	r.retried, r.delay, r.code = true, delay, code
	return nil
}

func (r *deliveryRepositoryFake) Fail(_ context.Context, _ uuid.UUID, _ string, code string) error {
	r.failed, r.code = true, code
	return nil
}
