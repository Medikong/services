package recovery

import (
	"regexp"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type Status string

const (
	StatusRecorded     Status = "recorded"
	StatusRetryPending Status = "retry_pending"
	StatusRetrying     Status = "retrying"
	StatusRetryFailed  Status = "retry_failed"
	StatusCompleted    Status = "completed"
	StatusFailedFinal  Status = "failed_final"
)

type OperationType string

const (
	OperationReserve OperationType = "reserve"
	OperationConfirm OperationType = "confirm"
	OperationRelease OperationType = "release"
	OperationReclaim OperationType = "reclaim"
)

type ResultKind string

const (
	ResultTransitioned   ResultKind = "transitioned"
	ResultAlreadyApplied ResultKind = "already_applied"
	ResultFailed         ResultKind = "failed"
)

type AttemptStatus string

const (
	AttemptRetryPending AttemptStatus = "retry_pending"
	AttemptRetrying     AttemptStatus = "retrying"
	AttemptCompleted    AttemptStatus = "completed"
	AttemptFailed       AttemptStatus = "failed"
)

var (
	recoveryIDPattern   = regexp.MustCompile(`^rcvy_[A-Za-z0-9_-]{8,120}$`)
	attemptIDPattern    = regexp.MustCompile(`^att_[A-Za-z0-9_-]{8,120}$`)
	redemptionIDPattern = regexp.MustCompile(`^redm_[A-Za-z0-9_-]{8,120}$`)
)

type Attempt struct {
	RecoveryID  string        `json:"recoveryId"`
	ID          string        `json:"attemptId"`
	BusinessKey string        `json:"businessKey"`
	Status      AttemptStatus `json:"status"`
	StartedAt   *time.Time    `json:"startedAt,omitempty"`
	FinishedAt  *time.Time    `json:"finishedAt,omitempty"`
	ResultKind  ResultKind    `json:"resultKind,omitempty"`
	ResultRef   string        `json:"resultRef,omitempty"`
	FailureCode string        `json:"failureCode,omitempty"`
	Retryable   *bool         `json:"retryable,omitempty"`
	CreatedAt   time.Time     `json:"createdAt"`
}

type Recovery struct {
	ID                    string        `json:"recoveryId"`
	RedemptionID          string        `json:"redemptionId"`
	OriginalOperationType OperationType `json:"originalOperationType"`
	OriginalPayloadRef    string        `json:"originalPayloadRef"`
	OriginalPayloadHash   string        `json:"originalPayloadHash"`
	BusinessKey           string        `json:"businessKey"`
	Status                Status        `json:"status"`
	CurrentAttemptID      string        `json:"currentAttemptId,omitempty"`
	CurrentAttempt        *Attempt      `json:"currentAttempt,omitempty"`
	AttemptCount          int           `json:"attemptCount"`
	NextAttemptAt         *time.Time    `json:"nextAttemptAt,omitempty"`
	ResultKind            ResultKind    `json:"resultKind,omitempty"`
	ResultRef             string        `json:"resultRef,omitempty"`
	FailureCode           string        `json:"failureCode,omitempty"`
	OperationRequestRef   string        `json:"operationRequestRef,omitempty"`
	ApprovalRef           string        `json:"approvalRef,omitempty"`
	LeaseOwner            string        `json:"leaseOwner,omitempty"`
	LeaseUntil            *time.Time    `json:"leaseUntil,omitempty"`
	Version               int64         `json:"version"`
	CreatedAt             time.Time     `json:"createdAt"`
	UpdatedAt             time.Time     `json:"updatedAt"`
}

type Failure struct {
	RecoveryID            string
	RedemptionID          string
	OriginalOperationType OperationType
	OriginalPayloadRef    string
	OriginalPayloadHash   string
	BusinessKey           string
	FailureCode           string
	NextAttemptAt         *time.Time
	OccurredAt            time.Time
}

func RecordFailure(input Failure) (Recovery, reliability.Event, error) {
	recovery := Recovery{
		ID: input.RecoveryID, RedemptionID: input.RedemptionID, OriginalOperationType: input.OriginalOperationType,
		OriginalPayloadRef: input.OriginalPayloadRef, OriginalPayloadHash: input.OriginalPayloadHash,
		BusinessKey: input.BusinessKey, Status: StatusRecorded, FailureCode: input.FailureCode,
		NextAttemptAt: input.NextAttemptAt, CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt,
	}
	if err := recovery.Validate(); err != nil {
		return Recovery{}, reliability.Event{}, err
	}
	return recovery, event(recovery, "EVT.A.19-40", "coupon.recovery.failure_recorded", input.OccurredAt), nil
}

func (r Recovery) Validate() error {
	if !recoveryIDPattern.MatchString(r.ID) || !redemptionIDPattern.MatchString(r.RedemptionID) || strings.TrimSpace(r.OriginalPayloadRef) == "" ||
		strings.TrimSpace(r.OriginalPayloadHash) == "" || strings.TrimSpace(r.BusinessKey) == "" ||
		strings.TrimSpace(r.FailureCode) == "" || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
		return oops.In("coupon_recovery").Code("coupon.recovery_invalid").New("coupon event recovery is incomplete")
	}
	if !validOperation(r.OriginalOperationType) {
		return oops.In("coupon_recovery").Code("coupon.recovery_operation_invalid").New("original coupon operation is not recoverable")
	}
	switch r.Status {
	case StatusRecorded, StatusRetryPending, StatusRetrying, StatusRetryFailed, StatusCompleted, StatusFailedFinal:
		return nil
	default:
		return oops.In("coupon_recovery").Code("coupon.recovery_status_invalid").New("coupon event recovery status is invalid")
	}
}

type RetryRequest struct {
	ExpectedVersion     int64
	AttemptID           string
	NextAttemptAt       time.Time
	ReasonCode          string
	OperationRequestRef string
	ApprovalRef         string
	RequestedAt         time.Time
}

func (r *Recovery) RequestRetry(input RetryRequest) (Attempt, reliability.Event, error) {
	if err := r.expectVersion(input.ExpectedVersion); err != nil {
		return Attempt{}, reliability.Event{}, err
	}
	if r.Status != StatusRecorded && r.Status != StatusRetryFailed {
		return Attempt{}, reliability.Event{}, invalidTransition(r.Status, StatusRetryPending)
	}
	if !attemptIDPattern.MatchString(input.AttemptID) || input.NextAttemptAt.IsZero() || input.RequestedAt.IsZero() ||
		strings.TrimSpace(input.ReasonCode) == "" || strings.TrimSpace(input.OperationRequestRef) == "" || strings.TrimSpace(input.ApprovalRef) == "" {
		return Attempt{}, reliability.Event{}, oops.In("coupon_recovery").Code("coupon.recovery_retry_invalid").New("coupon recovery retry request is incomplete")
	}
	attempt := Attempt{
		RecoveryID: r.ID, ID: input.AttemptID, BusinessKey: r.BusinessKey,
		Status: AttemptRetryPending, CreatedAt: input.RequestedAt,
	}
	r.Status = StatusRetryPending
	r.CurrentAttemptID = input.AttemptID
	r.CurrentAttempt = &attempt
	r.AttemptCount++
	r.NextAttemptAt = &input.NextAttemptAt
	r.ResultKind = ""
	r.ResultRef = ""
	r.OperationRequestRef = input.OperationRequestRef
	r.ApprovalRef = input.ApprovalRef
	r.LeaseOwner = ""
	r.LeaseUntil = nil
	r.Version++
	r.UpdatedAt = input.RequestedAt
	domainEvent := event(*r, "EVT.A.19-39", "coupon.recovery.retry_pending", input.RequestedAt)
	domainEvent.Data.(map[string]any)["reason_code"] = input.ReasonCode
	return attempt, domainEvent, nil
}

func (r *Recovery) Lease(expectedVersion int64, attemptID, businessKey, owner string, until, now time.Time) error {
	if err := r.expectVersion(expectedVersion); err != nil {
		return err
	}
	if r.Status != StatusRetryPending || r.CurrentAttempt == nil || r.CurrentAttempt.Status != AttemptRetryPending {
		return invalidTransition(r.Status, StatusRetrying)
	}
	if err := r.correlate(attemptID, businessKey); err != nil {
		return err
	}
	if strings.TrimSpace(owner) == "" || !until.After(now) {
		return oops.In("coupon_recovery").Code("coupon.recovery_lease_invalid").New("coupon recovery lease owner and future deadline are required")
	}
	r.Status = StatusRetrying
	r.LeaseOwner = owner
	r.LeaseUntil = &until
	r.CurrentAttempt.Status = AttemptRetrying
	r.CurrentAttempt.StartedAt = &now
	r.Version++
	r.UpdatedAt = now
	return nil
}

type ReplayResult struct {
	ExpectedVersion int64
	AttemptID       string
	BusinessKey     string
	Kind            ResultKind
	ResultRef       string
	FailureCode     string
	Retryable       bool
	NextAttemptAt   *time.Time
	RecordedAt      time.Time
}

func (r *Recovery) RecordResult(input ReplayResult) (reliability.Event, error) {
	if err := r.expectVersion(input.ExpectedVersion); err != nil {
		return reliability.Event{}, err
	}
	if r.Status != StatusRetrying || r.CurrentAttempt == nil || r.CurrentAttempt.Status != AttemptRetrying {
		return reliability.Event{}, invalidTransition(r.Status, StatusCompleted)
	}
	if err := r.correlate(input.AttemptID, input.BusinessKey); err != nil {
		return reliability.Event{}, err
	}
	if input.RecordedAt.IsZero() {
		return reliability.Event{}, oops.In("coupon_recovery").Code("coupon.recovery_result_time_required").New("coupon recovery result time is required")
	}
	switch input.Kind {
	case ResultTransitioned, ResultAlreadyApplied:
		if strings.TrimSpace(input.ResultRef) == "" {
			return reliability.Event{}, oops.In("coupon_recovery").Code("coupon.recovery_result_ref_required").New("successful recovery result reference is required")
		}
	case ResultFailed:
		if strings.TrimSpace(input.FailureCode) == "" {
			return reliability.Event{}, oops.In("coupon_recovery").Code("coupon.recovery_failure_code_required").New("failed recovery result requires a failure code")
		}
		if input.Retryable && input.NextAttemptAt == nil {
			return reliability.Event{}, oops.In("coupon_recovery").Code("coupon.recovery_next_attempt_required").New("retryable recovery failure requires a next attempt time")
		}
	default:
		return reliability.Event{}, oops.In("coupon_recovery").Code("coupon.recovery_result_kind_invalid").New("coupon recovery result kind is invalid")
	}
	r.ResultKind = input.Kind
	r.CurrentAttempt.ResultKind = input.Kind
	r.CurrentAttempt.FinishedAt = &input.RecordedAt
	r.LeaseOwner = ""
	r.LeaseUntil = nil
	r.Version++
	r.UpdatedAt = input.RecordedAt
	switch input.Kind {
	case ResultTransitioned, ResultAlreadyApplied:
		r.Status = StatusCompleted
		r.ResultRef = input.ResultRef
		r.FailureCode = ""
		r.NextAttemptAt = nil
		r.CurrentAttempt.Status = AttemptCompleted
		r.CurrentAttempt.ResultRef = input.ResultRef
		r.CurrentAttempt.FailureCode = ""
		r.CurrentAttempt.Retryable = nil
		return event(*r, "EVT.A.19-26", "coupon.recovery.completed", input.RecordedAt), nil
	case ResultFailed:
		r.Status = StatusRetryFailed
		r.ResultRef = ""
		r.FailureCode = input.FailureCode
		r.NextAttemptAt = input.NextAttemptAt
		r.CurrentAttempt.Status = AttemptFailed
		r.CurrentAttempt.ResultRef = ""
		r.CurrentAttempt.FailureCode = input.FailureCode
		r.CurrentAttempt.Retryable = &input.Retryable
		return event(*r, "EVT.A.19-27", "coupon.recovery.retry_failed", input.RecordedAt), nil
	}
	return reliability.Event{}, oops.In("coupon_recovery").Code("coupon.recovery_result_kind_invalid").New("coupon recovery result kind is invalid")
}

type Finalization struct {
	ExpectedVersion     int64
	ReasonCode          string
	OperationRequestRef string
	ApprovalRef         string
	FinalizedAt         time.Time
}

func (r *Recovery) Finalize(input Finalization) (reliability.Event, error) {
	if err := r.expectVersion(input.ExpectedVersion); err != nil {
		return reliability.Event{}, err
	}
	if r.Status != StatusRecorded && r.Status != StatusRetryFailed {
		return reliability.Event{}, invalidTransition(r.Status, StatusFailedFinal)
	}
	if strings.TrimSpace(input.ReasonCode) == "" || strings.TrimSpace(input.OperationRequestRef) == "" ||
		strings.TrimSpace(input.ApprovalRef) == "" || input.FinalizedAt.IsZero() {
		return reliability.Event{}, oops.In("coupon_recovery").Code("coupon.recovery_finalization_invalid").New("approved recovery finalization is incomplete")
	}
	r.Status = StatusFailedFinal
	r.FailureCode = input.ReasonCode
	r.OperationRequestRef = input.OperationRequestRef
	r.ApprovalRef = input.ApprovalRef
	r.NextAttemptAt = nil
	r.LeaseOwner = ""
	r.LeaseUntil = nil
	r.Version++
	r.UpdatedAt = input.FinalizedAt
	return event(*r, "EVT.A.19-30", "coupon.recovery.failed_final", input.FinalizedAt), nil
}

func (r Recovery) correlate(attemptID, businessKey string) error {
	if r.CurrentAttemptID != attemptID || r.BusinessKey != businessKey || r.CurrentAttempt == nil ||
		r.CurrentAttempt.ID != attemptID || r.CurrentAttempt.BusinessKey != businessKey {
		return oops.In("coupon_recovery").Code("coupon.recovery_correlation_mismatch").New("recovery result does not match the current attempt and business key")
	}
	return nil
}

func (r Recovery) expectVersion(expected int64) error {
	if r.Version == expected {
		return nil
	}
	return oops.In("coupon_recovery").Code("coupon.version_conflict").With("recovery_id", r.ID, "expected_version", expected, "actual_version", r.Version).New("coupon event recovery version does not match")
}

func validOperation(value OperationType) bool {
	switch value {
	case OperationReserve, OperationConfirm, OperationRelease, OperationReclaim:
		return true
	default:
		return false
	}
}

func invalidTransition(from, to Status) error {
	return oops.In("coupon_recovery").Code("coupon.recovery_transition_invalid").With("from", from, "to", to).New("coupon event recovery state transition is not allowed")
}

func event(r Recovery, documentID, eventType string, at time.Time) reliability.Event {
	data := map[string]any{
		"recovery_id": r.ID, "redemption_id": r.RedemptionID, "attempt_id": r.CurrentAttemptID, "business_key": r.BusinessKey,
		"original_operation_type": r.OriginalOperationType, "original_payload_ref": r.OriginalPayloadRef,
		"status": r.Status, "result_kind": r.ResultKind, "result_ref": r.ResultRef,
		"failure_code": r.FailureCode, "attempt_count": r.AttemptCount, "next_attempt_at": r.NextAttemptAt,
	}
	return reliability.Event{
		ID: uuid.New(), DocumentID: documentID, Type: eventType,
		AggregateType: "CouponEventRecovery", AggregateID: r.ID, AggregateVersion: r.Version,
		PayloadSchemaVersion: 1, Data: data, OccurredAt: at,
	}
}
