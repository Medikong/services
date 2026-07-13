package issuanceapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/couponcode"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
)

const (
	CommandClaim              = "CMD.A.19-05"
	CommandRedeemCode         = "CMD.A.19-06"
	CommandIssueUserCoupon    = "CMD.A.19-07"
	CommandCreateIssueRequest = "CMD.A.19-13"
	CommandRecordFailure      = "CMD.A.19-14"
	CommandConfirmCode        = "CMD.A.19-16"
	CommandReleaseCode        = "CMD.A.19-17"
	CommandRetryIssue         = "CMD.A.19-19"
	CommandFinalizeFailure    = "CMD.A.19-22"
	CommandRecordSuccess      = "CMD.A.19-23"
	CommandReject             = "CMD.A.19-29"
	CommandMarkPending        = "CMD.A.19-30"
)

type CommandMetadata struct {
	CommandID     string
	BusinessKey   string
	CorrelationID string
	CausationID   string
	TraceID       string
	ApprovalRef   string
	OccurredAt    time.Time
	LeaseUntil    time.Time
	ExpiresAt     time.Time
}

type CampaignReader interface {
	GetEffective(context.Context, string, time.Time) (campaign.Campaign, error)
}

type OperationalControlReader interface {
	FindEffective(context.Context, operations.Scope, time.Time) ([]operations.Control, error)
}

type Dependencies struct {
	Campaigns          CampaignReader
	IssueRequests      issuerequest.Repository
	Codes              couponcode.Repository
	UserCoupons        usercoupon.Repository
	Approvals          ports.OperationApprovalPort
	Cases              ports.CSCasePort
	UserEligibility    ports.UserEligibilityPort
	OperationalControl OperationalControlReader
	CodeHashKey        []byte
	CodeReservationTTL time.Duration
}

type Service struct {
	campaigns          CampaignReader
	issueRequests      issuerequest.Repository
	codes              couponcode.Repository
	userCoupons        usercoupon.Repository
	approvals          ports.OperationApprovalPort
	cases              ports.CSCasePort
	userEligibility    ports.UserEligibilityPort
	operationalControl OperationalControlReader
	codeHashKey        []byte
	codeReservationTTL time.Duration
}

func New(deps Dependencies) (*Service, error) {
	if deps.Campaigns == nil || deps.IssueRequests == nil || deps.Codes == nil || deps.UserCoupons == nil || deps.Approvals == nil || deps.Cases == nil || deps.UserEligibility == nil || deps.OperationalControl == nil {
		return nil, oops.In("coupon_issuance_application").Code("issuance.dependencies_required").New("issuance repositories and verification ports are required")
	}
	if len(deps.CodeHashKey) < 32 || deps.CodeReservationTTL <= 0 {
		return nil, oops.In("coupon_issuance_application").Code("issuance.code_policy_invalid").New("code hash key and reservation ttl are required")
	}
	return &Service{
		campaigns: deps.Campaigns, issueRequests: deps.IssueRequests, codes: deps.Codes,
		userCoupons: deps.UserCoupons, approvals: deps.Approvals, cases: deps.Cases,
		userEligibility: deps.UserEligibility, operationalControl: deps.OperationalControl,
		codeHashKey: append([]byte(nil), deps.CodeHashKey...), codeReservationTTL: deps.CodeReservationTTL,
	}, nil
}

type ClaimInput struct {
	Metadata   CommandMetadata
	CampaignID string
	UserID     string
}

type RedeemCodeInput struct {
	Metadata CommandMetadata
	UserID   string
	Code     string
}

type CreateIssueRequestInput struct {
	Metadata       CommandMetadata
	IssueRequestID string
	CampaignID     string
	UserID         string
	SourceType     issuerequest.SourceType
	SourceRef      string
	ReasonCode     string
	CaseRef        string
}

type CreateCompensationIssueRequestInput struct {
	Metadata       CommandMetadata
	CampaignID     string
	UserID         string
	SourceRef      shared.ExternalRef
	ReasonCode     string
	CaseRef        string
	ApprovalPolicy shared.SnapshotRef
}

type IssueUserCouponInput struct {
	Metadata                    CommandMetadata
	IssueRequestID              string
	ExpectedIssueRequestVersion int64
}

type RecordFailureInput struct {
	Metadata         CommandMetadata
	IssueRequestID   string
	ExpectedVersion  int64
	FailureCode      string
	FailureResultRef string
	Retryable        bool
	NextAttemptAt    *time.Time
}

type ConfirmCodeInput struct {
	Metadata             CommandMetadata
	CodeID               string
	IssueRequestID       string
	UserCouponID         string
	ExpectedBatchVersion int64
}

type ReleaseCodeInput struct {
	Metadata             CommandMetadata
	CodeID               string
	IssueRequestID       string
	FailureResultRef     string
	ExpectedBatchVersion int64
}

type RetryIssueInput struct {
	Metadata        CommandMetadata
	IssueRequestID  string
	ExpectedVersion int64
	NextAttemptAt   time.Time
}

type FinalizeFailureInput struct {
	Metadata        CommandMetadata
	IssueRequestID  string
	ExpectedVersion int64
	FailureCode     string
}

type RecordSuccessInput struct {
	Metadata        CommandMetadata
	IssueRequestID  string
	ExpectedVersion int64
	UserCouponID    string
}

type RejectInput struct {
	Metadata        CommandMetadata
	IssueRequestID  string
	ExpectedVersion int64
	ReasonCode      string
	SourceResultRef string
}

type MarkPendingInput struct {
	Metadata             CommandMetadata
	IssueRequestID       string
	ExpectedVersion      int64
	ReservationResultRef string
}

type IssueRequestResult struct {
	IssueRequestID   string
	Status           issuerequest.Status
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Replayed         bool
}

type CodeResult struct {
	IssueRequestID   string
	CodeID           string
	CampaignID       string
	BatchVersion     int64
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Replayed         bool
	Rejected         bool
	ReasonCode       string
}

type UserCouponResult struct {
	UserCouponID     string
	IssueRequestID   string
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Replayed         bool
}

func (s *Service) Claim(ctx context.Context, input ClaimInput) (IssueRequestResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return IssueRequestResult{}, err
	}
	if strings.TrimSpace(input.CampaignID) == "" || strings.TrimSpace(input.UserID) == "" {
		return IssueRequestResult{}, invalidInput(CommandClaim, "campaign and user are required")
	}
	current, fundingSnapshot, policySnapshot, err := s.readCampaignSnapshots(ctx, input.CampaignID, input.Metadata.OccurredAt, true)
	if err != nil {
		return IssueRequestResult{}, err
	}
	eligibility, err := s.userEligibility.Snapshot(ctx, input.UserID, input.Metadata.OccurredAt)
	if err != nil {
		return IssueRequestResult{}, verificationError(CommandClaim, err)
	}
	if err := eligibility.Snapshot.Validate(); err != nil {
		return IssueRequestResult{}, err
	}
	if !eligibility.Eligible {
		return IssueRequestResult{}, ErrUserIneligible
	}
	policySnapshot, err = attachEligibilitySnapshot(policySnapshot, eligibility.Snapshot)
	if err != nil {
		return IssueRequestResult{}, err
	}
	issueRequestID := stableID("issue_request", CommandClaim, input.Metadata.BusinessKey)
	request := issuerequest.Request{
		ID: issueRequestID, CampaignID: input.CampaignID, UserID: input.UserID,
		BusinessKey: input.Metadata.BusinessKey, SourceType: issuerequest.SourceClaim,
		SourceRef: "claim:" + issueRequestID, Status: issuerequest.StatusAccepted,
		IssuerAndFundingSnapshot: fundingSnapshot, PolicySnapshot: policySnapshot,
		ApprovalRef: current.ApprovalRef, CreatedAt: input.Metadata.OccurredAt.UTC(),
		UpdatedAt: input.Metadata.OccurredAt.UTC(),
	}
	if err := request.Validate(); err != nil {
		return IssueRequestResult{}, err
	}
	hashInput := struct {
		CampaignID      string
		UserID          string
		FundingSnapshot json.RawMessage
		PolicySnapshot  json.RawMessage
	}{input.CampaignID, input.UserID, fundingSnapshot, policySnapshot}
	command, err := issueCommand(CommandClaim, input.Metadata, hashInput)
	if err != nil {
		return IssueRequestResult{}, err
	}
	mutation, err := s.issueRequests.Create(ctx, request, issuerequest.Admission{PerUserLimit: current.PerUserLimit}, command)
	if err != nil {
		return IssueRequestResult{}, err
	}
	return issueResult(issueRequestID, mutation), nil
}

func (s *Service) RedeemCode(ctx context.Context, input RedeemCodeInput) (CodeResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return CodeResult{}, err
	}
	if strings.TrimSpace(input.UserID) == "" {
		return CodeResult{}, invalidInput(CommandRedeemCode, "user is required")
	}
	digest, _, err := couponcode.Fingerprint(input.Code, s.codeHashKey)
	if err != nil {
		return CodeResult{}, err
	}
	issueRequestID := stableID("issue_request", CommandRedeemCode, input.Metadata.BusinessKey)
	hashInput := struct {
		UserID      string
		Fingerprint string
	}{input.UserID, hex.EncodeToString(digest)}
	command, err := codeCommand(CommandRedeemCode, input.Metadata, hashInput)
	if err != nil {
		return CodeResult{}, err
	}
	code, err := s.codes.FindByHash(ctx, digest)
	if err != nil {
		return CodeResult{}, err
	}
	// A locked repository decision remains authoritative for duplicate and
	// concurrent reservations. Replays do not repeat external eligibility reads.
	if code.Status == couponcode.CodeAvailable {
		reasonCode, validationErr := s.codeRejectionReason(ctx, code.CampaignID, input.UserID, input.Metadata.OccurredAt)
		if validationErr != nil {
			return CodeResult{}, validationErr
		}
		if reasonCode != "" {
			mutation, rejectErr := s.codes.Reject(ctx, digest, input.UserID, issueRequestID, reasonCode, command)
			if rejectErr != nil {
				return CodeResult{}, rejectErr
			}
			return codeResult(issueRequestID, mutation), nil
		}
	}
	mutation, err := s.codes.Reserve(ctx, digest, input.UserID, issueRequestID, input.Metadata.OccurredAt.Add(s.codeReservationTTL), command)
	if err != nil {
		return CodeResult{}, err
	}
	return codeResult(issueRequestID, mutation), nil
}

func (s *Service) codeRejectionReason(ctx context.Context, campaignID, userID string, at time.Time) (string, error) {
	current, err := s.campaigns.GetEffective(ctx, campaignID, at)
	if err != nil {
		if errors.Is(err, campaign.ErrCampaignInactive) {
			return "campaign_inactive", nil
		}
		return "", err
	}
	controls, err := s.operationalControl.FindEffective(ctx, operations.Scope{Type: operations.ScopeCampaign, Ref: campaignID}, at)
	if err != nil {
		return "", verificationError(CommandRedeemCode, err)
	}
	for _, control := range controls {
		if control.Active && control.BlockIssuance && !control.EffectiveFrom.After(at) {
			return "issuance_blocked", nil
		}
	}
	if !current.IsIssuableAt(at) {
		return "campaign_inactive", nil
	}
	eligibility, err := s.userEligibility.Snapshot(ctx, userID, at)
	if err != nil {
		return "", verificationError(CommandRedeemCode, err)
	}
	if err := eligibility.Snapshot.Validate(); err != nil {
		return "", err
	}
	if !eligibility.Eligible {
		return "user_ineligible", nil
	}
	return "", nil
}

func (s *Service) CreateIssueRequest(ctx context.Context, input CreateIssueRequestInput) (IssueRequestResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return IssueRequestResult{}, err
	}
	if strings.TrimSpace(input.CampaignID) == "" || strings.TrimSpace(input.UserID) == "" || strings.TrimSpace(input.SourceRef) == "" {
		return IssueRequestResult{}, invalidInput(CommandCreateIssueRequest, "campaign, user, and source are required")
	}
	switch input.SourceType {
	case issuerequest.SourceRedeemCode, issuerequest.SourceBulk, issuerequest.SourceSystemGrant, issuerequest.SourceOperatorGrant:
	default:
		return IssueRequestResult{}, invalidInput(CommandCreateIssueRequest, "source type is not supported by this command")
	}
	if input.SourceType == issuerequest.SourceOperatorGrant {
		if strings.TrimSpace(input.CaseRef) == "" || strings.TrimSpace(input.ReasonCode) == "" {
			return IssueRequestResult{}, invalidInput(CommandCreateIssueRequest, "case reference and reason are required for operator grants")
		}
		if strings.TrimSpace(input.Metadata.ApprovalRef) != "" {
			if err := s.approvals.VerifyApproval(ctx, input.Metadata.ApprovalRef, CommandCreateIssueRequest); err != nil {
				return IssueRequestResult{}, verificationError(CommandCreateIssueRequest, err)
			}
		}
		if err := s.cases.VerifyCase(ctx, input.CaseRef, ports.CSCaseBinding{UserID: input.UserID, OperationTaskRef: input.SourceRef}); err != nil {
			return IssueRequestResult{}, verificationError(CommandCreateIssueRequest, err)
		}
	}
	current, fundingSnapshot, policySnapshot, err := s.readCampaignSnapshots(ctx, input.CampaignID, input.Metadata.OccurredAt, false)
	if err != nil {
		return IssueRequestResult{}, err
	}
	issueRequestID := input.IssueRequestID
	if issueRequestID == "" {
		issueRequestID = stableID("issue_request", CommandCreateIssueRequest, input.Metadata.BusinessKey)
	}
	approvalRef := current.ApprovalRef
	if input.SourceType == issuerequest.SourceOperatorGrant {
		approvalRef = input.Metadata.ApprovalRef
	}
	request := issuerequest.Request{
		ID: issueRequestID, CampaignID: input.CampaignID, UserID: input.UserID,
		BusinessKey: input.Metadata.BusinessKey, SourceType: input.SourceType, SourceRef: input.SourceRef,
		Status: issuerequest.StatusAccepted, IssuerAndFundingSnapshot: fundingSnapshot,
		PolicySnapshot: policySnapshot, ApprovalRef: approvalRef,
		CreatedAt: input.Metadata.OccurredAt.UTC(), UpdatedAt: input.Metadata.OccurredAt.UTC(),
	}
	if err := request.Validate(); err != nil {
		return IssueRequestResult{}, err
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(strings.TrimSpace(input.Metadata.ApprovalRef) != "")
	hashInput.IssueRequestID = issueRequestID
	command, err := issueCommand(CommandCreateIssueRequest, input.Metadata, hashInput)
	if err != nil {
		return IssueRequestResult{}, err
	}
	mutation, err := s.issueRequests.Create(ctx, request, issuerequest.Admission{PerUserLimit: current.PerUserLimit}, command)
	if err != nil {
		return IssueRequestResult{}, err
	}
	return issueResult(issueRequestID, mutation), nil
}

func (s *Service) CreateCompensationIssueRequest(ctx context.Context, input CreateCompensationIssueRequestInput) (IssueRequestResult, error) {
	if err := input.SourceRef.Validate(); err != nil {
		return IssueRequestResult{}, err
	}
	if strings.TrimSpace(input.ReasonCode) == "" {
		return IssueRequestResult{}, invalidInput(CommandCreateIssueRequest, "compensation reason is required")
	}
	if err := input.ApprovalPolicy.Validate(); err != nil {
		return IssueRequestResult{}, err
	}
	return s.CreateIssueRequest(ctx, CreateIssueRequestInput{
		Metadata: input.Metadata, CampaignID: input.CampaignID, UserID: input.UserID,
		SourceType: issuerequest.SourceOperatorGrant,
		SourceRef:  strings.Join([]string{input.SourceRef.Context, input.SourceRef.Type, input.SourceRef.ID}, ":"),
		ReasonCode: input.ReasonCode,
		CaseRef:    input.CaseRef,
	})
}

func (s *Service) IssueUserCoupon(ctx context.Context, input IssueUserCouponInput) (UserCouponResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return UserCouponResult{}, err
	}
	if strings.TrimSpace(input.IssueRequestID) == "" || input.ExpectedIssueRequestVersion < 0 {
		return UserCouponResult{}, invalidInput(CommandIssueUserCoupon, "issue request and expected version are required")
	}
	request, err := s.issueRequests.Get(ctx, input.IssueRequestID)
	if err != nil {
		return UserCouponResult{}, err
	}
	if request.Version != input.ExpectedIssueRequestVersion {
		return UserCouponResult{}, issuerequest.ErrInvalidTransition
	}
	if request.Status == issuerequest.StatusCompleted && request.UserCouponID != "" {
		existing, err := s.userCoupons.GetByIssueRequest(ctx, request.ID)
		if err != nil {
			return UserCouponResult{}, err
		}
		snapshot, err := json.Marshal(existing)
		if err != nil {
			return UserCouponResult{}, oops.In("coupon_issuance_application").Code("issuance.grant_snapshot_failed").Wrap(err)
		}
		return UserCouponResult{
			UserCouponID: existing.ID, IssueRequestID: request.ID, ResultRef: existing.ResultRef,
			ResponseSnapshot: snapshot, Replayed: true,
		}, nil
	}
	if request.Status != issuerequest.StatusProcessing {
		return UserCouponResult{}, issuerequest.ErrInvalidTransition
	}
	policy, err := decodePolicySnapshot(request.PolicySnapshot)
	if err != nil {
		return UserCouponResult{}, err
	}
	funding, fundingVersion, err := decodeFundingSnapshot(request.IssuerAndFundingSnapshot)
	if err != nil {
		return UserCouponResult{}, err
	}
	if policy.CampaignID != request.CampaignID || policy.CampaignVersion != fundingVersion {
		return UserCouponResult{}, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("issue request snapshots do not match the campaign")
	}
	grantSnapshot, err := buildGrantSnapshot(policy, funding, request.PolicySnapshot)
	if err != nil {
		return UserCouponResult{}, oops.In("coupon_issuance_application").Code("issuance.grant_snapshot_failed").Wrap(err)
	}
	userCouponID := stableID("user_coupon", CommandIssueUserCoupon, request.ID)
	resultRef := "user_coupon:" + userCouponID + ":granted"
	coupon := usercoupon.Coupon{
		ID: userCouponID, CampaignID: request.CampaignID, PolicyVersion: policy.PolicyVersion,
		UserID: request.UserID, IssueRequestID: request.ID, Status: usercoupon.StatusGranted,
		UsableFrom: policy.StartsAt, ExpiresAt: policy.EndsAt, GrantSnapshot: grantSnapshot,
		ResultRef: resultRef, CreatedAt: input.Metadata.OccurredAt.UTC(), UpdatedAt: input.Metadata.OccurredAt.UTC(),
	}
	if err := coupon.Validate(); err != nil {
		return UserCouponResult{}, err
	}
	hashInput := struct {
		IssueRequestID string
		RequestVersion int64
		PolicySnapshot json.RawMessage
	}{request.ID, request.Version, request.PolicySnapshot}
	command, err := userCouponCommand(CommandIssueUserCoupon, input.Metadata, hashInput)
	if err != nil {
		return UserCouponResult{}, err
	}
	mutation, err := s.userCoupons.Grant(ctx, coupon, command)
	if err != nil {
		return UserCouponResult{}, err
	}
	return UserCouponResult{
		UserCouponID: mutation.Coupon.ID, IssueRequestID: request.ID, ResultRef: mutation.ResultRef,
		ResponseSnapshot: mutation.ResponseSnapshot, Replayed: mutation.Replayed,
	}, nil
}

func (s *Service) RecordFailure(ctx context.Context, input RecordFailureInput) (IssueRequestResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return IssueRequestResult{}, err
	}
	if strings.TrimSpace(input.IssueRequestID) == "" || input.ExpectedVersion < 0 || strings.TrimSpace(input.FailureCode) == "" || strings.TrimSpace(input.FailureResultRef) == "" {
		return IssueRequestResult{}, invalidInput(CommandRecordFailure, "issue request, failure code, failure result, and expected version are required")
	}
	if input.Retryable && (input.NextAttemptAt == nil || !input.NextAttemptAt.After(input.Metadata.OccurredAt)) {
		return IssueRequestResult{}, invalidInput(CommandRecordFailure, "retryable failure requires a future retry time")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := issueCommand(CommandRecordFailure, input.Metadata, hashInput)
	if err != nil {
		return IssueRequestResult{}, err
	}
	mutation, err := s.issueRequests.RecordFailure(ctx, input.IssueRequestID, input.ExpectedVersion, input.FailureCode, input.Retryable, input.NextAttemptAt, command)
	if err != nil {
		return IssueRequestResult{}, err
	}
	return issueResult(input.IssueRequestID, mutation), nil
}

func (s *Service) ConfirmCode(ctx context.Context, input ConfirmCodeInput) (CodeResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return CodeResult{}, err
	}
	if strings.TrimSpace(input.CodeID) == "" || strings.TrimSpace(input.IssueRequestID) == "" || strings.TrimSpace(input.UserCouponID) == "" || input.ExpectedBatchVersion < 0 {
		return CodeResult{}, invalidInput(CommandConfirmCode, "code, issue request, user coupon, and expected batch version are required")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := codeCommand(CommandConfirmCode, input.Metadata, hashInput)
	if err != nil {
		return CodeResult{}, err
	}
	mutation, err := s.codes.Confirm(ctx, input.CodeID, input.IssueRequestID, input.UserCouponID, input.ExpectedBatchVersion, command)
	if err != nil {
		return CodeResult{}, err
	}
	return codeResult(input.IssueRequestID, mutation), nil
}

func (s *Service) ReleaseCode(ctx context.Context, input ReleaseCodeInput) (CodeResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return CodeResult{}, err
	}
	if strings.TrimSpace(input.CodeID) == "" || strings.TrimSpace(input.IssueRequestID) == "" || strings.TrimSpace(input.FailureResultRef) == "" || input.ExpectedBatchVersion < 0 {
		return CodeResult{}, invalidInput(CommandReleaseCode, "code, issue request, failure result, and expected batch version are required")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := codeCommand(CommandReleaseCode, input.Metadata, hashInput)
	if err != nil {
		return CodeResult{}, err
	}
	mutation, err := s.codes.Release(ctx, input.CodeID, input.IssueRequestID, input.ExpectedBatchVersion, command)
	if err != nil {
		return CodeResult{}, err
	}
	return codeResult(input.IssueRequestID, mutation), nil
}

func (s *Service) RetryIssue(ctx context.Context, input RetryIssueInput) (IssueRequestResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return IssueRequestResult{}, err
	}
	// A retry event may be consumed after its scheduled time. A past deadline
	// means the request is due immediately; changing it here would lose the
	// failure record's exact next_attempt_at correlation.
	if strings.TrimSpace(input.IssueRequestID) == "" || input.ExpectedVersion < 0 || input.NextAttemptAt.IsZero() {
		return IssueRequestResult{}, invalidInput(CommandRetryIssue, "issue request, expected version, and retry time are required")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := issueCommand(CommandRetryIssue, input.Metadata, hashInput)
	if err != nil {
		return IssueRequestResult{}, err
	}
	mutation, err := s.issueRequests.Retry(ctx, input.IssueRequestID, input.ExpectedVersion, input.NextAttemptAt, command)
	if err != nil {
		return IssueRequestResult{}, err
	}
	return issueResult(input.IssueRequestID, mutation), nil
}

func (s *Service) FinalizeFailure(ctx context.Context, input FinalizeFailureInput) (IssueRequestResult, error) {
	if err := input.Metadata.validate(true); err != nil {
		return IssueRequestResult{}, err
	}
	if strings.TrimSpace(input.IssueRequestID) == "" || input.ExpectedVersion < 0 || strings.TrimSpace(input.FailureCode) == "" {
		return IssueRequestResult{}, invalidInput(CommandFinalizeFailure, "issue request, failure code, and expected version are required")
	}
	if err := s.approvals.VerifyApproval(ctx, input.Metadata.ApprovalRef, CommandFinalizeFailure); err != nil {
		return IssueRequestResult{}, verificationError(CommandFinalizeFailure, err)
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(true)
	command, err := issueCommand(CommandFinalizeFailure, input.Metadata, hashInput)
	if err != nil {
		return IssueRequestResult{}, err
	}
	mutation, err := s.issueRequests.FinalizeFailure(ctx, input.IssueRequestID, input.ExpectedVersion, input.FailureCode, command)
	if err != nil {
		return IssueRequestResult{}, err
	}
	return issueResult(input.IssueRequestID, mutation), nil
}

func (s *Service) RecordSuccess(ctx context.Context, input RecordSuccessInput) (IssueRequestResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return IssueRequestResult{}, err
	}
	if strings.TrimSpace(input.IssueRequestID) == "" || input.ExpectedVersion < 0 || strings.TrimSpace(input.UserCouponID) == "" {
		return IssueRequestResult{}, invalidInput(CommandRecordSuccess, "issue request, user coupon, and expected version are required")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := issueCommand(CommandRecordSuccess, input.Metadata, hashInput)
	if err != nil {
		return IssueRequestResult{}, err
	}
	mutation, err := s.issueRequests.Complete(ctx, input.IssueRequestID, input.ExpectedVersion, input.UserCouponID, command)
	if err != nil {
		return IssueRequestResult{}, err
	}
	return issueResult(input.IssueRequestID, mutation), nil
}

func (s *Service) Reject(ctx context.Context, input RejectInput) (IssueRequestResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return IssueRequestResult{}, err
	}
	if strings.TrimSpace(input.IssueRequestID) == "" || input.ExpectedVersion < 0 || strings.TrimSpace(input.ReasonCode) == "" || strings.TrimSpace(input.SourceResultRef) == "" {
		return IssueRequestResult{}, invalidInput(CommandReject, "issue request, reason, source result, and expected version are required")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := issueCommand(CommandReject, input.Metadata, hashInput)
	if err != nil {
		return IssueRequestResult{}, err
	}
	mutation, err := s.issueRequests.Reject(ctx, input.IssueRequestID, input.ExpectedVersion, input.ReasonCode, command)
	if err != nil {
		return IssueRequestResult{}, err
	}
	return issueResult(input.IssueRequestID, mutation), nil
}

func (s *Service) MarkPending(ctx context.Context, input MarkPendingInput) (IssueRequestResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return IssueRequestResult{}, err
	}
	if strings.TrimSpace(input.IssueRequestID) == "" || input.ExpectedVersion < 0 || strings.TrimSpace(input.ReservationResultRef) == "" {
		return IssueRequestResult{}, invalidInput(CommandMarkPending, "issue request, reservation result, and expected version are required")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := issueCommand(CommandMarkPending, input.Metadata, hashInput)
	if err != nil {
		return IssueRequestResult{}, err
	}
	mutation, err := s.issueRequests.MarkPending(ctx, input.IssueRequestID, input.ExpectedVersion, command)
	if err != nil {
		return IssueRequestResult{}, err
	}
	return issueResult(input.IssueRequestID, mutation), nil
}

type campaignPolicySnapshot struct {
	CampaignID          string                         `json:"campaignId"`
	CampaignVersion     int64                          `json:"campaignVersion"`
	PolicyVersion       int64                          `json:"policyVersion"`
	DisplayName         string                         `json:"displayName"`
	PerUserLimit        int64                          `json:"perUserLimit"`
	StartsAt            time.Time                      `json:"startsAt"`
	EndsAt              time.Time                      `json:"endsAt"`
	Benefits            []campaign.Benefit             `json:"benefits"`
	Applicability       []campaign.ApplicabilityPolicy `json:"applicability"`
	OwnerSnapshot       shared.SnapshotRef             `json:"ownerSnapshot"`
	EligibilitySnapshot *shared.SnapshotRef            `json:"eligibilitySnapshot,omitempty"`
}

type snapshotEnvelope struct {
	Version     int64           `json:"version"`
	PayloadHash string          `json:"payloadHash"`
	Payload     json.RawMessage `json:"payload"`
}

func (s *Service) readCampaignSnapshots(ctx context.Context, campaignID string, at time.Time, enforceClaimWindow bool) (campaign.Campaign, json.RawMessage, json.RawMessage, error) {
	current, err := s.campaigns.GetEffective(ctx, campaignID, at)
	if err != nil {
		return campaign.Campaign{}, nil, nil, err
	}
	if !current.IsIssuableAt(at) {
		return campaign.Campaign{}, nil, nil, campaign.ErrCampaignInactive
	}
	controls, err := s.operationalControl.FindEffective(ctx, operations.Scope{Type: operations.ScopeCampaign, Ref: campaignID}, at)
	if err != nil {
		return campaign.Campaign{}, nil, nil, verificationError("campaign_operational_control", err)
	}
	for _, control := range controls {
		if control.Active && control.BlockIssuance && !control.EffectiveFrom.After(at) {
			return campaign.Campaign{}, nil, nil, ErrIssuanceBlocked
		}
	}
	if enforceClaimWindow {
		if current.ClaimStartsAt == nil || current.ClaimEndsAt == nil || at.Before(*current.ClaimStartsAt) || !at.Before(*current.ClaimEndsAt) {
			return campaign.Campaign{}, nil, nil, campaign.ErrCampaignInactive
		}
	}
	fundingSnapshot, err := freezeSnapshot(current.Version, current.IssuerAndFunding)
	if err != nil {
		return campaign.Campaign{}, nil, nil, err
	}
	policySnapshot, err := freezeSnapshot(current.Version, campaignPolicySnapshot{
		CampaignID: current.ID, CampaignVersion: current.Version, PolicyVersion: current.CurrentPolicyVersion,
		DisplayName: current.DisplayName, PerUserLimit: current.PerUserLimit,
		StartsAt: current.StartsAt, EndsAt: current.EndsAt, Benefits: current.Benefits,
		Applicability: current.Applicability, OwnerSnapshot: current.OwnerSnapshot,
	})
	if err != nil {
		return campaign.Campaign{}, nil, nil, err
	}
	return current, fundingSnapshot, policySnapshot, nil
}

func attachEligibilitySnapshot(raw json.RawMessage, eligibility shared.SnapshotRef) (json.RawMessage, error) {
	policy, err := decodePolicySnapshot(raw)
	if err != nil {
		return nil, err
	}
	policy.EligibilitySnapshot = &eligibility
	return freezeSnapshot(policy.CampaignVersion, policy)
}

func freezeSnapshot(version int64, value any) (json.RawMessage, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, oops.In("coupon_issuance_application").Code("issuance.snapshot_encode_failed").Wrap(err)
	}
	digest := sha256.Sum256(payload)
	envelope, err := json.Marshal(snapshotEnvelope{Version: version, PayloadHash: "sha256:" + hex.EncodeToString(digest[:]), Payload: payload})
	if err != nil {
		return nil, oops.In("coupon_issuance_application").Code("issuance.snapshot_encode_failed").Wrap(err)
	}
	return envelope, nil
}

func decodePolicySnapshot(raw json.RawMessage) (campaignPolicySnapshot, error) {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return campaignPolicySnapshot{}, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").Wrap(err)
	}
	var policy campaignPolicySnapshot
	if err := json.Unmarshal(envelope.Payload, &policy); err != nil {
		return campaignPolicySnapshot{}, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").Wrap(err)
	}
	canonical, err := json.Marshal(policy)
	if err != nil {
		return campaignPolicySnapshot{}, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").Wrap(err)
	}
	digest := sha256.Sum256(canonical)
	if envelope.Version < 0 || envelope.PayloadHash != "sha256:"+hex.EncodeToString(digest[:]) {
		return campaignPolicySnapshot{}, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("policy snapshot version or hash is invalid")
	}
	if strings.TrimSpace(policy.CampaignID) == "" || strings.TrimSpace(policy.DisplayName) == "" || policy.PolicyVersion < 1 || policy.PerUserLimit <= 0 || policy.StartsAt.IsZero() || !policy.StartsAt.Before(policy.EndsAt) {
		return campaignPolicySnapshot{}, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("policy snapshot is incomplete")
	}
	if policy.CampaignVersion != envelope.Version {
		return campaignPolicySnapshot{}, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("policy snapshot aggregate version does not match its envelope")
	}
	return policy, nil
}

func decodeFundingSnapshot(raw json.RawMessage) (shared.IssuerAndFunding, int64, error) {
	var envelope snapshotEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return shared.IssuerAndFunding{}, 0, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").Wrap(err)
	}
	var funding shared.IssuerAndFunding
	if err := json.Unmarshal(envelope.Payload, &funding); err != nil {
		return shared.IssuerAndFunding{}, 0, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").Wrap(err)
	}
	canonical, err := json.Marshal(funding)
	if err != nil {
		return shared.IssuerAndFunding{}, 0, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").Wrap(err)
	}
	digest := sha256.Sum256(canonical)
	if envelope.Version < 0 || envelope.PayloadHash != "sha256:"+hex.EncodeToString(digest[:]) {
		return shared.IssuerAndFunding{}, 0, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("funding snapshot version or hash is invalid")
	}
	if err := funding.Validate(); err != nil {
		return shared.IssuerAndFunding{}, 0, err
	}
	return funding, envelope.Version, nil
}

type grantBenefit struct {
	Type              campaign.BenefitType `json:"type"`
	Amount            *shared.Money        `json:"amount,omitempty"`
	Percentage        string               `json:"percentage,omitempty"`
	MaxDiscountAmount *shared.Money        `json:"maxDiscountAmount,omitempty"`
}

type grantApplicability struct {
	PolicySchemaVersion int                  `json:"policySchemaVersion"`
	IncludeTargets      []shared.ExternalRef `json:"includeTargets"`
	ExcludeTargets      []shared.ExternalRef `json:"excludeTargets"`
	MinimumOrderAmount  *shared.Money        `json:"minimumOrderAmount,omitempty"`
	StackingPolicyRef   string               `json:"stackingPolicyRef,omitempty"`
}

type grantFunding struct {
	IssuerType              string              `json:"issuerType"`
	IssuerRef               shared.ExternalRef  `json:"issuerRef"`
	FunderType              string              `json:"funderType"`
	FunderRef               *shared.ExternalRef `json:"funderRef,omitempty"`
	PlatformSharePercentage string              `json:"platformSharePercentage,omitempty"`
}

func buildGrantSnapshot(policy campaignPolicySnapshot, funding shared.IssuerAndFunding, frozenPolicySnapshot json.RawMessage) (json.RawMessage, error) {
	if len(policy.Benefits) == 0 {
		return nil, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("grant snapshot requires a benefit")
	}
	benefit := policy.Benefits[0]
	if err := benefit.Validate(); err != nil {
		return nil, err
	}
	applicability := grantApplicability{PolicySchemaVersion: 1, IncludeTargets: []shared.ExternalRef{}, ExcludeTargets: []shared.ExternalRef{}}
	for _, rule := range policy.Applicability {
		ref := shared.ExternalRef{Context: rule.TargetType, Type: rule.TargetType, ID: rule.TargetRef}
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		switch rule.Inclusion {
		case "include":
			applicability.IncludeTargets = append(applicability.IncludeTargets, ref)
		case "exclude":
			applicability.ExcludeTargets = append(applicability.ExcludeTargets, ref)
		default:
			return nil, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("applicability inclusion is invalid")
		}
		var condition struct {
			MinimumOrderAmount *shared.Money `json:"minimumOrderAmount"`
			StackingPolicyRef  string        `json:"stackingPolicyRef"`
		}
		if err := json.Unmarshal(rule.ConditionValue, &condition); err != nil {
			return nil, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").Wrap(err)
		}
		if condition.MinimumOrderAmount != nil {
			if err := condition.MinimumOrderAmount.Validate(); err != nil {
				return nil, err
			}
			if applicability.MinimumOrderAmount != nil && *applicability.MinimumOrderAmount != *condition.MinimumOrderAmount {
				return nil, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("applicability minimum order amounts conflict")
			}
			copyValue := *condition.MinimumOrderAmount
			applicability.MinimumOrderAmount = &copyValue
		}
		if condition.StackingPolicyRef != "" {
			if applicability.StackingPolicyRef != "" && applicability.StackingPolicyRef != condition.StackingPolicyRef {
				return nil, oops.In("coupon_issuance_application").Code("issuance.snapshot_invalid").New("applicability stacking policies conflict")
			}
			applicability.StackingPolicyRef = condition.StackingPolicyRef
		}
	}
	return json.Marshal(struct {
		DisplayName      string             `json:"displayName"`
		Benefit          grantBenefit       `json:"benefit"`
		Applicability    grantApplicability `json:"applicability"`
		IssuerAndFunding grantFunding       `json:"issuerAndFunding"`
		PolicySnapshot   json.RawMessage    `json:"policySnapshot"`
	}{
		DisplayName: policy.DisplayName,
		Benefit: grantBenefit{
			Type: benefit.Type, Amount: benefit.Amount, Percentage: benefit.Percentage,
			MaxDiscountAmount: benefit.MaxDiscountAmount,
		},
		Applicability: applicability,
		IssuerAndFunding: grantFunding{
			IssuerType: funding.IssuerType, IssuerRef: funding.IssuerRef, FunderType: funding.FunderType,
			FunderRef: funding.FunderRef, PlatformSharePercentage: funding.PlatformSharePercentage,
		},
		PolicySnapshot: frozenPolicySnapshot,
	})
}

func (m CommandMetadata) validate(requireApproval bool) error {
	if strings.TrimSpace(m.CommandID) == "" || strings.TrimSpace(m.BusinessKey) == "" || strings.TrimSpace(m.CorrelationID) == "" || m.OccurredAt.IsZero() ||
		!m.LeaseUntil.After(m.OccurredAt) || !m.ExpiresAt.After(m.LeaseUntil) {
		return invalidInput("command", "command identity, correlation, lease, and expiry are required")
	}
	if requireApproval && strings.TrimSpace(m.ApprovalRef) == "" {
		return invalidInput("command", "approval reference is required")
	}
	return nil
}

func (m CommandMetadata) canonical(includeApproval bool) CommandMetadata {
	if includeApproval {
		return CommandMetadata{ApprovalRef: m.ApprovalRef}
	}
	return CommandMetadata{}
}

func issueCommand(operation string, metadata CommandMetadata, payload any) (issuerequest.Command, error) {
	requestHash, err := canonicalHash(payload)
	if err != nil {
		return issuerequest.Command{}, err
	}
	return issuerequest.Command{
		OperationType: operation, BusinessKey: metadata.BusinessKey, RequestHash: requestHash,
		CorrelationID: metadata.CorrelationID, CausationID: causationID(metadata),
		TraceID: metadata.TraceID, OccurredAt: metadata.OccurredAt.UTC(),
		LeaseUntil: metadata.LeaseUntil.UTC(), ExpiresAt: metadata.ExpiresAt.UTC(),
	}, nil
}

func codeCommand(operation string, metadata CommandMetadata, payload any) (couponcode.Command, error) {
	requestHash, err := canonicalHash(payload)
	if err != nil {
		return couponcode.Command{}, err
	}
	return couponcode.Command{
		OperationType: operation, BusinessKey: metadata.BusinessKey, RequestHash: requestHash,
		CorrelationID: metadata.CorrelationID, CausationID: causationID(metadata),
		TraceID: metadata.TraceID, OccurredAt: metadata.OccurredAt.UTC(),
		LeaseUntil: metadata.LeaseUntil.UTC(), ExpiresAt: metadata.ExpiresAt.UTC(),
	}, nil
}

func userCouponCommand(operation string, metadata CommandMetadata, payload any) (usercoupon.Command, error) {
	requestHash, err := canonicalHash(payload)
	if err != nil {
		return usercoupon.Command{}, err
	}
	return usercoupon.Command{
		OperationType: operation, BusinessKey: metadata.BusinessKey, RequestHash: requestHash,
		CorrelationID: metadata.CorrelationID, CausationID: causationID(metadata),
		TraceID: metadata.TraceID, OccurredAt: metadata.OccurredAt.UTC(),
		LeaseUntil: metadata.LeaseUntil.UTC(), ExpiresAt: metadata.ExpiresAt.UTC(),
	}, nil
}

func canonicalHash(payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", oops.In("coupon_issuance_application").Code("issuance.request_hash_failed").Wrap(err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func causationID(metadata CommandMetadata) string {
	if metadata.CausationID != "" {
		return metadata.CausationID
	}
	return metadata.CommandID
}

func stableID(kind, operation, businessKey string) string {
	prefix := ""
	switch kind {
	case "issue_request":
		prefix = "ireq_"
	case "user_coupon":
		prefix = "ucpn_"
	}
	return prefix + uuid.NewSHA1(uuid.NameSpaceOID, []byte(kind+"\x00"+operation+"\x00"+businessKey)).String()
}

func issueResult(issueRequestID string, mutation issuerequest.Mutation) IssueRequestResult {
	status := mutation.Request.Status
	if status == "" {
		status = issuerequest.StatusAccepted
	}
	return IssueRequestResult{
		IssueRequestID: issueRequestID, Status: status, ResultRef: mutation.ResultRef,
		ResponseSnapshot: mutation.ResponseSnapshot, Replayed: mutation.Replayed,
	}
}

func codeResult(issueRequestID string, mutation couponcode.Mutation) CodeResult {
	return CodeResult{
		IssueRequestID: issueRequestID, CodeID: mutation.Code.ID, CampaignID: mutation.Code.CampaignID,
		BatchVersion: mutation.BatchVersion, ResultRef: mutation.ResultRef, ResponseSnapshot: mutation.ResponseSnapshot,
		Replayed: mutation.Replayed, Rejected: mutation.Rejected, ReasonCode: mutation.ReasonCode,
	}
}

func invalidInput(operation, message string) error {
	return oops.In("coupon_issuance_application").Code("issuance.input_invalid").With("operation", operation).New(message)
}

func verificationError(operation string, err error) error {
	return oops.In("coupon_issuance_application").Code("issuance.verification_failed").With("operation", operation).Wrap(err)
}

var (
	ErrUserIneligible  = oops.In("coupon_issuance_application").Code("issuance.user_ineligible").New("user is not eligible for this coupon campaign")
	ErrIssuanceBlocked = oops.In("coupon_issuance_application").Code("issuance.operational_stop").New("coupon issuance is blocked by an operational control")
)
