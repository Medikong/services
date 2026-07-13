package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	domainoperations "github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	domainredemption "github.com/Medikong/services/services/coupon-service/internal/domain/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
)

type Metadata struct {
	IdempotencyKey string
	BusinessKey    string
	CorrelationID  string
	CausationID    string
	TraceID        string
	RequestedAt    time.Time
	LeaseUntil     time.Time
	ExpiresAt      time.Time
}

type RegisterBulkJobInput struct {
	CampaignID       string             `json:"campaignId"`
	OwnerServiceID   string             `json:"ownerServiceId"`
	AudienceSnapshot shared.SnapshotRef `json:"audienceSnapshot"`
	EvaluationAsOf   time.Time          `json:"evaluationAsOf"`
	OperationTaskRef shared.ExternalRef `json:"operationTaskRef"`
	ApprovalRef      string             `json:"approvalRef"`
}

type AggregateBulkResultInput struct {
	BulkJobID       string    `json:"bulkJobId"`
	ExpectedVersion int64     `json:"expectedVersion"`
	TargetCount     *int64    `json:"targetCount,omitempty"`
	SucceededCount  int64     `json:"succeededCount"`
	RejectedCount   int64     `json:"rejectedCount"`
	FailedCount     int64     `json:"failedCount"`
	ResultRef       string    `json:"resultRef"`
	Final           bool      `json:"final"`
	RecordedAt      time.Time `json:"recordedAt"`
}

type ApplyOperationalStopInput struct {
	Scopes           []domainoperations.Scope `json:"scopes"`
	Active           bool                     `json:"active"`
	EffectiveFrom    time.Time                `json:"effectiveFrom"`
	BlockIssuance    bool                     `json:"blockIssuance"`
	BlockRedemption  bool                     `json:"blockRedemption"`
	OperationTaskRef shared.ExternalRef       `json:"operationTaskRef"`
	ApprovalRef      string                   `json:"approvalRef"`
	ReasonCode       string                   `json:"reasonCode"`
}

type RequestRecoveryInput struct {
	RecoveryID       string             `json:"recoveryId"`
	ReasonCode       string             `json:"reasonCode"`
	NextAttemptAt    time.Time          `json:"nextAttemptAt"`
	OperationTaskRef shared.ExternalRef `json:"operationTaskRef"`
	ApprovalRef      string             `json:"approvalRef"`
}

type ExpireUserCouponInput struct {
	UserCouponID    string    `json:"userCouponId"`
	ExpectedVersion int64     `json:"expectedVersion"`
	AsOf            time.Time `json:"asOf"`
}

type FinalizeRecoveryInput struct {
	RecoveryID       string             `json:"recoveryId"`
	ReasonCode       string             `json:"reasonCode"`
	OperationTaskRef shared.ExternalRef `json:"operationTaskRef"`
	ApprovalRef      string             `json:"approvalRef"`
}

type ApplyReadOnlyNoticeInput struct {
	ControlID        string             `json:"controlId"`
	ExpectedVersion  int64              `json:"expectedVersion"`
	Message          string             `json:"message"`
	EffectiveFrom    time.Time          `json:"effectiveFrom"`
	Active           bool               `json:"active"`
	OperationTaskRef shared.ExternalRef `json:"operationTaskRef"`
	ApprovalRef      string             `json:"approvalRef"`
}

type RecordRecoveryResultInput struct {
	RecoveryID    string              `json:"recoveryId"`
	AttemptID     string              `json:"attemptId"`
	BusinessKey   string              `json:"businessKey"`
	Kind          recovery.ResultKind `json:"kind"`
	ResultRef     string              `json:"resultRef,omitempty"`
	FailureCode   string              `json:"failureCode,omitempty"`
	Retryable     bool                `json:"retryable"`
	NextAttemptAt *time.Time          `json:"nextAttemptAt,omitempty"`
	RecordedAt    time.Time           `json:"recordedAt,omitempty"`
}

type RecordProcessingFailureInput struct {
	RedemptionID          string                 `json:"redemptionId"`
	OriginalOperationType recovery.OperationType `json:"originalOperationType"`
	OriginalPayloadRef    string                 `json:"originalPayloadRef"`
	OriginalPayloadHash   string                 `json:"originalPayloadHash"`
	BusinessKey           string                 `json:"businessKey"`
	FailureCode           string                 `json:"failureCode"`
	NextAttemptAt         *time.Time             `json:"nextAttemptAt,omitempty"`
	OccurredAt            time.Time              `json:"occurredAt"`
}

type Dependencies struct {
	BulkJobs    bulk.Repository
	Controls    domainoperations.Repository
	Recoveries  recovery.Repository
	UserCoupons usercoupon.Repository
	Redemptions ConsumingRedemptionReader
	Audience    ports.BulkAudiencePort
	Approvals   ports.OperationApprovalPort
	Cases       ports.CSCasePort
}

type ConsumingRedemptionReader interface {
	FindConsumingByUserCoupon(context.Context, string) (domainredemption.Redemption, bool, error)
}

type Service struct {
	deps Dependencies
}

func NewService(deps Dependencies) (*Service, error) {
	if deps.BulkJobs == nil || deps.Controls == nil || deps.Recoveries == nil || deps.UserCoupons == nil || deps.Redemptions == nil ||
		deps.Audience == nil || deps.Approvals == nil || deps.Cases == nil {
		return nil, inputError("coupon.operations_dependency_required", "operations application dependencies are required")
	}
	return &Service{deps: deps}, nil
}

func (s *Service) RegisterBulkJob(ctx context.Context, input RegisterBulkJobInput, metadata Metadata) (bulk.Job, error) {
	if strings.TrimSpace(input.CampaignID) == "" || strings.TrimSpace(input.OwnerServiceID) == "" ||
		utf8.RuneCountInString(input.OwnerServiceID) > 200 || input.EvaluationAsOf.IsZero() {
		return bulk.Job{}, inputError("coupon.bulk_job_input_invalid", "campaign, owner service, and evaluation time are required")
	}
	if err := input.AudienceSnapshot.Validate(); err != nil {
		return bulk.Job{}, err
	}
	if err := s.verifyApprovedTask(ctx, input.OperationTaskRef, input.ApprovalRef); err != nil {
		return bulk.Job{}, err
	}
	page, err := s.deps.Audience.Page(ctx, input.AudienceSnapshot.SourceRef.ID, input.EvaluationAsOf, "", 1)
	if err != nil {
		return bulk.Job{}, oops.In("coupon_operations_application").Code("coupon.audience_snapshot_verification_failed").Wrap(err)
	}
	if page.Snapshot != input.AudienceSnapshot {
		return bulk.Job{}, inputError("coupon.audience_snapshot_mismatch", "audience source does not confirm the supplied immutable snapshot")
	}
	command, err := newCommand("CMD.A.19-08", "coupon.bulk_job.register", []string{
		input.CampaignID, input.OperationTaskRef.ID, input.AudienceSnapshot.PayloadHash,
	}, input, metadata)
	if err != nil {
		return bulk.Job{}, err
	}
	job, domainEvent, err := bulk.Register(bulk.Registration{
		JobID: stableID("bjob", command.BusinessKey), CampaignID: input.CampaignID, OwnerServiceID: input.OwnerServiceID,
		AudienceSnapshot: input.AudienceSnapshot, EvaluationAsOf: input.EvaluationAsOf,
		OperationRequestRef: input.OperationTaskRef.ID, ApprovalRef: input.ApprovalRef,
		CreatedAt: metadata.RequestedAt,
	})
	if err != nil {
		return bulk.Job{}, err
	}
	return s.deps.BulkJobs.Create(ctx, job, domainEvent, command)
}

func (s *Service) AggregateBulkResult(ctx context.Context, input AggregateBulkResultInput, metadata Metadata) (bulk.Job, error) {
	if strings.TrimSpace(input.BulkJobID) == "" || input.ExpectedVersion < 0 || input.RecordedAt.IsZero() {
		return bulk.Job{}, inputError("coupon.bulk_result_input_invalid", "bulk job, expected version, and recorded time are required")
	}
	command, err := newCommand("CMD.A.19-18", "coupon.bulk_job.aggregate_result", []string{input.BulkJobID, input.ResultRef}, input, metadata)
	if err != nil {
		return bulk.Job{}, err
	}
	return s.deps.BulkJobs.AggregateResult(ctx, input.BulkJobID, input.ExpectedVersion, bulk.ResultDelta{
		TargetCount: input.TargetCount, SucceededCount: input.SucceededCount, RejectedCount: input.RejectedCount,
		FailedCount: input.FailedCount, ResultRef: input.ResultRef, Final: input.Final, RecordedAt: input.RecordedAt,
	}, command)
}

func (s *Service) ApplyOperationalStop(ctx context.Context, input ApplyOperationalStopInput, metadata Metadata) (domainoperations.Control, error) {
	if err := s.verifyApprovedTask(ctx, input.OperationTaskRef, input.ApprovalRef); err != nil {
		return domainoperations.Control{}, err
	}
	command, err := newCommand("CMD.A.19-20", "coupon.operational_control.apply_stop", []string{
		input.OperationTaskRef.ID, input.EffectiveFrom.UTC().Format(time.RFC3339Nano),
	}, input, metadata)
	if err != nil {
		return domainoperations.Control{}, err
	}
	control, domainEvent, err := domainoperations.ApplyStop(domainoperations.Stop{
		ControlID: stableID("ctrl", command.BusinessKey), Scopes: input.Scopes, Active: input.Active,
		EffectiveFrom: input.EffectiveFrom, BlockIssuance: input.BlockIssuance,
		BlockRedemption: input.BlockRedemption, OperationRequestRef: input.OperationTaskRef.ID,
		ApprovalRef: input.ApprovalRef, ReasonCode: input.ReasonCode, AppliedAt: metadata.RequestedAt,
	})
	if err != nil {
		return domainoperations.Control{}, err
	}
	return s.deps.Controls.Create(ctx, control, domainEvent, command)
}

func (s *Service) RequestRecovery(ctx context.Context, input RequestRecoveryInput, metadata Metadata) (recovery.Recovery, error) {
	if strings.TrimSpace(input.RecoveryID) == "" || strings.TrimSpace(input.ReasonCode) == "" ||
		input.NextAttemptAt.IsZero() || input.NextAttemptAt.Before(metadata.RequestedAt) {
		return recovery.Recovery{}, inputError("coupon.recovery_request_invalid", "recovery, reason, and a current or future next attempt time are required")
	}
	if err := s.verifyApprovedTask(ctx, input.OperationTaskRef, input.ApprovalRef); err != nil {
		return recovery.Recovery{}, err
	}
	current, err := s.deps.Recoveries.Find(ctx, input.RecoveryID)
	if err != nil {
		return recovery.Recovery{}, err
	}
	command, err := newCommand("CMD.A.19-21", "coupon.recovery.request", []string{
		input.RecoveryID, input.OperationTaskRef.ID,
	}, input, metadata)
	if err != nil {
		return recovery.Recovery{}, err
	}
	return s.deps.Recoveries.RequestRetry(ctx, input.RecoveryID, recovery.RetryRequest{
		ExpectedVersion: current.Version, AttemptID: stableID("att", command.BusinessKey),
		NextAttemptAt: input.NextAttemptAt, ReasonCode: input.ReasonCode,
		OperationRequestRef: input.OperationTaskRef.ID, ApprovalRef: input.ApprovalRef,
		RequestedAt: metadata.RequestedAt,
	}, command)
}

func (s *Service) ExpireUserCoupon(ctx context.Context, input ExpireUserCouponInput, metadata Metadata) (usercoupon.Mutation, error) {
	if strings.TrimSpace(input.UserCouponID) == "" || input.ExpectedVersion < 0 || input.AsOf.IsZero() {
		return usercoupon.Mutation{}, inputError("coupon.expiration_input_invalid", "user coupon, expected version, and expiration time are required")
	}
	command, err := newUserCouponCommand("CMD.A.19-24", "coupon.user_coupon.expire", []string{input.UserCouponID}, input, metadata)
	if err != nil {
		return usercoupon.Mutation{}, err
	}
	if _, consuming, err := s.deps.Redemptions.FindConsumingByUserCoupon(ctx, input.UserCouponID); err != nil {
		return usercoupon.Mutation{}, err
	} else if consuming {
		return usercoupon.Mutation{}, inputError("coupon.user_coupon_consuming", "reserved, confirmed, or reclaimed coupon use must be resolved before expiration")
	}
	return s.deps.UserCoupons.Expire(ctx, input.UserCouponID, input.ExpectedVersion, input.AsOf, command)
}

func (s *Service) FinalizeRecovery(ctx context.Context, input FinalizeRecoveryInput, metadata Metadata) (recovery.Recovery, error) {
	if strings.TrimSpace(input.RecoveryID) == "" || strings.TrimSpace(input.ReasonCode) == "" {
		return recovery.Recovery{}, inputError("coupon.recovery_finalization_invalid", "recovery and final reason are required")
	}
	if err := s.verifyApprovedTask(ctx, input.OperationTaskRef, input.ApprovalRef); err != nil {
		return recovery.Recovery{}, err
	}
	current, err := s.deps.Recoveries.Find(ctx, input.RecoveryID)
	if err != nil {
		return recovery.Recovery{}, err
	}
	command, err := newCommand("CMD.A.19-25", "coupon.recovery.finalize", []string{
		input.RecoveryID, input.OperationTaskRef.ID,
	}, input, metadata)
	if err != nil {
		return recovery.Recovery{}, err
	}
	return s.deps.Recoveries.Finalize(ctx, input.RecoveryID, recovery.Finalization{
		ExpectedVersion: current.Version, ReasonCode: input.ReasonCode,
		OperationRequestRef: input.OperationTaskRef.ID, ApprovalRef: input.ApprovalRef,
		FinalizedAt: metadata.RequestedAt,
	}, command)
}

func (s *Service) ApplyReadOnlyNotice(ctx context.Context, input ApplyReadOnlyNoticeInput, metadata Metadata) (domainoperations.Control, error) {
	if strings.TrimSpace(input.ControlID) == "" || input.ExpectedVersion < 0 {
		return domainoperations.Control{}, inputError("coupon.notice_input_invalid", "control id and expected version are required")
	}
	if err := s.verifyApprovedTask(ctx, input.OperationTaskRef, input.ApprovalRef); err != nil {
		return domainoperations.Control{}, err
	}
	command, err := newCommand("CMD.A.19-31", "coupon.operational_control.apply_notice", []string{input.ControlID}, input, metadata)
	if err != nil {
		return domainoperations.Control{}, err
	}
	return s.deps.Controls.ApplyNotice(ctx, input.ControlID, domainoperations.NoticeUpdate{
		ExpectedVersion: input.ExpectedVersion, Message: input.Message,
		EffectiveFrom: input.EffectiveFrom, Active: input.Active, AppliedAt: metadata.RequestedAt,
	}, command)
}

func (s *Service) RecordRecoveryResult(ctx context.Context, input RecordRecoveryResultInput, metadata Metadata) (recovery.Recovery, error) {
	if strings.TrimSpace(input.RecoveryID) == "" || strings.TrimSpace(input.AttemptID) == "" ||
		strings.TrimSpace(input.BusinessKey) == "" {
		return recovery.Recovery{}, inputError("coupon.recovery_result_input_invalid", "recovery result correlation is required")
	}
	recordedAt := input.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = metadata.RequestedAt
	}
	current, err := s.deps.Recoveries.Find(ctx, input.RecoveryID)
	if err != nil {
		return recovery.Recovery{}, err
	}
	if current.CurrentAttemptID != input.AttemptID || current.BusinessKey != input.BusinessKey ||
		current.CurrentAttempt == nil || current.CurrentAttempt.ID != input.AttemptID || current.CurrentAttempt.BusinessKey != input.BusinessKey {
		return recovery.Recovery{}, inputError("coupon.recovery_correlation_mismatch", "recovery result does not match the current version, attempt, and business key")
	}
	command, err := newCommand("CMD.A.19-33", "coupon.recovery.record_result", []string{
		input.RecoveryID, input.AttemptID, input.BusinessKey,
	}, input, metadata)
	if err != nil {
		return recovery.Recovery{}, err
	}
	return s.deps.Recoveries.RecordResult(ctx, input.RecoveryID, recovery.ReplayResult{
		ExpectedVersion: current.Version, AttemptID: input.AttemptID, BusinessKey: input.BusinessKey,
		Kind: input.Kind, ResultRef: input.ResultRef, FailureCode: input.FailureCode,
		Retryable: input.Retryable, NextAttemptAt: input.NextAttemptAt, RecordedAt: recordedAt,
	}, command)
}

func (s *Service) RecordProcessingFailure(ctx context.Context, input RecordProcessingFailureInput, metadata Metadata) (recovery.Recovery, error) {
	if !strings.HasPrefix(input.RedemptionID, "redm_") || strings.TrimSpace(input.OriginalPayloadRef) == "" || !strings.HasPrefix(input.OriginalPayloadHash, "sha256:") ||
		len(strings.TrimPrefix(input.OriginalPayloadHash, "sha256:")) == 0 || strings.TrimSpace(input.BusinessKey) == "" ||
		strings.TrimSpace(input.FailureCode) == "" || input.OccurredAt.IsZero() {
		return recovery.Recovery{}, inputError("coupon.processing_failure_input_invalid", "redemption, immutable payload reference, hash, business key, failure code, and time are required")
	}
	command, err := newCommand("CMD.A.19-34", "coupon.recovery.record_failure", []string{
		input.RedemptionID, input.BusinessKey, input.OriginalPayloadHash,
	}, input, metadata)
	if err != nil {
		return recovery.Recovery{}, err
	}
	recorded, domainEvent, err := recovery.RecordFailure(recovery.Failure{
		RecoveryID: stableID("rcvy", command.BusinessKey), RedemptionID: input.RedemptionID, OriginalOperationType: input.OriginalOperationType,
		OriginalPayloadRef: input.OriginalPayloadRef, OriginalPayloadHash: input.OriginalPayloadHash,
		BusinessKey: input.BusinessKey, FailureCode: input.FailureCode,
		NextAttemptAt: input.NextAttemptAt, OccurredAt: input.OccurredAt,
	})
	if err != nil {
		return recovery.Recovery{}, err
	}
	return s.deps.Recoveries.RecordFailure(ctx, recorded, domainEvent, command)
}

func (s *Service) verifyApprovedTask(ctx context.Context, task shared.ExternalRef, approvalRef string) error {
	if err := task.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(approvalRef) == "" {
		return inputError("coupon.approval_ref_required", "operation approval reference is required")
	}
	if err := s.deps.Approvals.VerifyApproval(ctx, approvalRef, task.ID); err != nil {
		return oops.In("coupon_operations_application").Code("coupon.operation_approval_verification_failed").Wrap(err)
	}
	if task.Context == "cs" {
		if err := s.deps.Cases.VerifyCase(ctx, task.ID, ports.CSCaseBinding{OperationTaskRef: task.ID}); err != nil {
			return oops.In("coupon_operations_application").Code("coupon.cs_case_verification_failed").Wrap(err)
		}
	}
	return nil
}

func newCommand(documentID, operation string, scope []string, request any, metadata Metadata) (reliability.Command, error) {
	hash, businessKey, err := commandIdentity(scope, request, metadata)
	if err != nil {
		return reliability.Command{}, err
	}
	return reliability.Command{
		DocumentID: documentID, OperationType: operation, BusinessKey: businessKey,
		RequestHash: hash, CorrelationID: metadata.CorrelationID, CausationID: metadata.CausationID,
		TraceID: metadata.TraceID, LeaseUntil: metadata.LeaseUntil, ExpiresAt: metadata.ExpiresAt,
	}, nil
}

func newUserCouponCommand(_ string, operation string, scope []string, request any, metadata Metadata) (usercoupon.Command, error) {
	hash, businessKey, err := commandIdentity(scope, request, metadata)
	if err != nil {
		return usercoupon.Command{}, err
	}
	return usercoupon.Command{
		OperationType: operation, BusinessKey: businessKey, RequestHash: hex.EncodeToString(hash[:]),
		CorrelationID: metadata.CorrelationID, CausationID: metadata.CausationID,
		TraceID: metadata.TraceID, OccurredAt: metadata.RequestedAt,
		LeaseUntil: metadata.LeaseUntil, ExpiresAt: metadata.ExpiresAt,
	}, nil
}

func commandIdentity(scope []string, request any, metadata Metadata) ([32]byte, string, error) {
	if metadata.RequestedAt.IsZero() || metadata.LeaseUntil.IsZero() || metadata.ExpiresAt.IsZero() ||
		!metadata.LeaseUntil.After(metadata.RequestedAt) || !metadata.ExpiresAt.After(metadata.LeaseUntil) {
		return [32]byte{}, "", inputError("coupon.idempotency_metadata_invalid", "request time, lease deadline, and expiry deadline are required in order")
	}
	if strings.TrimSpace(metadata.BusinessKey) == "" && strings.TrimSpace(metadata.IdempotencyKey) == "" {
		return [32]byte{}, "", inputError("coupon.idempotency_key_required", "idempotency key or explicit business key is required")
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return [32]byte{}, "", oops.In("coupon_operations_application").Code("coupon.request_hash_failed").Wrap(err)
	}
	businessKey := metadata.BusinessKey
	if businessKey == "" {
		parts := append(append([]string(nil), scope...), metadata.IdempotencyKey)
		businessKey = strings.Join(parts, "|")
	}
	return sha256.Sum256(requestJSON), businessKey, nil
}

func stableID(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(digest[:12])
}

func inputError(code, message string) error {
	return oops.In("coupon_operations_application").Code(code).New(message)
}
