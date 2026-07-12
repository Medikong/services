package http

type ExternalRef struct {
	Context string `json:"context"`
	Type    string `json:"type"`
	ID      string `json:"id"`
}

type SnapshotRef struct {
	SourceRef     ExternalRef `json:"sourceRef"`
	SourceVersion string      `json:"sourceVersion"`
	CapturedAt    string      `json:"capturedAt"`
	PayloadHash   string      `json:"payloadHash"`
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
	PolicySchemaVersion *int          `json:"policySchemaVersion"`
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

type RedeemCouponCodeRequest struct {
	Code string `json:"code"`
}

type OrderItemSnapshot struct {
	ProductRef  ExternalRef  `json:"productRef"`
	DropRef     *ExternalRef `json:"dropRef,omitempty"`
	SellerRef   ExternalRef  `json:"sellerRef"`
	CategoryRef *ExternalRef `json:"categoryRef,omitempty"`
	Quantity    *int         `json:"quantity"`
	UnitPrice   Money        `json:"unitPrice"`
}

type OrderCandidateSnapshot struct {
	SnapshotRef SnapshotRef         `json:"snapshotRef"`
	OrderID     string              `json:"orderId"`
	UserID      string              `json:"userId"`
	Items       []OrderItemSnapshot `json:"items"`
	ShippingFee Money               `json:"shippingFee"`
}

type ValidateCouponRequest struct {
	UserCouponID      string                 `json:"userCouponId"`
	OrderSnapshot     OrderCandidateSnapshot `json:"orderSnapshot"`
	PolicyVersion     *int64                 `json:"policyVersion"`
	StackingPolicyRef string                 `json:"stackingPolicyRef,omitempty"`
}

type ExpectedVersionRequest struct {
	ExpectedVersion *int64 `json:"expectedVersion"`
}

type RedemptionTransitionRequest struct {
	ExpectedVersion *int64       `json:"expectedVersion"`
	ResultRef       ExternalRef  `json:"resultRef"`
	ResultSnapshot  *SnapshotRef `json:"resultSnapshot,omitempty"`
	ReasonCode      string       `json:"reasonCode"`
}

type CreateCouponCampaignRequest struct {
	DisplayName         string              `json:"displayName"`
	Description         string              `json:"description,omitempty"`
	Benefit             Benefit             `json:"benefit"`
	Applicability       ApplicabilityPolicy `json:"applicability"`
	IssuerAndFunding    IssuerAndFunding    `json:"issuerAndFunding"`
	UsableFrom          string              `json:"usableFrom"`
	ExpiresAt           string              `json:"expiresAt"`
	OwnerSnapshot       SnapshotRef         `json:"ownerSnapshot"`
	ExternalBusinessRef string              `json:"externalBusinessRef,omitempty"`
}

type ConfigureIssuanceRequest struct {
	ExpectedVersion *int64 `json:"expectedVersion"`
	TotalQuantity   *int64 `json:"totalQuantity"`
	PerUserLimit    *int64 `json:"perUserLimit"`
	ClaimStartsAt   string `json:"claimStartsAt"`
	ClaimEndsAt     string `json:"claimEndsAt"`
}

type ReviewCampaignRequest struct {
	ExpectedVersion         *int64      `json:"expectedVersion"`
	Decision                string      `json:"decision"`
	ReasonCode              string      `json:"reasonCode"`
	SellerOwnershipSnapshot SnapshotRef `json:"sellerOwnershipSnapshot"`
}

type CreatePolicyVersionRequest struct {
	ExpectedVersion  *int64               `json:"expectedVersion"`
	EffectiveAt      string               `json:"effectiveAt"`
	Benefit          *Benefit             `json:"benefit,omitempty"`
	Applicability    *ApplicabilityPolicy `json:"applicability,omitempty"`
	IssuerAndFunding *IssuerAndFunding    `json:"issuerAndFunding,omitempty"`
}

type CreateBulkIssueJobRequest struct {
	CampaignID       string      `json:"campaignId"`
	AudienceSnapshot SnapshotRef `json:"audienceSnapshot"`
	EvaluationAsOf   string      `json:"evaluationAsOf"`
	OperationTaskRef ExternalRef `json:"operationTaskRef"`
}

type OperationalScope struct {
	Type string      `json:"type"`
	Ref  ExternalRef `json:"ref"`
}

type ApplyOperationalControlRequest struct {
	Scope            OperationalScope `json:"scope"`
	BlockIssuance    *bool            `json:"blockIssuance"`
	BlockRedemption  *bool            `json:"blockRedemption"`
	EffectiveFrom    string           `json:"effectiveFrom"`
	Active           *bool            `json:"active"`
	ReasonCode       string           `json:"reasonCode,omitempty"`
	OperationTaskRef ExternalRef      `json:"operationTaskRef"`
}

type ApplyReadOnlyNoticeRequest struct {
	ExpectedVersion *int64 `json:"expectedVersion"`
	Message         string `json:"message"`
	EffectiveFrom   string `json:"effectiveFrom"`
	Active          *bool  `json:"active"`
}

type RetryRecoveryRequest struct {
	ReasonCode       string      `json:"reasonCode"`
	OperationTaskRef ExternalRef `json:"operationTaskRef"`
}

type FinalizeRecoveryRequest struct {
	ReasonCode       string      `json:"reasonCode"`
	OperationTaskRef ExternalRef `json:"operationTaskRef"`
}

type CreateCompensationIssueRequest struct {
	CampaignID string      `json:"campaignId"`
	UserID     string      `json:"userId"`
	SourceRef  ExternalRef `json:"sourceRef"`
	ReasonCode string      `json:"reasonCode"`
}

type IssueAcceptedData struct {
	IssueRequestID string `json:"issueRequestId"`
	Status         string `json:"status"`
	StatusPath     string `json:"statusPath"`
}

type WalletCoupon struct {
	UserCouponID string  `json:"userCouponId"`
	CampaignID   string  `json:"campaignId"`
	DisplayName  string  `json:"displayName"`
	Benefit      Benefit `json:"benefit"`
	Status       string  `json:"status"`
	UsableFrom   string  `json:"usableFrom"`
	ExpiresAt    string  `json:"expiresAt"`
}

type ReadOnlyNotice struct {
	ControlID     string `json:"controlId"`
	Message       string `json:"message"`
	EffectiveFrom string `json:"effectiveFrom"`
}

type WalletCouponListData struct {
	Items         []WalletCoupon   `json:"items"`
	NextCursor    string           `json:"nextCursor,omitempty"`
	ActiveNotices []ReadOnlyNotice `json:"activeNotices"`
}

type CouponDetailData struct {
	UserCouponID     string              `json:"userCouponId"`
	CampaignID       string              `json:"campaignId"`
	DisplayName      string              `json:"displayName"`
	Benefit          Benefit             `json:"benefit"`
	Status           string              `json:"status"`
	UsableFrom       string              `json:"usableFrom"`
	ExpiresAt        string              `json:"expiresAt"`
	PolicyVersion    int64               `json:"policyVersion"`
	Applicability    ApplicabilityPolicy `json:"applicability"`
	IssuerAndFunding IssuerAndFunding    `json:"issuerAndFunding"`
	ActiveNotice     *ReadOnlyNotice     `json:"activeNotice,omitempty"`
}

type CostShare struct {
	BearerType string       `json:"bearerType"`
	BearerRef  *ExternalRef `json:"bearerRef,omitempty"`
	Amount     Money        `json:"amount"`
}

type CouponValidationData struct {
	RedemptionID     string      `json:"redemptionId"`
	Eligible         bool        `json:"eligible"`
	ReasonCode       string      `json:"reasonCode,omitempty"`
	Discount         Money       `json:"discount"`
	FinalOrderAmount Money       `json:"finalOrderAmount"`
	CostShares       []CostShare `json:"costShares,omitempty"`
	PolicyVersion    int64       `json:"policyVersion"`
	OrderSnapshotRef SnapshotRef `json:"orderSnapshotRef"`
	Version          int64       `json:"version"`
}

type RedemptionData struct {
	RedemptionID  string       `json:"redemptionId"`
	Status        string       `json:"status"`
	Version       int64        `json:"version"`
	Discount      Money        `json:"discount"`
	ReservedUntil string       `json:"reservedUntil,omitempty"`
	ResultRef     *ExternalRef `json:"resultRef,omitempty"`
}

type CampaignData struct {
	CampaignID    string `json:"campaignId"`
	Status        string `json:"status"`
	PolicyVersion int64  `json:"policyVersion"`
	Version       int64  `json:"version"`
}

type BulkJobAcceptedData struct {
	BulkJobID  string `json:"bulkJobId"`
	Status     string `json:"status"`
	StatusPath string `json:"statusPath"`
}

type PerformanceCounts struct {
	Requested   int64 `json:"requested"`
	Issued      int64 `json:"issued"`
	Rejected    int64 `json:"rejected"`
	FailedFinal int64 `json:"failedFinal"`
	Reserved    int64 `json:"reserved"`
	Confirmed   int64 `json:"confirmed"`
	Released    int64 `json:"released"`
	Reclaimed   int64 `json:"reclaimed"`
}

type BulkJobData struct {
	BulkJobID      string            `json:"bulkJobId"`
	CampaignID     string            `json:"campaignId"`
	Status         string            `json:"status"`
	Counts         PerformanceCounts `json:"counts"`
	EvaluationAsOf string            `json:"evaluationAsOf"`
	NextCursorRef  string            `json:"nextCursorRef,omitempty"`
}

type CampaignPerformanceData struct {
	CampaignID        string            `json:"campaignId"`
	Counts            PerformanceCounts `json:"counts"`
	ConfirmedDiscount *Money            `json:"confirmedDiscount,omitempty"`
	ReclaimedDiscount *Money            `json:"reclaimedDiscount,omitempty"`
}

type OperationalControlData struct {
	ControlID       string           `json:"controlId"`
	Scope           OperationalScope `json:"scope"`
	BlockIssuance   bool             `json:"blockIssuance"`
	BlockRedemption bool             `json:"blockRedemption"`
	EffectiveFrom   string           `json:"effectiveFrom"`
	Active          bool             `json:"active"`
	Version         int64            `json:"version"`
}

type ReadOnlyNoticeData struct {
	ControlID     string `json:"controlId"`
	Message       string `json:"message"`
	EffectiveFrom string `json:"effectiveFrom"`
	Active        bool   `json:"active"`
	Version       int64  `json:"version"`
}

type RecoveryItem struct {
	RecoveryID            string       `json:"recoveryId"`
	Status                string       `json:"status"`
	OriginalOperationType string       `json:"originalOperationType"`
	OriginalPayloadRef    ExternalRef  `json:"originalPayloadRef"`
	BusinessKeyRef        string       `json:"businessKeyRef"`
	AttemptID             string       `json:"attemptId,omitempty"`
	AttemptCount          int64        `json:"attemptCount"`
	NextAttemptAt         string       `json:"nextAttemptAt,omitempty"`
	ResultKind            string       `json:"resultKind,omitempty"`
	ResultRef             *ExternalRef `json:"resultRef,omitempty"`
	FailureCode           string       `json:"failureCode,omitempty"`
	UpdatedAt             string       `json:"updatedAt"`
}

type RecoveryListData struct {
	Items      []RecoveryItem `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type RecoveryAttemptAcceptedData struct {
	RecoveryID string `json:"recoveryId"`
	AttemptID  string `json:"attemptId"`
	Status     string `json:"status"`
	StatusPath string `json:"statusPath"`
}

type TimelineEvent struct {
	EventID          string      `json:"eventId"`
	EventType        string      `json:"eventType"`
	OccurredAt       string      `json:"occurredAt"`
	CampaignID       string      `json:"campaignId,omitempty"`
	UserCouponID     string      `json:"userCouponId,omitempty"`
	ResultRef        ExternalRef `json:"resultRef"`
	PublicReasonCode string      `json:"publicReasonCode,omitempty"`
}

type TimelineData struct {
	Items      []TimelineEvent `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type SignalStatus struct {
	Status     string `json:"status"`
	AsOf       string `json:"asOf"`
	LagSeconds *int64 `json:"lagSeconds,omitempty"`
}

type IncidentStatusData struct {
	Issuance   SignalStatus `json:"issuance"`
	Redemption SignalStatus `json:"redemption"`
	Recovery   SignalStatus `json:"recovery"`
	Postgres   SignalStatus `json:"postgres"`
	Redis      SignalStatus `json:"redis"`
	MQ         SignalStatus `json:"mq"`
	Workers    SignalStatus `json:"workers"`
}

type CostAttributionItem struct {
	AttributionID string      `json:"attributionId"`
	OrderRef      ExternalRef `json:"orderRef"`
	RedemptionID  string      `json:"redemptionId"`
	CampaignID    string      `json:"campaignId"`
	Kind          string      `json:"kind"`
	Discount      Money       `json:"discount"`
	Shares        []CostShare `json:"shares"`
	OccurredAt    string      `json:"occurredAt"`
}

type CostAttributionListData struct {
	Items      []CostAttributionItem `json:"items"`
	NextCursor string                `json:"nextCursor,omitempty"`
}
