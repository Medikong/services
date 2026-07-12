package eventing

import (
	"context"
	"testing"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
)

type externalRecorder struct {
	settlement   int
	notification int
}

func (r *externalRecorder) DeliverCostAttribution(context.Context, reliability.Event) error {
	r.settlement++
	return nil
}

func (r *externalRecorder) DeliverCouponEvent(context.Context, reliability.Event) error {
	r.notification++
	return nil
}

func TestExternalPublisherRoutesOwnedEventFamilies(t *testing.T) {
	recorder := &externalRecorder{}
	publisher, err := NewExternalPublisher(recorder, recorder)
	if err != nil {
		t.Fatalf("NewExternalPublisher() error = %v", err)
	}
	for _, id := range []string{"EVT.A.19-09", "EVT.A.19-28", "EVT.A.19-38"} {
		if err := publisher.Publish(context.Background(), policy.Envelope{EventDocumentID: id}); err != nil {
			t.Fatalf("Publish(%s) error = %v", id, err)
		}
	}
	if recorder.notification != 1 || recorder.settlement != 1 {
		t.Fatalf("deliveries = notification:%d settlement:%d, want 1 each", recorder.notification, recorder.settlement)
	}
}
