package issuerequest

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/samber/oops"
)

type SourceType string

const (
	SourceClaim         SourceType = "claim"
	SourceRedeemCode    SourceType = "redeem_code"
	SourceBulk          SourceType = "bulk"
	SourceSystemGrant   SourceType = "system_grant"
	SourceOperatorGrant SourceType = "operator_grant"
)

type Status string

const (
	StatusAccepted        Status = "accepted"
	StatusPending         Status = "pending"
	StatusProcessing      Status = "processing"
	StatusFailedRetryable Status = "failed_retryable"
	StatusRetryPending    Status = "retry_pending"
	StatusRejected        Status = "rejected"
	StatusFailedFinal     Status = "failed_final"
	StatusCompleted       Status = "completed"
)

type Request struct {
	ID                       string          `json:"issueRequestId"`
	CampaignID               string          `json:"campaignId"`
	UserID                   string          `json:"userId"`
	BusinessKey              string          `json:"businessKey"`
	SourceType               SourceType      `json:"sourceType"`
	SourceRef                string          `json:"sourceRef,omitempty"`
	Status                   Status          `json:"status"`
	UserCouponID             string          `json:"userCouponId,omitempty"`
	FailureCode              string          `json:"failureCode,omitempty"`
	RetryCount               int             `json:"retryCount"`
	NextAttemptAt            *time.Time      `json:"nextAttemptAt,omitempty"`
	IssuerAndFundingSnapshot json.RawMessage `json:"issuerAndFundingSnapshot"`
	PolicySnapshot           json.RawMessage `json:"policySnapshot"`
	ApprovalRef              string          `json:"approvalRef,omitempty"`
	ResultRef                string          `json:"resultRef,omitempty"`
	LeaseOwner               string          `json:"leaseOwner,omitempty"`
	LeaseUntil               *time.Time      `json:"leaseUntil,omitempty"`
	Version                  int64           `json:"version"`
	CreatedAt                time.Time       `json:"createdAt"`
	UpdatedAt                time.Time       `json:"updatedAt"`
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.CampaignID) == "" || strings.TrimSpace(r.UserID) == "" || strings.TrimSpace(r.BusinessKey) == "" || strings.TrimSpace(r.SourceRef) == "" || r.Version < 0 || !json.Valid(r.IssuerAndFundingSnapshot) || !json.Valid(r.PolicySnapshot) {
		return oops.In("coupon_issue_request").Code("issue_request.invalid").New("issue request identity and funding snapshot are required")
	}
	switch r.SourceType {
	case SourceClaim, SourceRedeemCode, SourceBulk, SourceSystemGrant, SourceOperatorGrant:
	default:
		return oops.In("coupon_issue_request").Code("issue_request.source_invalid").New("issue request source type is not supported")
	}
	if !r.Status.Valid() || r.RetryCount < 0 {
		return oops.In("coupon_issue_request").Code("issue_request.status_invalid").New("issue request status or retry count is invalid")
	}
	if r.Status == StatusCompleted && r.UserCouponID == "" {
		return oops.In("coupon_issue_request").Code("issue_request.result_required").New("completed issue request requires user coupon id")
	}
	if (r.Status == StatusRejected || r.Status == StatusFailedRetryable || r.Status == StatusFailedFinal) && r.FailureCode == "" {
		return oops.In("coupon_issue_request").Code("issue_request.failure_required").New("failed issue request requires failure code")
	}
	return nil
}

func (s Status) Valid() bool {
	switch s {
	case StatusAccepted, StatusPending, StatusProcessing, StatusFailedRetryable, StatusRetryPending, StatusRejected, StatusFailedFinal, StatusCompleted:
		return true
	default:
		return false
	}
}

func (r Request) MarkPending() (Request, error) {
	return r.transition(StatusPending, "", "", nil)
}

func (r Request) MarkProcessing() (Request, error) {
	return r.transition(StatusProcessing, "", "", nil)
}

func (r Request) RecordFailure(code string, retryable bool, nextAttemptAt *time.Time) (Request, error) {
	if retryable && nextAttemptAt == nil {
		return Request{}, ErrInvalidTransition
	}
	if !retryable && nextAttemptAt != nil {
		return Request{}, ErrInvalidTransition
	}
	// CMD.A.19-14 records the failure only. CMD.A.19-22 is the sole path to
	// failed_final after an approved retry stop or non-retryable diagnosis.
	return r.transition(StatusFailedRetryable, "", code, nextAttemptAt)
}

func (r Request) Retry(nextAttemptAt time.Time) (Request, error) {
	return r.transition(StatusRetryPending, "", "", &nextAttemptAt)
}

func (r Request) Reject(code string) (Request, error) {
	return r.transition(StatusRejected, "", code, nil)
}

func (r Request) Complete(userCouponID string) (Request, error) {
	return r.transition(StatusCompleted, userCouponID, "", nil)
}

func (r Request) FinalizeFailure(code string) (Request, error) {
	return r.transition(StatusFailedFinal, "", code, nil)
}

func (r Request) transition(next Status, userCouponID, failureCode string, nextAttemptAt *time.Time) (Request, error) {
	if r.Status == next {
		if (next != StatusCompleted || r.UserCouponID == userCouponID) && (failureCode == "" || r.FailureCode == failureCode) {
			return r, nil
		}
		return Request{}, ErrInvalidTransition
	}
	allowed := false
	switch r.Status {
	case StatusAccepted:
		allowed = next == StatusPending || next == StatusRejected
	case StatusPending, StatusRetryPending:
		allowed = next == StatusProcessing || next == StatusFailedFinal
	case StatusProcessing:
		allowed = next == StatusCompleted || next == StatusFailedRetryable || next == StatusFailedFinal
	case StatusFailedRetryable:
		allowed = next == StatusRetryPending || next == StatusFailedFinal
	}
	if !allowed {
		return Request{}, ErrInvalidTransition
	}
	if next == StatusCompleted && strings.TrimSpace(userCouponID) == "" {
		return Request{}, ErrInvalidTransition
	}
	if (next == StatusRejected || next == StatusFailedRetryable || next == StatusFailedFinal) && strings.TrimSpace(failureCode) == "" {
		return Request{}, ErrInvalidTransition
	}
	r.Status = next
	r.UserCouponID = userCouponID
	r.FailureCode = failureCode
	r.NextAttemptAt = nextAttemptAt
	if next == StatusRetryPending {
		r.RetryCount++
	}
	r.Version++
	return r, nil
}

var (
	ErrNotFound             = oops.In("coupon_issue_request").Code("issue_request.not_found").New("issue request was not found")
	ErrInvalidTransition    = oops.In("coupon_issue_request").Code("issue_request.transition_invalid").New("issue request transition is not allowed")
	ErrVersionConflict      = oops.In("coupon_issue_request").Code("issue_request.version_conflict").New("issue request version does not match")
	ErrIdempotencyConflict  = oops.In("coupon_issue_request").Code("issue_request.idempotency_conflict").New("idempotency key was reused with a different request")
	ErrCommandInProgress    = oops.In("coupon_issue_request").Code("issue_request.command_in_progress").New("the same command is already processing")
	ErrPerUserLimitExceeded = oops.In("coupon_issue_request").Code("issue_request.per_user_limit_exceeded").New("campaign per-user issuance limit was reached")
)
