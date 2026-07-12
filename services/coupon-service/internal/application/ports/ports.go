package ports

import (
	"context"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

type WorkloadAccess struct {
	ServiceID   string
	Roles       []string
	OperationID string
	Resources   map[string]string
}

type WorkloadAuthorizationPort interface {
	AuthorizeWorkload(context.Context, WorkloadAccess) error
}

type UserEligibility struct {
	Eligible bool
	Reason   string
	Snapshot shared.SnapshotRef
}

type UserEligibilityPort interface {
	Snapshot(context.Context, string, time.Time) (UserEligibility, error)
}

type ProductSnapshotPort interface {
	VerifyProduct(context.Context, shared.ExternalRef, shared.SnapshotRef) error
}

type DropSnapshotPort interface {
	VerifyDrop(context.Context, shared.ExternalRef, shared.SnapshotRef) error
}

type SellerCatalogSnapshotPort interface {
	VerifySellerOwnership(context.Context, shared.SnapshotRef) error
}

type OrderSnapshotPort interface {
	VerifyOrder(context.Context, shared.SnapshotRef) error
}

type PaymentResultPort interface {
	VerifyPaymentResult(context.Context, shared.ExternalRef, *shared.SnapshotRef, PaymentResultBinding) error
}

type PaymentResultBinding struct {
	RedemptionID string
	OrderID      string
}

type CSCasePort interface {
	VerifyCase(context.Context, string, CSCaseBinding) error
}

type CSCaseBinding struct {
	UserID           string
	RedemptionID     string
	OperationTaskRef string
}

type OperationApprovalPort interface {
	// VerifyApproval checks that approvalRef exists and is bound to the supplied
	// command purpose or external operation-task reference.
	VerifyApproval(ctx context.Context, approvalRef, binding string) error
}

type AudiencePage struct {
	UserIDs    []string
	NextCursor string
	Snapshot   shared.SnapshotRef
}

type BulkAudiencePort interface {
	Page(context.Context, string, time.Time, string, int) (AudiencePage, error)
}

type SettlementEventPort interface {
	DeliverCostAttribution(context.Context, reliability.Event) error
}

type NotificationEventPort interface {
	DeliverCouponEvent(context.Context, reliability.Event) error
}

type ObservabilityPort interface {
	RecordReference(context.Context, string, string, time.Time) error
}
