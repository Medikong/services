package readmodel

import (
	"context"
	"time"

	"github.com/samber/oops"
)

type WalletQuery struct {
	UserID string
	Status WalletStatus
	Cursor string
	Limit  int
}

type PerformanceQuery struct {
	CampaignID string
	From       *time.Time
	To         *time.Time
}

type FailureQuery struct {
	Kind                  string
	Status                string
	OriginalOperationType string
	Cursor                string
	Limit                 int
}

type TimelineQuery struct {
	UserID string
	Cursor string
	Limit  int
}

type CostAttributionQuery struct {
	OrderID    string
	CampaignID string
	From       *time.Time
	To         *time.Time
	Cursor     string
	Limit      int
}

type NoticeScope struct {
	Type string
	Ref  string
}

type NoticeQuery struct {
	Scopes []NoticeScope
	AsOf   time.Time
	Limit  int
}

type Repository interface {
	ListWallet(context.Context, WalletQuery) (Page[WalletCoupon], error)
	GetCouponDetail(context.Context, string, string) (CouponDetail, error)
	CampaignPerformance(context.Context, PerformanceQuery) (CampaignPerformance, error)
	BulkJobSummary(context.Context, string) (BulkJobSummary, error)
	ListFailures(context.Context, FailureQuery) (Page[Failure], error)
	ListTimeline(context.Context, TimelineQuery) (Page[TimelineEvent], error)
	GetIncidentStatus(context.Context) (IncidentStatus, error)
	ListCostAttributions(context.Context, CostAttributionQuery) (Page[CostAttribution], error)
	ListActiveNotices(context.Context, NoticeQuery) ([]ReadOnlyNotice, error)
}

var (
	ErrNotFound      = oops.In("coupon_read_model").Code("coupon.read_model_not_found").New("coupon read model was not found")
	ErrInvalidQuery  = oops.In("coupon_read_model").Code("coupon.read_model_query_invalid").New("coupon read model query is invalid")
	ErrInvalidCursor = oops.In("coupon_read_model").Code("coupon.read_model_cursor_invalid").New("coupon read model cursor is invalid")
)

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultLimit, nil
	}
	if limit < 1 || limit > MaxLimit {
		return 0, ErrInvalidQuery
	}
	return limit, nil
}
