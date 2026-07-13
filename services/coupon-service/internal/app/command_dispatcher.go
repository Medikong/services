package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	campaignapp "github.com/Medikong/services/services/coupon-service/internal/application/campaign"
	issuanceapp "github.com/Medikong/services/services/coupon-service/internal/application/issuance"
	operationsapp "github.com/Medikong/services/services/coupon-service/internal/application/operations"
	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	redemptionapp "github.com/Medikong/services/services/coupon-service/internal/application/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/couponcode"
	domaineventing "github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type QuantityAdmissionGate interface {
	Admit(context.Context, string, string, int64, int64) (campaign.GateResult, error)
	Complete(context.Context, string, string, int64) (campaign.GateResult, error)
	Compensate(context.Context, string, string, int64) (campaign.GateResult, error)
}

type QuantityGateFailureHook func(operation string, err error)

type QuantityGateOptions struct {
	Gate        QuantityAdmissionGate
	FailureMode config.RedisFailureMode
	FailureHook QuantityGateFailureHook
}

type campaignCommandService interface {
	ReserveQuantity(context.Context, campaignapp.ReserveQuantityInput) (campaignapp.QuantityResult, error)
	ConfirmQuantity(context.Context, campaignapp.DecideQuantityInput) (campaignapp.QuantityResult, error)
	ReleaseQuantity(context.Context, campaignapp.DecideQuantityInput) (campaignapp.QuantityResult, error)
}

type issuanceCommandService interface {
	CreateIssueRequest(context.Context, issuanceapp.CreateIssueRequestInput) (issuanceapp.IssueRequestResult, error)
	IssueUserCoupon(context.Context, issuanceapp.IssueUserCouponInput) (issuanceapp.UserCouponResult, error)
	RecordFailure(context.Context, issuanceapp.RecordFailureInput) (issuanceapp.IssueRequestResult, error)
	ConfirmCode(context.Context, issuanceapp.ConfirmCodeInput) (issuanceapp.CodeResult, error)
	ReleaseCode(context.Context, issuanceapp.ReleaseCodeInput) (issuanceapp.CodeResult, error)
	RetryIssue(context.Context, issuanceapp.RetryIssueInput) (issuanceapp.IssueRequestResult, error)
	FinalizeFailure(context.Context, issuanceapp.FinalizeFailureInput) (issuanceapp.IssueRequestResult, error)
	RecordSuccess(context.Context, issuanceapp.RecordSuccessInput) (issuanceapp.IssueRequestResult, error)
	Reject(context.Context, issuanceapp.RejectInput) (issuanceapp.IssueRequestResult, error)
	MarkPending(context.Context, issuanceapp.MarkPendingInput) (issuanceapp.IssueRequestResult, error)
}

type redemptionCommandService interface {
	Replay(context.Context, redemptionapp.ReplayInput, redemptionapp.Metadata) (redemptionapp.RecoveryResultCommand, error)
}

type operationsCommandService interface {
	AggregateBulkResult(context.Context, operationsapp.AggregateBulkResultInput, operationsapp.Metadata) (bulk.Job, error)
	ExpireUserCoupon(context.Context, operationsapp.ExpireUserCouponInput, operationsapp.Metadata) (usercoupon.Mutation, error)
	RecordRecoveryResult(context.Context, operationsapp.RecordRecoveryResultInput, operationsapp.Metadata) (recovery.Recovery, error)
	RecordProcessingFailure(context.Context, operationsapp.RecordProcessingFailureInput, operationsapp.Metadata) (recovery.Recovery, error)
}

type quantityReservationReader interface {
	FindQuantityReservation(context.Context, string, string) (campaign.QuantityReservation, bool, error)
}

type codeCorrelationReader interface {
	FindByIDWithBatchVersion(context.Context, string) (couponcode.Code, int64, error)
}

type bulkResultReader interface {
	HasResultRef(context.Context, string, string) (bool, error)
}

type campaignStateReader interface {
	Get(context.Context, string) (campaign.Campaign, error)
}

type issueStateReader interface {
	Get(context.Context, string) (issuerequest.Request, error)
}

type userCouponStateReader interface {
	Get(context.Context, string) (usercoupon.Coupon, error)
}

type bulkStateReader interface {
	Find(context.Context, string) (bulk.Job, error)
}

type recoveryStateReader interface {
	Find(context.Context, string) (recovery.Recovery, error)
}

type commandDispatcher struct {
	campaigns       campaignCommandService
	issuance        issuanceCommandService
	redemptions     redemptionCommandService
	operations      operationsCommandService
	campaignReader  campaignStateReader
	issueReader     issueStateReader
	codeReader      codeCorrelationReader
	userCouponRead  userCouponStateReader
	bulkReader      bulkStateReader
	bulkResults     bulkResultReader
	recoveryReader  recoveryStateReader
	reservations    quantityReservationReader
	commandLease    time.Duration
	idempotencyTTL  time.Duration
	quantityOptions QuantityGateOptions
	now             func() time.Time
}

type commandHandler func(context.Context, domaineventing.CommandRequest, decodedCommandPayload, dispatchMetadata) (string, error)

func newCommandDispatcher(parts components, domainPolicy config.DomainPolicyConfig, quantity QuantityGateOptions) (*commandDispatcher, error) {
	if domainPolicy.CommandLease <= 0 || domainPolicy.IdempotencyTTL <= domainPolicy.CommandLease {
		return nil, dispatcherError("coupon.command_dispatcher_policy_invalid", "command lease and a longer idempotency ttl are required")
	}
	reservations, ok := parts.campaignRepo.(quantityReservationReader)
	if !ok {
		return nil, dispatcherError("coupon.command_dispatcher_repository_invalid", "campaign repository does not expose quantity correlation")
	}
	codes, ok := parts.codeRepo.(codeCorrelationReader)
	if !ok {
		return nil, dispatcherError("coupon.command_dispatcher_repository_invalid", "coupon code repository does not expose issue correlation")
	}
	bulkResults, ok := parts.bulkRepo.(bulkResultReader)
	if !ok {
		return nil, dispatcherError("coupon.command_dispatcher_repository_invalid", "bulk repository does not expose result correlation")
	}
	if quantity.Gate != nil && quantity.FailureMode != config.RedisFailureDBFallback && quantity.FailureMode != config.RedisFailureClosed {
		return nil, dispatcherError("coupon.command_dispatcher_gate_mode_invalid", "quantity gate requires an explicit failure mode")
	}
	return &commandDispatcher{
		campaigns: parts.campaigns, issuance: parts.issuance, redemptions: parts.redemptions, operations: parts.operations,
		campaignReader: parts.campaignRepo, issueReader: parts.issueRepo, codeReader: codes,
		userCouponRead: parts.userCouponRepo, bulkReader: parts.bulkRepo, bulkResults: bulkResults,
		recoveryReader: parts.recoveryRepo, reservations: reservations,
		commandLease: domainPolicy.CommandLease, idempotencyTTL: domainPolicy.IdempotencyTTL,
		quantityOptions: quantity, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

func (d *commandDispatcher) Dispatch(ctx context.Context, request domaineventing.CommandRequest) (string, error) {
	if d == nil {
		return "", dispatcherError("coupon.command_dispatcher_required", "command dispatcher is required")
	}
	payload, err := decodeCommandPayload(request.Payload)
	if err != nil {
		return "", err
	}
	metadata, err := d.metadata(request, payload)
	if err != nil {
		return "", err
	}
	handler, ok := d.handlers()[request.CommandDocumentID]
	if !ok {
		return "", oops.In("coupon_command_dispatcher").Code("coupon.command_unsupported").With("command_document_id", request.CommandDocumentID).New("durable command is not supported")
	}
	return handler(ctx, request, payload, metadata)
}

func (d *commandDispatcher) handlers() map[string]commandHandler {
	return map[string]commandHandler{
		"CMD.A.19-07": d.issueUserCoupon,
		"CMD.A.19-13": d.createIssueRequest,
		"CMD.A.19-14": d.recordIssueFailure,
		"CMD.A.19-16": d.confirmCode,
		"CMD.A.19-17": d.releaseCode,
		"CMD.A.19-18": d.aggregateBulkResult,
		"CMD.A.19-19": d.retryIssue,
		"CMD.A.19-22": d.finalizeIssueFailure,
		"CMD.A.19-23": d.recordIssueSuccess,
		"CMD.A.19-24": d.expireUserCoupon,
		"CMD.A.19-26": d.reserveQuantity,
		"CMD.A.19-27": d.confirmQuantity,
		"CMD.A.19-28": d.releaseQuantity,
		"CMD.A.19-29": d.rejectIssue,
		"CMD.A.19-30": d.markIssuePending,
		"CMD.A.19-32": d.replayRedemption,
		"CMD.A.19-33": d.recordRecoveryResult,
		"CMD.A.19-34": d.recordProcessingFailure,
	}
}

func commandDispatcherDocumentIDs() []string {
	// This is the concrete handler registry used by Dispatch, not a separate
	// documentation-only count.
	d := &commandDispatcher{}
	ids := make([]string, 0, len(d.handlers()))
	for id := range d.handlers() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

type dispatchMetadata struct {
	commandID     string
	businessKey   string
	correlationID string
	causationID   string
	traceID       string
	requestedAt   time.Time
	leaseUntil    time.Time
	expiresAt     time.Time
}

func (d *commandDispatcher) metadata(request domaineventing.CommandRequest, payload decodedCommandPayload) (dispatchMetadata, error) {
	if request.ID == uuid.Nil || strings.TrimSpace(request.CommandDocumentID) == "" || strings.TrimSpace(request.BusinessKey) == "" || strings.TrimSpace(request.CorrelationID) == "" {
		return dispatchMetadata{}, dispatcherError("coupon.command_request_invalid", "command request identity, business key, and correlation are required")
	}
	causationID := strings.TrimSpace(request.CausationID)
	if request.SourceEventID != nil {
		causationID = request.SourceEventID.String()
	}
	if payload.envelope != nil {
		if request.SourceEventID != nil && *request.SourceEventID != payload.envelope.EventID {
			return dispatchMetadata{}, dispatcherError("coupon.command_source_event_mismatch", "queued source event does not match the immutable policy envelope")
		}
		causationID = payload.envelope.EventID.String()
		if payload.envelope.CorrelationID != "" && payload.envelope.CorrelationID != request.CorrelationID {
			return dispatchMetadata{}, dispatcherError("coupon.command_correlation_mismatch", "queued correlation does not match the immutable policy envelope")
		}
	}
	if causationID == "" {
		causationID = request.ID.String()
	}
	now := d.now().UTC()
	return dispatchMetadata{
		commandID: request.CommandDocumentID, businessKey: request.BusinessKey,
		correlationID: request.CorrelationID, causationID: causationID, traceID: firstValue(request.TraceID, request.CorrelationID),
		requestedAt: now, leaseUntil: now.Add(d.commandLease), expiresAt: now.Add(d.idempotencyTTL),
	}, nil
}

func (m dispatchMetadata) campaign(approvalRef string) campaignapp.CommandMetadata {
	return campaignapp.CommandMetadata{
		CommandID: m.commandID, BusinessKey: m.businessKey, CorrelationID: m.correlationID,
		CausationID: m.causationID, TraceID: m.traceID, ApprovalRef: approvalRef,
		OccurredAt: m.requestedAt, LeaseUntil: m.leaseUntil, ExpiresAt: m.expiresAt,
	}
}

func (m dispatchMetadata) issuance(approvalRef string) issuanceapp.CommandMetadata {
	return issuanceapp.CommandMetadata{
		CommandID: m.commandID, BusinessKey: m.businessKey, CorrelationID: m.correlationID,
		CausationID: m.causationID, TraceID: m.traceID, ApprovalRef: approvalRef,
		OccurredAt: m.requestedAt, LeaseUntil: m.leaseUntil, ExpiresAt: m.expiresAt,
	}
}

func (m dispatchMetadata) redemption() redemptionapp.Metadata {
	return redemptionapp.Metadata{
		BusinessKey: m.businessKey, CorrelationID: m.correlationID, CausationID: m.causationID,
		TraceID:     m.traceID,
		RequestedAt: m.requestedAt, LeaseUntil: m.leaseUntil, ExpiresAt: m.expiresAt,
	}
}

func (m dispatchMetadata) operations() operationsapp.Metadata {
	return operationsapp.Metadata{
		BusinessKey: m.businessKey, CorrelationID: m.correlationID, CausationID: m.causationID,
		TraceID: m.traceID, RequestedAt: m.requestedAt, LeaseUntil: m.leaseUntil, ExpiresAt: m.expiresAt,
	}
}

type durableCommandData struct {
	IssueRequestID              string                  `json:"issueRequestId"`
	CampaignID                  string                  `json:"campaignId"`
	UserID                      string                  `json:"userId"`
	UserCouponID                string                  `json:"userCouponId"`
	CodeID                      string                  `json:"codeId"`
	BulkJobID                   string                  `json:"bulkJobId"`
	RecoveryID                  string                  `json:"recoveryId"`
	RedemptionID                string                  `json:"redemptionId"`
	AttemptID                   string                  `json:"attemptId"`
	BusinessKey                 string                  `json:"businessKey"`
	SourceType                  issuerequest.SourceType `json:"sourceType"`
	SourceRef                   string                  `json:"sourceRef"`
	ReasonCode                  string                  `json:"reasonCode"`
	FailureCode                 string                  `json:"failureCode"`
	FailureResultRef            string                  `json:"failureResultRef"`
	SourceResultRef             string                  `json:"sourceResultRef"`
	ReservationResultRef        string                  `json:"reservationResultRef"`
	ResultRef                   string                  `json:"resultRef"`
	ApprovalRef                 string                  `json:"approvalRef"`
	CaseRef                     string                  `json:"caseRef"`
	OriginalOperationType       recovery.OperationType  `json:"originalOperationType"`
	OriginalPayloadRef          string                  `json:"originalPayloadRef"`
	OriginalPayloadHash         string                  `json:"originalPayloadHash"`
	ResultKind                  recovery.ResultKind     `json:"resultKind"`
	Kind                        recovery.ResultKind     `json:"kind"`
	ExpectedIssueRequestVersion *int64                  `json:"expectedIssueRequestVersion"`
	ExpectedBatchVersion        *int64                  `json:"expectedBatchVersion"`
	ExpectedVersion             *int64                  `json:"expectedVersion"`
	Version                     *int64                  `json:"version"`
	Quantity                    *int64                  `json:"quantity"`
	TargetCount                 *int64                  `json:"targetCount"`
	SucceededCount              *int64                  `json:"succeededCount"`
	RejectedCount               *int64                  `json:"rejectedCount"`
	FailedCount                 *int64                  `json:"failedCount"`
	Retryable                   *bool                   `json:"retryable"`
	Final                       *bool                   `json:"final"`
	NextAttemptAt               *time.Time              `json:"nextAttemptAt"`
	RecordedAt                  *time.Time              `json:"recordedAt"`
	OccurredAt                  *time.Time              `json:"occurredAt"`
	AsOf                        *time.Time              `json:"asOf"`
}

type decodedCommandPayload struct {
	data     durableCommandData
	envelope *policy.Envelope
}

func decodeCommandPayload(raw json.RawMessage) (decodedCommandPayload, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return decodedCommandPayload{}, dispatcherError("coupon.command_payload_invalid", "durable command payload must be valid json")
	}
	var probe struct {
		EventDocumentID string `json:"event_document_id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return decodedCommandPayload{}, dispatcherWrap("coupon.command_payload_invalid", "durable command payload cannot be decoded", err)
	}
	dataJSON := raw
	var envelope *policy.Envelope
	if probe.EventDocumentID != "" {
		var value policy.Envelope
		if err := json.Unmarshal(raw, &value); err != nil {
			return decodedCommandPayload{}, dispatcherWrap("coupon.command_envelope_invalid", "policy envelope cannot be decoded", err)
		}
		if value.EventID == uuid.Nil || value.EventDocumentID == "" || value.PayloadSchemaVersion != 1 || value.Data == nil {
			return decodedCommandPayload{}, dispatcherError("coupon.command_envelope_invalid", "policy envelope identity, schema, and data are required")
		}
		encoded, err := json.Marshal(value.Data)
		if err != nil {
			return decodedCommandPayload{}, dispatcherWrap("coupon.command_envelope_invalid", "policy envelope data cannot be encoded", err)
		}
		dataJSON = encoded
		envelope = &value
	}
	normalized, err := normalizeCommandData(dataJSON)
	if err != nil {
		return decodedCommandPayload{}, err
	}
	var data durableCommandData
	if err := json.Unmarshal(normalized, &data); err != nil {
		return decodedCommandPayload{}, dispatcherWrap("coupon.command_data_invalid", "command data cannot be decoded", err)
	}
	return decodedCommandPayload{data: data, envelope: envelope}, nil
}

func normalizeCommandData(raw []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, dispatcherWrap("coupon.command_data_invalid", "command data must be a json object", err)
	}
	for name, value := range fields {
		camel := lowerCamelCommandField(name)
		if _, exists := fields[camel]; !exists {
			fields[camel] = value
		}
	}
	result, err := json.Marshal(fields)
	if err != nil {
		return nil, dispatcherWrap("coupon.command_data_invalid", "normalized command data cannot be encoded", err)
	}
	return result, nil
}

func lowerCamelCommandField(value string) string {
	parts := strings.Split(value, "_")
	for index := 1; index < len(parts); index++ {
		if parts[index] != "" {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, "")
}

func (d *commandDispatcher) issueUserCoupon(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	version, err := d.issueVersion(ctx, data.IssueRequestID, data.ExpectedIssueRequestVersion, data.Version)
	if err != nil {
		return "", err
	}
	result, err := d.issuance.IssueUserCoupon(ctx, issuanceapp.IssueUserCouponInput{
		Metadata: metadata.issuance(""), IssueRequestID: data.IssueRequestID, ExpectedIssueRequestVersion: version,
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) createIssueRequest(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	if payload.eventDocumentID() == "EVT.A.19-12" {
		if data.CodeID == "" {
			return "", missingField("code_id")
		}
		data.SourceType = issuerequest.SourceRedeemCode
		data.SourceRef = data.CodeID
	}
	if data.CampaignID == "" || data.UserID == "" || data.SourceType == "" || data.SourceRef == "" {
		return "", dispatcherError("coupon.command_correlation_missing", "issue request, campaign, user, source type, and source reference are required")
	}
	result, err := d.issuance.CreateIssueRequest(ctx, issuanceapp.CreateIssueRequestInput{
		Metadata: metadata.issuance(data.ApprovalRef), IssueRequestID: data.IssueRequestID,
		CampaignID: data.CampaignID, UserID: data.UserID, SourceType: data.SourceType,
		SourceRef: data.SourceRef, ReasonCode: data.ReasonCode, CaseRef: data.CaseRef,
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) recordIssueFailure(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	version, err := d.issueVersion(ctx, data.IssueRequestID, data.ExpectedVersion, data.Version)
	if err != nil {
		return "", err
	}
	retryable := data.Retryable != nil && *data.Retryable
	resultRef := firstValue(data.FailureResultRef, data.ResultRef)
	result, err := d.issuance.RecordFailure(ctx, issuanceapp.RecordFailureInput{
		Metadata: metadata.issuance(""), IssueRequestID: data.IssueRequestID, ExpectedVersion: version,
		FailureCode: data.FailureCode, FailureResultRef: resultRef, Retryable: retryable, NextAttemptAt: data.NextAttemptAt,
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) confirmCode(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	issue, code, batchVersion, skipped, err := d.codeCorrelation(ctx, data)
	if err != nil || skipped {
		return skippedResult(request.CommandDocumentID, data.IssueRequestID), err
	}
	if code.Status == couponcode.CodeRedeemed && code.RedeemedUserCouponID == data.UserCouponID {
		return "code:" + code.ID + ":redeemed", nil
	}
	if code.Status != couponcode.CodeReserved || code.ReservedIssueRequestID != issue.ID {
		return "", dispatcherError("coupon.code_correlation_mismatch", "reserved code does not match the issue request")
	}
	if data.ExpectedBatchVersion != nil {
		batchVersion = *data.ExpectedBatchVersion
	}
	result, err := d.issuance.ConfirmCode(ctx, issuanceapp.ConfirmCodeInput{
		Metadata: metadata.issuance(""), CodeID: code.ID, IssueRequestID: issue.ID,
		UserCouponID: data.UserCouponID, ExpectedBatchVersion: batchVersion,
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) releaseCode(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	issue, code, batchVersion, skipped, err := d.codeCorrelation(ctx, data)
	if err != nil || skipped {
		return skippedResult(request.CommandDocumentID, data.IssueRequestID), err
	}
	if code.Status == couponcode.CodeAvailable {
		return "code:" + code.ID + ":released", nil
	}
	if code.Status != couponcode.CodeReserved || code.ReservedIssueRequestID != issue.ID {
		return "", dispatcherError("coupon.code_correlation_mismatch", "reserved code does not match the issue request")
	}
	if data.ExpectedBatchVersion != nil {
		batchVersion = *data.ExpectedBatchVersion
	}
	result, err := d.issuance.ReleaseCode(ctx, issuanceapp.ReleaseCodeInput{
		Metadata: metadata.issuance(""), CodeID: code.ID, IssueRequestID: issue.ID,
		FailureResultRef: firstValue(data.FailureResultRef, data.ResultRef, issue.ResultRef), ExpectedBatchVersion: batchVersion,
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) aggregateBulkResult(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	eventID := payload.eventDocumentID()
	if eventID != "" {
		if data.IssueRequestID == "" {
			return "", missingField("issue_request_id")
		}
		if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
			return "", err
		}
		issue, err := d.issueReader.Get(ctx, data.IssueRequestID)
		if err != nil {
			return "", err
		}
		if issue.SourceType != issuerequest.SourceBulk {
			return skippedResult(request.CommandDocumentID, issue.ID), nil
		}
		bulkJobID, err := bulkJobFromSource(issue.SourceRef)
		if err != nil {
			return "", err
		}
		if data.BulkJobID != "" && data.BulkJobID != bulkJobID {
			return "", dispatcherError("coupon.bulk_correlation_mismatch", "event bulk job does not match the issue source")
		}
		data.BulkJobID = bulkJobID
		data.ResultRef = firstValue(data.ResultRef, issue.ResultRef)
		one := int64(1)
		zero := int64(0)
		data.SucceededCount, data.RejectedCount, data.FailedCount = &zero, &zero, &zero
		switch eventID {
		case "EVT.A.19-09":
			data.SucceededCount = &one
		case "EVT.A.19-08":
			data.RejectedCount = &one
		case "EVT.A.19-11":
			data.FailedCount = &one
		default:
			return "", dispatcherError("coupon.bulk_source_event_invalid", "bulk result command source event is not terminal")
		}
	}
	if data.BulkJobID == "" || data.ResultRef == "" {
		return "", dispatcherError("coupon.bulk_correlation_missing", "bulk job and terminal result reference are required")
	}
	processed, err := d.bulkResults.HasResultRef(ctx, data.BulkJobID, data.ResultRef)
	if err != nil {
		return "", err
	}
	if processed {
		return data.ResultRef, nil
	}
	job, err := d.bulkReader.Find(ctx, data.BulkJobID)
	if err != nil {
		return "", err
	}
	recordedAt := metadata.requestedAt
	if data.RecordedAt != nil {
		recordedAt = data.RecordedAt.UTC()
	} else if payload.envelope != nil {
		recordedAt = payload.envelope.OccurredAt.UTC()
	}
	result, err := d.operations.AggregateBulkResult(ctx, operationsapp.AggregateBulkResultInput{
		BulkJobID: data.BulkJobID, ExpectedVersion: job.Version, TargetCount: data.TargetCount,
		SucceededCount: valueOrZero(data.SucceededCount), RejectedCount: valueOrZero(data.RejectedCount),
		FailedCount: valueOrZero(data.FailedCount), ResultRef: data.ResultRef,
		Final: data.Final != nil && *data.Final, RecordedAt: recordedAt,
	}, metadata.operations())
	if err != nil {
		return "", err
	}
	return firstValue(data.ResultRef, result.ID), nil
}

func (d *commandDispatcher) retryIssue(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	current, err := d.issueReader.Get(ctx, data.IssueRequestID)
	if err != nil {
		return "", err
	}
	if current.Status != issuerequest.StatusFailedRetryable {
		if current.Status == issuerequest.StatusRetryPending || current.Status == issuerequest.StatusProcessing || current.Status == issuerequest.StatusCompleted {
			return current.ResultRef, nil
		}
		return "", dispatcherError("coupon.issue_retry_state_invalid", "issue request is not retryable")
	}
	if data.NextAttemptAt == nil {
		return "", missingField("next_attempt_at")
	}
	result, err := d.issuance.RetryIssue(ctx, issuanceapp.RetryIssueInput{
		Metadata: metadata.issuance(""), IssueRequestID: current.ID, ExpectedVersion: current.Version, NextAttemptAt: *data.NextAttemptAt,
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) finalizeIssueFailure(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	current, err := d.issueReader.Get(ctx, data.IssueRequestID)
	if err != nil {
		return "", err
	}
	if current.Status == issuerequest.StatusFailedFinal {
		return current.ResultRef, nil
	}
	result, err := d.issuance.FinalizeFailure(ctx, issuanceapp.FinalizeFailureInput{
		Metadata: metadata.issuance(data.ApprovalRef), IssueRequestID: current.ID,
		ExpectedVersion: current.Version, FailureCode: data.FailureCode,
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) recordIssueSuccess(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	current, err := d.issueReader.Get(ctx, data.IssueRequestID)
	if err != nil {
		return "", err
	}
	if current.Status == issuerequest.StatusCompleted {
		if current.UserCouponID != data.UserCouponID {
			return "", dispatcherError("coupon.issue_result_mismatch", "completed issue request points to another user coupon")
		}
		return current.ResultRef, nil
	}
	result, err := d.issuance.RecordSuccess(ctx, issuanceapp.RecordSuccessInput{
		Metadata: metadata.issuance(""), IssueRequestID: current.ID,
		ExpectedVersion: current.Version, UserCouponID: data.UserCouponID,
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) expireUserCoupon(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.UserCouponID); err != nil {
		return "", err
	}
	current, err := d.userCouponRead.Get(ctx, data.UserCouponID)
	if err != nil {
		return "", err
	}
	if current.Status == usercoupon.StatusExpired {
		return current.ResultRef, nil
	}
	asOf := current.ExpiresAt
	if data.AsOf != nil {
		asOf = data.AsOf.UTC()
	}
	result, err := d.operations.ExpireUserCoupon(ctx, operationsapp.ExpireUserCouponInput{
		UserCouponID: current.ID, ExpectedVersion: current.Version, AsOf: asOf,
	}, metadata.operations())
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) reserveQuantity(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if data.CampaignID == "" || data.IssueRequestID == "" {
		return "", dispatcherError("coupon.quantity_correlation_missing", "campaign and issue request are required")
	}
	if request.AggregateID != "" && request.AggregateID != data.CampaignID {
		return "", dispatcherError("coupon.command_target_mismatch", "queued campaign target does not match event data")
	}
	quantity := int64(1)
	if data.Quantity != nil {
		quantity = *data.Quantity
	}
	current, err := d.campaignReader.Get(ctx, data.CampaignID)
	if err != nil {
		return "", err
	}
	existing, exists, err := d.reservations.FindQuantityReservation(ctx, data.CampaignID, data.IssueRequestID)
	if err != nil {
		return "", err
	}
	gateAdmitted, err := d.admitQuantity(ctx, current, data.IssueRequestID, quantity)
	if err != nil {
		return "", err
	}
	if exists {
		if existing.Quantity != quantity {
			return "", dispatcherError("coupon.quantity_correlation_mismatch", "existing quantity reservation has another amount")
		}
		if existing.State == campaign.ReservationRejected || existing.State == campaign.ReservationReleased {
			if err := d.compensateQuantity(ctx, data.CampaignID, data.IssueRequestID, quantity, gateAdmitted); err != nil {
				return "", err
			}
			return existing.ResultRef, nil
		}
		if err := d.completeQuantity(ctx, data.CampaignID, data.IssueRequestID, quantity, gateAdmitted); err != nil {
			return "", err
		}
		return existing.ResultRef, nil
	}
	result, reserveErr := d.campaigns.ReserveQuantity(ctx, campaignapp.ReserveQuantityInput{
		Metadata: metadata.campaign(""), CampaignID: data.CampaignID, IssueRequestID: data.IssueRequestID,
		Quantity: quantity, ExpectedVersion: current.Version,
	})
	if reserveErr != nil || result.Rejected {
		if err := d.compensateQuantity(ctx, data.CampaignID, data.IssueRequestID, quantity, gateAdmitted); err != nil {
			if reserveErr != nil {
				return "", oops.Join(reserveErr, err)
			}
			return "", err
		}
		if reserveErr != nil {
			return "", reserveErr
		}
		return result.ResultRef, nil
	}
	if err := d.completeQuantity(ctx, data.CampaignID, data.IssueRequestID, quantity, gateAdmitted); err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) confirmQuantity(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	return d.decideQuantity(ctx, request, payload.data, metadata, campaign.ReservationConfirmed)
}

func (d *commandDispatcher) releaseQuantity(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	return d.decideQuantity(ctx, request, payload.data, metadata, campaign.ReservationReleased)
}

func (d *commandDispatcher) decideQuantity(ctx context.Context, request domaineventing.CommandRequest, data durableCommandData, metadata dispatchMetadata, target campaign.ReservationState) (string, error) {
	if data.IssueRequestID == "" {
		return "", missingField("issue_request_id")
	}
	issue, err := d.issueReader.Get(ctx, data.IssueRequestID)
	if err != nil {
		return "", err
	}
	campaignID := firstValue(data.CampaignID, issue.CampaignID)
	if campaignID != issue.CampaignID {
		return "", dispatcherError("coupon.quantity_correlation_mismatch", "quantity event campaign does not match the issue request")
	}
	if request.AggregateID != "" && request.AggregateID != campaignID {
		return "", dispatcherError("coupon.command_target_mismatch", "queued campaign target does not match issue correlation")
	}
	reservation, exists, err := d.reservations.FindQuantityReservation(ctx, campaignID, issue.ID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", dispatcherError("coupon.quantity_reservation_missing", "quantity decision requires an existing reservation")
	}
	if reservation.State == target {
		return reservation.ResultRef, nil
	}
	if reservation.State != campaign.ReservationReserved {
		return "", dispatcherError("coupon.quantity_transition_conflict", "quantity reservation already has another terminal result")
	}
	current, err := d.campaignReader.Get(ctx, campaignID)
	if err != nil {
		return "", err
	}
	input := campaignapp.DecideQuantityInput{
		Metadata: metadata.campaign(""), CampaignID: campaignID, IssueRequestID: issue.ID, ExpectedVersion: current.Version,
	}
	var result campaignapp.QuantityResult
	if target == campaign.ReservationConfirmed {
		result, err = d.campaigns.ConfirmQuantity(ctx, input)
	} else {
		result, err = d.campaigns.ReleaseQuantity(ctx, input)
	}
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) rejectIssue(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	current, err := d.issueReader.Get(ctx, data.IssueRequestID)
	if err != nil {
		return "", err
	}
	if current.Status == issuerequest.StatusRejected {
		return current.ResultRef, nil
	}
	result, err := d.issuance.Reject(ctx, issuanceapp.RejectInput{
		Metadata: metadata.issuance(""), IssueRequestID: current.ID, ExpectedVersion: current.Version,
		ReasonCode: data.ReasonCode, SourceResultRef: firstValue(data.SourceResultRef, data.ResultRef),
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) markIssuePending(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.IssueRequestID); err != nil {
		return "", err
	}
	current, err := d.issueReader.Get(ctx, data.IssueRequestID)
	if err != nil {
		return "", err
	}
	if current.Status != issuerequest.StatusAccepted {
		if current.Status == issuerequest.StatusPending || current.Status == issuerequest.StatusProcessing || current.Status == issuerequest.StatusCompleted {
			return current.ResultRef, nil
		}
		return "", dispatcherError("coupon.issue_pending_state_invalid", "issue request cannot enter pending from its current state")
	}
	result, err := d.issuance.MarkPending(ctx, issuanceapp.MarkPendingInput{
		Metadata: metadata.issuance(""), IssueRequestID: current.ID, ExpectedVersion: current.Version,
		ReservationResultRef: firstValue(data.ReservationResultRef, data.ResultRef),
	})
	if err != nil {
		return "", err
	}
	return result.ResultRef, nil
}

func (d *commandDispatcher) replayRedemption(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if data.RecoveryID == "" || data.AttemptID == "" || data.BusinessKey == "" || data.RedemptionID == "" {
		return "", dispatcherError("coupon.recovery_correlation_missing", "recovery, attempt, business key, and redemption are required")
	}
	if request.AggregateID != "" && request.AggregateID != data.RedemptionID {
		return "", dispatcherError("coupon.command_target_mismatch", "queued redemption target does not match recovery data")
	}
	current, err := d.recoveryReader.Find(ctx, data.RecoveryID)
	if err != nil {
		return "", err
	}
	if current.CurrentAttemptID != data.AttemptID || current.BusinessKey != data.BusinessKey || current.RedemptionID != data.RedemptionID {
		return "", dispatcherError("coupon.recovery_correlation_mismatch", "recovery aggregate does not match the queued replay correlation")
	}
	if data.OriginalOperationType != "" && current.OriginalOperationType != data.OriginalOperationType {
		return "", dispatcherError("coupon.recovery_correlation_mismatch", "recovery operation does not match the queued replay")
	}
	if data.OriginalPayloadRef != "" && current.OriginalPayloadRef != data.OriginalPayloadRef {
		return "", dispatcherError("coupon.recovery_correlation_mismatch", "recovery payload reference does not match the queued replay")
	}
	result, err := d.redemptions.Replay(ctx, redemptionapp.ReplayInput{
		RecoveryID: current.ID, AttemptID: current.CurrentAttemptID, BusinessKey: current.BusinessKey,
		RedemptionID: current.RedemptionID, OriginalOperationType: current.OriginalOperationType,
		OriginalPayloadRef: current.OriginalPayloadRef, OriginalPayloadHash: current.OriginalPayloadHash,
	}, metadata.redemption())
	if err != nil {
		return "", err
	}
	return firstValue(result.ResultRef, fmt.Sprintf("recovery:%s:attempt:%s:%s", result.RecoveryID, result.AttemptID, result.Kind)), nil
}

func (d *commandDispatcher) recordRecoveryResult(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if data.RecoveryID == "" || data.AttemptID == "" || data.BusinessKey == "" || data.RedemptionID == "" {
		return "", dispatcherError("coupon.recovery_correlation_missing", "recovery result correlation is incomplete")
	}
	if request.AggregateID != "" && request.AggregateID != data.RecoveryID {
		return "", dispatcherError("coupon.command_target_mismatch", "queued recovery target does not match replay result")
	}
	current, err := d.recoveryReader.Find(ctx, data.RecoveryID)
	if err != nil {
		return "", err
	}
	if current.RedemptionID != data.RedemptionID || current.CurrentAttemptID != data.AttemptID || current.BusinessKey != data.BusinessKey {
		return "", dispatcherError("coupon.recovery_correlation_mismatch", "replay result does not match the current recovery attempt")
	}
	if current.Status == recovery.StatusCompleted || current.Status == recovery.StatusRetryFailed {
		if current.ResultKind != firstResultKind(data) {
			return "", dispatcherError("coupon.recovery_result_mismatch", "recovery already contains another result kind")
		}
		return firstValue(current.ResultRef, fmt.Sprintf("recovery:%s:attempt:%s:%s", current.ID, current.CurrentAttemptID, current.ResultKind)), nil
	}
	recordedAt := metadata.requestedAt
	if data.RecordedAt != nil {
		recordedAt = data.RecordedAt.UTC()
	} else if payload.envelope != nil {
		recordedAt = payload.envelope.OccurredAt.UTC()
	}
	kind := firstResultKind(data)
	result, err := d.operations.RecordRecoveryResult(ctx, operationsapp.RecordRecoveryResultInput{
		RecoveryID: data.RecoveryID, AttemptID: data.AttemptID, BusinessKey: data.BusinessKey,
		Kind: kind, ResultRef: data.ResultRef, FailureCode: data.FailureCode,
		Retryable: false, NextAttemptAt: nil, RecordedAt: recordedAt,
	}, metadata.operations())
	if err != nil {
		return "", err
	}
	return firstValue(result.ResultRef, fmt.Sprintf("recovery:%s:attempt:%s:%s", result.ID, data.AttemptID, kind)), nil
}

func (d *commandDispatcher) recordProcessingFailure(ctx context.Context, request domaineventing.CommandRequest, payload decodedCommandPayload, metadata dispatchMetadata) (string, error) {
	data := payload.data
	if err := requirePayloadTarget(request, data.RedemptionID); err != nil {
		return "", err
	}
	occurredAt := metadata.requestedAt
	if data.OccurredAt != nil {
		occurredAt = data.OccurredAt.UTC()
	}
	result, err := d.operations.RecordProcessingFailure(ctx, operationsapp.RecordProcessingFailureInput{
		RedemptionID: data.RedemptionID, OriginalOperationType: data.OriginalOperationType,
		OriginalPayloadRef: data.OriginalPayloadRef, OriginalPayloadHash: data.OriginalPayloadHash,
		BusinessKey: data.BusinessKey, FailureCode: data.FailureCode,
		NextAttemptAt: data.NextAttemptAt, OccurredAt: occurredAt,
	}, metadata.operations())
	if err != nil {
		return "", err
	}
	return result.ID, nil
}

func (d *commandDispatcher) issueVersion(ctx context.Context, issueRequestID string, preferred ...*int64) (int64, error) {
	for _, value := range preferred {
		if value != nil {
			return *value, nil
		}
	}
	current, err := d.issueReader.Get(ctx, issueRequestID)
	if err != nil {
		return 0, err
	}
	return current.Version, nil
}

func (d *commandDispatcher) codeCorrelation(ctx context.Context, data durableCommandData) (issuerequest.Request, couponcode.Code, int64, bool, error) {
	issue, err := d.issueReader.Get(ctx, data.IssueRequestID)
	if err != nil {
		return issuerequest.Request{}, couponcode.Code{}, 0, false, err
	}
	if issue.SourceType != issuerequest.SourceRedeemCode {
		return issue, couponcode.Code{}, 0, true, nil
	}
	codeID := firstValue(data.CodeID, issue.SourceRef)
	if codeID == "" || (data.CodeID != "" && issue.SourceRef != "" && data.CodeID != issue.SourceRef) {
		return issuerequest.Request{}, couponcode.Code{}, 0, false, dispatcherError("coupon.code_correlation_mismatch", "issue source and event code do not match")
	}
	code, batchVersion, err := d.codeReader.FindByIDWithBatchVersion(ctx, codeID)
	if err != nil {
		return issuerequest.Request{}, couponcode.Code{}, 0, false, err
	}
	if code.CampaignID != issue.CampaignID {
		return issuerequest.Request{}, couponcode.Code{}, 0, false, dispatcherError("coupon.code_correlation_mismatch", "coupon code campaign does not match the issue request")
	}
	return issue, code, batchVersion, false, nil
}

func (d *commandDispatcher) admitQuantity(ctx context.Context, current campaign.Campaign, issueRequestID string, quantity int64) (bool, error) {
	if d.quantityOptions.Gate == nil {
		return false, nil
	}
	result, err := d.quantityOptions.Gate.Admit(ctx, current.ID, issueRequestID, quantity, current.TotalQuantity)
	if err != nil {
		return false, d.quantityGateFailure("admit", err)
	}
	// A Redis rejection is only an admission hint. PostgreSQL still makes the
	// authoritative reserve/reject decision.
	return !result.Rejected(), nil
}

func (d *commandDispatcher) completeQuantity(ctx context.Context, campaignID, issueRequestID string, quantity int64, admitted bool) error {
	if !admitted || d.quantityOptions.Gate == nil {
		return nil
	}
	_, err := d.quantityOptions.Gate.Complete(ctx, campaignID, issueRequestID, quantity)
	if err != nil {
		return d.quantityGateFailure("complete", err)
	}
	return nil
}

func (d *commandDispatcher) compensateQuantity(ctx context.Context, campaignID, issueRequestID string, quantity int64, admitted bool) error {
	if !admitted || d.quantityOptions.Gate == nil {
		return nil
	}
	_, err := d.quantityOptions.Gate.Compensate(ctx, campaignID, issueRequestID, quantity)
	if err != nil {
		return d.quantityGateFailure("compensate", err)
	}
	return nil
}

func (d *commandDispatcher) quantityGateFailure(operation string, err error) error {
	if d.quantityOptions.FailureHook != nil {
		d.quantityOptions.FailureHook(operation, err)
	}
	if d.quantityOptions.FailureMode == config.RedisFailureDBFallback {
		return nil
	}
	return dispatcherWrap("coupon.quantity_gate_failed", "quantity gate operation failed in fail_closed mode", err)
}

func (p decodedCommandPayload) eventDocumentID() string {
	if p.envelope == nil {
		return ""
	}
	return p.envelope.EventDocumentID
}

func requirePayloadTarget(request domaineventing.CommandRequest, payloadID string) error {
	if strings.TrimSpace(payloadID) == "" {
		return missingField("target_id")
	}
	if request.AggregateID != "" && request.AggregateID != payloadID {
		return dispatcherError("coupon.command_target_mismatch", "queued target does not match immutable command data")
	}
	return nil
}

func bulkJobFromSource(sourceRef string) (string, error) {
	jobID, _, ok := strings.Cut(strings.TrimSpace(sourceRef), ":")
	if !ok || !strings.HasPrefix(jobID, "bjob_") {
		return "", dispatcherError("coupon.bulk_source_invalid", "bulk issue source must preserve bulk_job_id and target correlation")
	}
	return jobID, nil
}

func firstResultKind(data durableCommandData) recovery.ResultKind {
	if data.ResultKind != "" {
		return data.ResultKind
	}
	return data.Kind
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func firstValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func skippedResult(commandID, aggregateID string) string {
	return "skipped:" + commandID + ":" + aggregateID
}

func missingField(field string) error {
	return oops.In("coupon_command_dispatcher").Code("coupon.command_correlation_missing").With("field", field).New("durable command correlation field is missing")
}

func dispatcherError(code, message string) error {
	return oops.In("coupon_command_dispatcher").Code(code).New(message)
}

func dispatcherWrap(code, message string, err error) error {
	return oops.In("coupon_command_dispatcher").Code(code).With("detail", message).Wrap(err)
}

var _ interface {
	Dispatch(context.Context, domaineventing.CommandRequest) (string, error)
} = (*commandDispatcher)(nil)
