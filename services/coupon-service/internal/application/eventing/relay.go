package eventing

import (
	"context"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	domaineventing "github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/samber/oops"
)

type Policy struct {
	BatchSize      int
	Lease          time.Duration
	AttemptTimeout time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

type Relay struct {
	workerID  string
	repo      domaineventing.OutboxRepository
	publisher domaineventing.Publisher
	policy    Policy
	now       func() time.Time
}

func NewRelay(workerID string, repo domaineventing.OutboxRepository, publisher domaineventing.Publisher, config Policy) (*Relay, error) {
	if workerID == "" || repo == nil || publisher == nil || config.BatchSize < 1 || config.Lease <= 0 ||
		config.AttemptTimeout <= 0 || config.MaxAttempts < 1 || config.BaseBackoff <= 0 || config.MaxBackoff < config.BaseBackoff {
		return nil, oops.In("coupon_outbox_relay").Code("coupon.outbox_relay_config_invalid").New("outbox relay configuration is invalid")
	}
	return &Relay{workerID: workerID, repo: repo, publisher: publisher, policy: config, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	items, err := r.repo.Claim(ctx, r.workerID, r.policy.BatchSize, r.policy.Lease)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		publishCtx, cancel := context.WithTimeout(ctx, r.policy.AttemptTimeout)
		err := r.publisher.Publish(publishCtx, item.Envelope)
		cancel()
		if err == nil {
			if err := r.repo.MarkPublished(ctx, item.Envelope.EventID, r.workerID); err != nil {
				return 0, err
			}
			continue
		}
		terminal := item.AttemptCount >= r.policy.MaxAttempts
		next := r.now().Add(RetryDelay(item.AttemptCount, r.policy.BaseBackoff, r.policy.MaxBackoff))
		if recordErr := r.repo.MarkFailed(ctx, item.Envelope.EventID, r.workerID, next, "COUPON_EVENT_DELIVERY_FAILED", terminal); recordErr != nil {
			return 0, oops.Join(err, recordErr)
		}
	}
	return len(items), nil
}

func RetryDelay(attempt int, base, maximum time.Duration) time.Duration {
	result := base
	for index := 1; index < attempt && result < maximum; index++ {
		if result > maximum/2 {
			return maximum
		}
		result *= 2
	}
	if result > maximum {
		return maximum
	}
	return result
}

type PolicyPublisher struct {
	processor *policy.Processor
	consumer  string
}

func NewPolicyPublisher(processor *policy.Processor, consumer string) *PolicyPublisher {
	return &PolicyPublisher{processor: processor, consumer: consumer}
}

func (p *PolicyPublisher) Publish(ctx context.Context, envelope policy.Envelope) error {
	if p == nil || p.processor == nil {
		return oops.In("coupon_policy_publisher").Code("coupon.policy_processor_required").New("policy processor is required")
	}
	return p.processor.Handle(ctx, p.consumer, envelope)
}

var _ domaineventing.Publisher = (*PolicyPublisher)(nil)
