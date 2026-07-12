package commanding

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type OperationsCommandSubmitter interface {
	SubmitFinalIssueFailure(context.Context, FinalIssueFailureInput, Metadata) (uuid.UUID, error)
	SubmitRedemptionProcessingFailure(context.Context, RedemptionProcessingFailureInput, Metadata) (uuid.UUID, error)
}

// OperationsCommandSource is the transport-neutral deployment hook for the
// externally approved CMD.A.19-22 and failure-reporting CMD.A.19-34 inputs.
type OperationsCommandSource interface {
	Run(context.Context, OperationsCommandSubmitter) error
}

type Metadata struct {
	BusinessKey   string
	CorrelationID string
	CausationID   string
	TraceID       string
}

type FinalIssueFailureInput struct {
	IssueRequestID string
	FailureCode    string
	ApprovalRef    string
}

type RedemptionProcessingFailureInput struct {
	RedemptionID          string
	OriginalOperationType recovery.OperationType
	OriginalPayloadRef    string
	OriginalPayloadHash   string
	BusinessKey           string
	FailureCode           string
	NextAttemptAt         *time.Time
	OccurredAt            time.Time
}

type OperationsIngress struct {
	submitter eventing.CommandSubmitter
	now       func() time.Time
}

func NewOperationsIngress(submitter eventing.CommandSubmitter) (*OperationsIngress, error) {
	if submitter == nil {
		return nil, ingressError("coupon.command_submitter_required", "durable command submitter is required")
	}
	return &OperationsIngress{submitter: submitter, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *OperationsIngress) SubmitFinalIssueFailure(ctx context.Context, input FinalIssueFailureInput, metadata Metadata) (uuid.UUID, error) {
	if err := validateMetadata(metadata); err != nil {
		return uuid.Nil, err
	}
	if !strings.HasPrefix(input.IssueRequestID, "ireq_") || strings.TrimSpace(input.FailureCode) == "" || strings.TrimSpace(input.ApprovalRef) == "" {
		return uuid.Nil, ingressError("coupon.final_issue_failure_input_invalid", "issue request, failure code, and approval reference are required")
	}
	return s.submit(ctx, "CMD.A.19-22", "CouponIssueRequest", input.IssueRequestID, metadata, map[string]any{
		"issueRequestId": input.IssueRequestID, "failureCode": input.FailureCode, "approvalRef": input.ApprovalRef,
	})
}

func (s *OperationsIngress) SubmitRedemptionProcessingFailure(ctx context.Context, input RedemptionProcessingFailureInput, metadata Metadata) (uuid.UUID, error) {
	if err := validateMetadata(metadata); err != nil {
		return uuid.Nil, err
	}
	if !strings.HasPrefix(input.RedemptionID, "redm_") || !validOperation(input.OriginalOperationType) ||
		strings.TrimSpace(input.OriginalPayloadRef) == "" || !validPayloadHash(input.OriginalPayloadHash) ||
		strings.TrimSpace(input.BusinessKey) == "" || strings.TrimSpace(input.FailureCode) == "" || input.OccurredAt.IsZero() {
		return uuid.Nil, ingressError("coupon.redemption_failure_input_invalid", "redemption and immutable failure correlation are required")
	}
	payload := map[string]any{
		"redemptionId": input.RedemptionID, "originalOperationType": input.OriginalOperationType,
		"originalPayloadRef": input.OriginalPayloadRef, "originalPayloadHash": input.OriginalPayloadHash,
		"businessKey": input.BusinessKey, "failureCode": input.FailureCode, "occurredAt": input.OccurredAt.UTC(),
	}
	if input.NextAttemptAt != nil {
		payload["nextAttemptAt"] = input.NextAttemptAt.UTC()
	}
	return s.submit(ctx, "CMD.A.19-34", "CouponEventRecovery", input.RedemptionID, metadata, payload)
}

var _ OperationsCommandSubmitter = (*OperationsIngress)(nil)

func (s *OperationsIngress) submit(ctx context.Context, commandID, aggregateType, aggregateID string, metadata Metadata, payload any) (uuid.UUID, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, oops.In("coupon_command_ingress").Code("coupon.command_payload_encode_failed").Wrap(err)
	}
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(commandID+"\x00"+metadata.BusinessKey))
	return s.submitter.SubmitCommand(ctx, eventing.CommandSubmission{
		ID: id, CommandDocumentID: commandID, AggregateType: aggregateType, AggregateID: aggregateID,
		BusinessKey: metadata.BusinessKey, CorrelationID: metadata.CorrelationID,
		CausationID: metadata.CausationID, TraceID: metadata.TraceID, Payload: encoded, NotBefore: s.now(),
	})
}

func validateMetadata(value Metadata) error {
	if strings.TrimSpace(value.BusinessKey) == "" || strings.TrimSpace(value.CorrelationID) == "" || len(value.BusinessKey) > 200 || len(value.CorrelationID) > 200 {
		return ingressError("coupon.command_ingress_metadata_invalid", "business key and correlation id are required")
	}
	return nil
}

func validPayloadHash(value string) bool {
	return strings.HasPrefix(value, "sha256:") && len(strings.TrimPrefix(value, "sha256:")) > 0
}

func validOperation(value recovery.OperationType) bool {
	switch value {
	case recovery.OperationReserve, recovery.OperationConfirm, recovery.OperationRelease, recovery.OperationReclaim:
		return true
	default:
		return false
	}
}

func ingressError(code, message string) error {
	return oops.In("coupon_command_ingress").Code(code).New(message)
}
