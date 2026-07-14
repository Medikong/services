package eventing

import (
	"context"
	"strings"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	domaineventing "github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/samber/oops"
)

// ExternalPublisher routes only the event families assigned to the explicit
// external ports. The ports must preserve event ID idempotency on retry.
type ExternalPublisher struct {
	settlement   ports.SettlementEventPort
	notification ports.NotificationEventPort
}

func NewExternalPublisher(settlement ports.SettlementEventPort, notification ports.NotificationEventPort) (*ExternalPublisher, error) {
	if settlement == nil || notification == nil {
		return nil, oops.In("coupon_external_publisher").Code("coupon.external_publisher_config_invalid").New("settlement and notification event ports are required")
	}
	return &ExternalPublisher{settlement: settlement, notification: notification}, nil
}

func (p *ExternalPublisher) Publish(ctx context.Context, envelope policy.Envelope) error {
	event := reliability.Event{
		ID: envelope.EventID, DocumentID: envelope.EventDocumentID, Type: envelope.EventType,
		AggregateType: envelope.AggregateType, AggregateID: envelope.AggregateID,
		AggregateVersion: envelope.AggregateVersion, CorrelationID: envelope.CorrelationID,
		CausationID: envelope.CausationID, TraceID: envelope.TraceID, PayloadSchemaVersion: envelope.PayloadSchemaVersion,
		Data: envelope.Data, OccurredAt: envelope.OccurredAt,
	}
	if envelope.EventDocumentID == "EVT.A.19-28" {
		if err := p.settlement.DeliverCostAttribution(ctx, event); err != nil {
			return err
		}
	}
	if notificationEvent(envelope.EventDocumentID) {
		if err := p.notification.DeliverCouponEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func notificationEvent(documentID string) bool {
	if !strings.HasPrefix(documentID, "EVT.A.19-") {
		return false
	}
	switch documentID {
	case "EVT.A.19-07", "EVT.A.19-08", "EVT.A.19-09", "EVT.A.19-10", "EVT.A.19-11",
		"EVT.A.19-12", "EVT.A.19-13", "EVT.A.19-14", "EVT.A.19-15",
		"EVT.A.19-19", "EVT.A.19-20", "EVT.A.19-21", "EVT.A.19-22", "EVT.A.19-23", "EVT.A.19-24",
		"EVT.A.19-29", "EVT.A.19-31", "EVT.A.19-32", "EVT.A.19-33", "EVT.A.19-34", "EVT.A.19-35",
		"EVT.A.19-36", "EVT.A.19-37":
		return true
	default:
		return false
	}
}

var _ domaineventing.Publisher = (*ExternalPublisher)(nil)
