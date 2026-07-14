package external

import (
	"context"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/commanding"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	appredemption "github.com/Medikong/services/services/coupon-service/internal/application/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

// Unavailable is the fail-closed adapter used until a deployment supplies a
// concrete Context endpoint or event publisher. It never treats an opaque
// external reference as verified merely because it is syntactically valid.
type Unavailable struct{}

func (Unavailable) Run(ctx context.Context, _ commanding.OperationsCommandSubmitter) error {
	<-ctx.Done()
	return nil
}

func (Unavailable) AuthorizeWorkload(context.Context, ports.WorkloadAccess) error {
	return unavailable("workload_authorization")
}

func (Unavailable) Snapshot(context.Context, string, time.Time) (ports.UserEligibility, error) {
	return ports.UserEligibility{}, unavailable("user")
}

func (Unavailable) VerifyProduct(context.Context, shared.ExternalRef, shared.SnapshotRef) error {
	return unavailable("product")
}

func (Unavailable) VerifyDrop(context.Context, shared.ExternalRef, shared.SnapshotRef) error {
	return unavailable("drop")
}

func (Unavailable) VerifySellerOwnership(context.Context, shared.SnapshotRef) error {
	return unavailable("seller_catalog")
}

func (Unavailable) VerifyOrder(context.Context, shared.SnapshotRef) error {
	return unavailable("order")
}

func (Unavailable) VerifyPaymentResult(context.Context, shared.ExternalRef, *shared.SnapshotRef, ports.PaymentResultBinding) error {
	return unavailable("payment")
}

func (Unavailable) VerifyCase(context.Context, string, ports.CSCaseBinding) error {
	return unavailable("cs")
}

func (Unavailable) VerifyApproval(context.Context, string, string) error {
	return unavailable("operation_approval")
}

func (Unavailable) Page(context.Context, string, time.Time, string, int) (ports.AudiencePage, error) {
	return ports.AudiencePage{}, unavailable("bulk_audience")
}

func (Unavailable) DeliverCostAttribution(context.Context, reliability.Event) error {
	return unavailable("settlement_event")
}

func (Unavailable) DeliverCouponEvent(context.Context, reliability.Event) error {
	return unavailable("notification_event")
}

func (Unavailable) RecordReference(context.Context, string, string, time.Time) error {
	return unavailable("observability")
}

func (Unavailable) Load(context.Context, string) ([]byte, error) {
	return nil, unavailable("replay_payload")
}

func unavailable(name string) error {
	return oops.
		In("coupon_external_adapter").
		Code("coupon.external_dependency_unavailable").
		With("dependency", name).
		New("coupon external dependency adapter is not configured")
}

var (
	_ ports.UserEligibilityPort        = Unavailable{}
	_ ports.ProductSnapshotPort        = Unavailable{}
	_ ports.DropSnapshotPort           = Unavailable{}
	_ ports.SellerCatalogSnapshotPort  = Unavailable{}
	_ ports.OrderSnapshotPort          = Unavailable{}
	_ ports.PaymentResultPort          = Unavailable{}
	_ ports.CSCasePort                 = Unavailable{}
	_ ports.OperationApprovalPort      = Unavailable{}
	_ ports.BulkAudiencePort           = Unavailable{}
	_ ports.SettlementEventPort        = Unavailable{}
	_ ports.NotificationEventPort      = Unavailable{}
	_ ports.ObservabilityPort          = Unavailable{}
	_ appredemption.ReplayPayloadStore = Unavailable{}
)
