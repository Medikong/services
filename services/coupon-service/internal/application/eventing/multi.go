package eventing

import (
	"context"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	domaineventing "github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/samber/oops"
)

// MultiPublisher delivers the same immutable event to independent idempotent
// consumers. A later retry safely replays consumers that already completed.
type MultiPublisher struct {
	publishers []domaineventing.Publisher
}

func NewMultiPublisher(publishers ...domaineventing.Publisher) (*MultiPublisher, error) {
	if len(publishers) == 0 {
		return nil, oops.In("coupon_event_publisher").Code("coupon.publisher_required").New("at least one event publisher is required")
	}
	for _, publisher := range publishers {
		if publisher == nil {
			return nil, oops.In("coupon_event_publisher").Code("coupon.publisher_required").New("event publisher must not be nil")
		}
	}
	return &MultiPublisher{publishers: append([]domaineventing.Publisher(nil), publishers...)}, nil
}

func (m *MultiPublisher) Publish(ctx context.Context, envelope policy.Envelope) error {
	for _, publisher := range m.publishers {
		if err := publisher.Publish(ctx, envelope); err != nil {
			return err
		}
	}
	return nil
}

var _ domaineventing.Publisher = (*MultiPublisher)(nil)
