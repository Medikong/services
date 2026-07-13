package readmodel

import (
	"time"

	"github.com/google/uuid"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

type WalletStatus string

const (
	WalletStatusPending   WalletStatus = "pending"
	WalletStatusAvailable WalletStatus = "available"
	WalletStatusReserved  WalletStatus = "reserved"
	WalletStatusUsed      WalletStatus = "used"
	WalletStatusReclaimed WalletStatus = "reclaimed"
	WalletStatusExpired   WalletStatus = "expired"
	WalletStatusFailed    WalletStatus = "failed"
)

func (s WalletStatus) Valid() bool {
	switch s {
	case WalletStatusPending, WalletStatusAvailable, WalletStatusReserved, WalletStatusUsed,
		WalletStatusReclaimed, WalletStatusExpired, WalletStatusFailed:
		return true
	default:
		return false
	}
}

type ExternalRef struct {
	Context string `json:"context"`
	Type    string `json:"type"`
	ID      string `json:"id"`
}

type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type Benefit struct {
	Type              string `json:"type"`
	Amount            *Money `json:"amount,omitempty"`
	Percentage        string `json:"percentage,omitempty"`
	MaxDiscountAmount *Money `json:"maxDiscountAmount,omitempty"`
}

type ApplicabilityPolicy struct {
	PolicySchemaVersion int           `json:"policySchemaVersion"`
	IncludeTargets      []ExternalRef `json:"includeTargets"`
	ExcludeTargets      []ExternalRef `json:"excludeTargets"`
	MinimumOrderAmount  *Money        `json:"minimumOrderAmount,omitempty"`
	StackingPolicyRef   string        `json:"stackingPolicyRef,omitempty"`
}

type IssuerAndFunding struct {
	IssuerType              string       `json:"issuerType"`
	IssuerRef               ExternalRef  `json:"issuerRef"`
	FunderType              string       `json:"funderType"`
	FunderRef               *ExternalRef `json:"funderRef,omitempty"`
	PlatformSharePercentage string       `json:"platformSharePercentage,omitempty"`
}

type CostShare struct {
	BearerType string       `json:"bearerType"`
	BearerRef  *ExternalRef `json:"bearerRef,omitempty"`
	Amount     Money        `json:"amount"`
}

type WalletCoupon struct {
	UserID            string
	UserCouponID      string
	CampaignID        string
	DisplayName       string
	Benefit           Benefit
	Status            WalletStatus
	UsableFrom        time.Time
	ExpiresAt         time.Time
	LastEventID       uuid.UUID
	ProjectionVersion int64
	UpdatedAt         time.Time
}

type ReadOnlyNotice struct {
	ControlID         string
	ScopeType         string
	ScopeRef          string
	Message           string
	EffectiveFrom     time.Time
	Active            bool
	LastEventID       uuid.UUID
	ProjectionVersion int64
	UpdatedAt         time.Time
}

// CouponDetailDocument contains only fields allowed by CouponDetailData. It is
// deliberately typed so an external aggregate payload cannot leak through the
// read model JSON column.
type CouponDetailDocument struct {
	UserCouponID     string              `json:"userCouponId"`
	CampaignID       string              `json:"campaignId"`
	DisplayName      string              `json:"displayName"`
	Benefit          Benefit             `json:"benefit"`
	Status           WalletStatus        `json:"status"`
	UsableFrom       time.Time           `json:"usableFrom"`
	ExpiresAt        time.Time           `json:"expiresAt"`
	PolicyVersion    int                 `json:"policyVersion"`
	Applicability    ApplicabilityPolicy `json:"applicability"`
	IssuerAndFunding IssuerAndFunding    `json:"issuerAndFunding"`
	ActiveNotice     *ReadOnlyNotice     `json:"-"`
}

type CouponDetail struct {
	UserID            string
	Document          CouponDetailDocument
	LastEventID       uuid.UUID
	ProjectionVersion int64
	UpdatedAt         time.Time
}

type PerformanceCounts struct {
	Requested   int64
	Issued      int64
	Rejected    int64
	FailedFinal int64
	Reserved    int64
	Confirmed   int64
	Released    int64
	Reclaimed   int64
}

type CampaignPerformance struct {
	CampaignID        string
	Counts            PerformanceCounts
	ConfirmedDiscount *Money
	ReclaimedDiscount *Money
	AsOf              time.Time
}

type BulkJobSummary struct {
	Counts        PerformanceCounts
	NextCursorRef string
	AsOf          time.Time
}

type Failure struct {
	FailureID         string
	Kind              string
	Status            string
	BusinessKey       string
	SourceRef         string
	OriginalOperation string
	CurrentAttemptID  string
	ResultKind        string
	ResultRef         string
	FailureCode       string
	AttemptCount      int
	NextAttemptAt     *time.Time
	LastEventID       uuid.UUID
	ProjectionVersion int64
	UpdatedAt         time.Time
}

type TimelineEvent struct {
	TimelineID        uuid.UUID
	UserID            string
	UserCouponID      string
	EventType         string
	ResultRef         ExternalRef
	OccurredAt        time.Time
	LastEventID       uuid.UUID
	ProjectionVersion int64
}

type SignalName string

const (
	SignalIssuance   SignalName = "issuance"
	SignalRedemption SignalName = "redemption"
	SignalRecovery   SignalName = "recovery"
	SignalPostgres   SignalName = "postgres"
	SignalRedis      SignalName = "redis"
	SignalMQ         SignalName = "mq"
	SignalWorkers    SignalName = "workers"
)

type IncidentSignal struct {
	Name              SignalName
	IncidentKey       string
	ScopeRef          string
	Status            string
	BusinessMetrics   map[string]int64
	ObservabilityRef  string
	ObservedAt        time.Time
	LastEventID       uuid.UUID
	ProjectionVersion int64
}

type IncidentStatus struct {
	Signals map[SignalName]IncidentSignal
	AsOf    time.Time
}

type CostAttributionKind string

const (
	CostAttributionConfirmed CostAttributionKind = "confirmed"
	CostAttributionReclaimed CostAttributionKind = "reclaimed"
)

type CostAttribution struct {
	AttributionID     uuid.UUID
	OrderRef          ExternalRef
	RedemptionID      string
	CampaignID        string
	Kind              CostAttributionKind
	Discount          Money
	Shares            []CostShare
	SettlementRef     string
	OccurredAt        time.Time
	LastEventID       uuid.UUID
	ProjectionVersion int64
}

type Page[T any] struct {
	Items      []T
	NextCursor string
}
