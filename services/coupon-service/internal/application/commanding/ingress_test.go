package commanding

import (
	"context"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/google/uuid"
)

func TestOperationsIngressSubmitsFinalIssueAndRedemptionFailureCommands(t *testing.T) {
	submitter := &submitterFake{}
	service, err := NewOperationsIngress(submitter)
	if err != nil {
		t.Fatal(err)
	}
	metadata := Metadata{
		BusinessKey: "operation:task-1", CorrelationID: "request-1",
		CausationID: "operation-task:task-1", TraceID: "trace-1",
	}

	issueID, err := service.SubmitFinalIssueFailure(context.Background(), FinalIssueFailureInput{
		IssueRequestID: "ireq_12345678", FailureCode: "retry_exhausted", ApprovalRef: "approval-1",
	}, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if issueID == uuid.Nil || submitter.last.CommandDocumentID != "CMD.A.19-22" || submitter.last.AggregateID != "ireq_12345678" {
		t.Fatalf("final issue submission = %#v", submitter.last)
	}

	next := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	recoveryID, err := service.SubmitRedemptionProcessingFailure(context.Background(), RedemptionProcessingFailureInput{
		RedemptionID: "redm_12345678", OriginalOperationType: recovery.OperationConfirm,
		OriginalPayloadRef: "payloads/order-1-confirm", OriginalPayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		BusinessKey: "order-1:coupon-1:confirm", FailureCode: "postgres_timeout", NextAttemptAt: &next, OccurredAt: next.Add(-time.Minute),
	}, Metadata{BusinessKey: "failure:order-1", CorrelationID: "request-2", CausationID: "API.A.19-07", TraceID: "trace-2"})
	if err != nil {
		t.Fatal(err)
	}
	if recoveryID == uuid.Nil || submitter.last.CommandDocumentID != "CMD.A.19-34" || submitter.last.AggregateID != "redm_12345678" {
		t.Fatalf("redemption failure submission = %#v", submitter.last)
	}
}

func TestOperationsIngressRejectsMissingExternalCorrelation(t *testing.T) {
	service, err := NewOperationsIngress(&submitterFake{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SubmitFinalIssueFailure(context.Background(), FinalIssueFailureInput{
		IssueRequestID: "ireq_12345678", FailureCode: "retry_exhausted",
	}, Metadata{BusinessKey: "operation:task-1", CorrelationID: "request-1"}); err == nil {
		t.Fatal("SubmitFinalIssueFailure() error = nil without approval")
	}
	if _, err := service.SubmitRedemptionProcessingFailure(context.Background(), RedemptionProcessingFailureInput{
		RedemptionID: "redm_12345678", OriginalOperationType: recovery.OperationConfirm,
		BusinessKey: "order-1:coupon-1:confirm", FailureCode: "postgres_timeout", OccurredAt: time.Now(),
	}, Metadata{BusinessKey: "failure:order-1", CorrelationID: "request-2"}); err == nil {
		t.Fatal("SubmitRedemptionProcessingFailure() error = nil without immutable payload correlation")
	}
}

type submitterFake struct {
	last eventing.CommandSubmission
}

func (f *submitterFake) SubmitCommand(_ context.Context, input eventing.CommandSubmission) (uuid.UUID, error) {
	f.last = input
	return input.ID, nil
}
