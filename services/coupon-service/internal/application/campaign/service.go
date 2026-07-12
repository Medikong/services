package campaignapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

const (
	CommandRegisterPolicy          = "CMD.A.19-01"
	CommandConfigureFirstComeLimit = "CMD.A.19-02"
	CommandReviewSellerCoupon      = "CMD.A.19-03"
	CommandChangePolicy            = "CMD.A.19-04"
	CommandReserveQuantity         = "CMD.A.19-26"
	CommandConfirmQuantity         = "CMD.A.19-27"
	CommandReleaseQuantity         = "CMD.A.19-28"
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

type Dependencies struct {
	Repository      campaign.Repository
	SellerSnapshots ports.SellerCatalogSnapshotPort
	Approvals       ports.OperationApprovalPort
}

type Service struct {
	repository      campaign.Repository
	sellerSnapshots ports.SellerCatalogSnapshotPort
	approvals       ports.OperationApprovalPort
}

func New(deps Dependencies) (*Service, error) {
	if deps.Repository == nil || deps.SellerSnapshots == nil || deps.Approvals == nil {
		return nil, oops.In("coupon_campaign_application").Code("campaign.dependencies_required").New("campaign repository and verification ports are required")
	}
	return &Service{repository: deps.Repository, sellerSnapshots: deps.SellerSnapshots, approvals: deps.Approvals}, nil
}

type RegisterPolicyInput struct {
	Metadata            CommandMetadata
	DisplayName         string
	Description         string
	StartsAt            time.Time
	EndsAt              time.Time
	Benefits            []campaign.Benefit
	Applicability       []campaign.ApplicabilityPolicy
	IssuerAndFunding    shared.IssuerAndFunding
	OwnerSnapshot       shared.SnapshotRef
	ExternalBusinessRef string
}

type ConfigureFirstComeLimitInput struct {
	Metadata        CommandMetadata
	CampaignID      string
	ExpectedVersion int64
	Limit           campaign.QuantityLimit
}

type ReviewSellerCouponInput struct {
	Metadata                CommandMetadata
	CampaignID              string
	ExpectedVersion         int64
	Decision                campaign.Status
	ReasonCode              string
	SellerOwnershipSnapshot shared.SnapshotRef
}

type ChangePolicyInput struct {
	Metadata         CommandMetadata
	CampaignID       string
	ExpectedVersion  int64
	EffectiveAt      time.Time
	Benefits         []campaign.Benefit
	Applicability    []campaign.ApplicabilityPolicy
	IssuerAndFunding *shared.IssuerAndFunding
}

type ReserveQuantityInput struct {
	Metadata        CommandMetadata
	CampaignID      string
	IssueRequestID  string
	Quantity        int64
	ExpectedVersion int64
}

type DecideQuantityInput struct {
	Metadata        CommandMetadata
	CampaignID      string
	IssueRequestID  string
	ExpectedVersion int64
}

type Result struct {
	CampaignID       string
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Replayed         bool
}

type QuantityResult struct {
	CampaignID       string
	ResultRef        string
	ResponseSnapshot json.RawMessage
	Reservation      campaign.QuantityReservation
	Version          int64
	Replayed         bool
	Rejected         bool
	ReasonCode       string
}

func (s *Service) RegisterPolicy(ctx context.Context, input RegisterPolicyInput) (Result, error) {
	if err := input.Metadata.validate(true); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(input.DisplayName) == "" || input.StartsAt.IsZero() || !input.StartsAt.Before(input.EndsAt) {
		return Result{}, invalidInput(CommandRegisterPolicy, "campaign name and validity period are required")
	}
	if err := input.OwnerSnapshot.Validate(); err != nil {
		return Result{}, err
	}
	if err := input.IssuerAndFunding.Validate(); err != nil {
		return Result{}, err
	}
	if err := s.approvals.VerifyApproval(ctx, input.Metadata.ApprovalRef, CommandRegisterPolicy); err != nil {
		return Result{}, verificationError(CommandRegisterPolicy, err)
	}
	if input.IssuerAndFunding.IssuerType == "seller" || input.IssuerAndFunding.IssuerType == "partnership" || input.IssuerAndFunding.FunderType == "seller" {
		if err := s.sellerSnapshots.VerifySellerOwnership(ctx, input.OwnerSnapshot); err != nil {
			return Result{}, verificationError(CommandRegisterPolicy, err)
		}
	}

	campaignID := stableID("campaign", CommandRegisterPolicy, input.Metadata.BusinessKey)
	benefits := append([]campaign.Benefit(nil), input.Benefits...)
	for index := range benefits {
		if benefits[index].ID == "" {
			benefits[index].ID = stableID("benefit", CommandRegisterPolicy, input.Metadata.BusinessKey+":"+strconv.Itoa(index))
		}
		benefits[index].PolicyVersion = 1
	}
	applicability := append([]campaign.ApplicabilityPolicy(nil), input.Applicability...)
	for index := range applicability {
		if applicability[index].ID == "" {
			applicability[index].ID = stableID("applicability", CommandRegisterPolicy, input.Metadata.BusinessKey+":"+strconv.Itoa(index))
		}
		applicability[index].PolicyVersion = 1
		if applicability[index].EffectiveFrom.IsZero() {
			applicability[index].EffectiveFrom = input.StartsAt
		}
		if applicability[index].SnapshotLabel == "" {
			applicability[index].SnapshotLabel = input.OwnerSnapshot.PayloadHash
		}
	}
	funding := input.IssuerAndFunding
	funding.ApprovalRef = input.Metadata.ApprovalRef
	registered := campaign.Campaign{
		ID: campaignID, DisplayName: input.DisplayName, Description: input.Description,
		Status: campaign.StatusUnderReview, StartsAt: input.StartsAt, EndsAt: input.EndsAt,
		CurrentPolicyVersion: 1, IssuerAndFunding: funding, ApprovalRef: input.Metadata.ApprovalRef,
		OwnerSnapshot: input.OwnerSnapshot, ExternalBusinessRef: input.ExternalBusinessRef,
		Benefits: benefits, Applicability: applicability, CreatedAt: input.Metadata.OccurredAt,
		UpdatedAt: input.Metadata.OccurredAt,
	}
	if err := registered.Validate(); err != nil {
		return Result{}, err
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(true)
	command, err := campaignCommand(CommandRegisterPolicy, input.Metadata, hashInput)
	if err != nil {
		return Result{}, err
	}
	mutation, err := s.repository.Create(ctx, registered, command)
	if err != nil {
		return Result{}, err
	}
	return mutationResult(campaignID, mutation), nil
}

func (s *Service) ConfigureFirstComeLimit(ctx context.Context, input ConfigureFirstComeLimitInput) (Result, error) {
	if err := input.Metadata.validate(false); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(input.CampaignID) == "" || input.ExpectedVersion < 0 {
		return Result{}, invalidInput(CommandConfigureFirstComeLimit, "campaign id and expected version are required")
	}
	if err := input.Limit.Validate(); err != nil {
		return Result{}, err
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := campaignCommand(CommandConfigureFirstComeLimit, input.Metadata, hashInput)
	if err != nil {
		return Result{}, err
	}
	mutation, err := s.repository.ConfigureIssuance(ctx, input.CampaignID, input.ExpectedVersion, input.Limit, command)
	if err != nil {
		return Result{}, err
	}
	return mutationResult(input.CampaignID, mutation), nil
}

func (s *Service) ReviewSellerCoupon(ctx context.Context, input ReviewSellerCouponInput) (Result, error) {
	if err := input.Metadata.validate(true); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(input.CampaignID) == "" || input.ExpectedVersion < 0 || strings.TrimSpace(input.ReasonCode) == "" {
		return Result{}, invalidInput(CommandReviewSellerCoupon, "campaign, expected version, and reason are required")
	}
	if input.Decision != campaign.StatusApproved && input.Decision != campaign.StatusRejected && input.Decision != campaign.StatusHeld {
		return Result{}, invalidInput(CommandReviewSellerCoupon, "review decision is not supported")
	}
	if err := input.SellerOwnershipSnapshot.Validate(); err != nil {
		return Result{}, err
	}
	if err := s.approvals.VerifyApproval(ctx, input.Metadata.ApprovalRef, CommandReviewSellerCoupon); err != nil {
		return Result{}, verificationError(CommandReviewSellerCoupon, err)
	}
	if err := s.sellerSnapshots.VerifySellerOwnership(ctx, input.SellerOwnershipSnapshot); err != nil {
		return Result{}, verificationError(CommandReviewSellerCoupon, err)
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(true)
	command, err := campaignCommand(CommandReviewSellerCoupon, input.Metadata, hashInput)
	if err != nil {
		return Result{}, err
	}
	mutation, err := s.repository.Review(ctx, input.CampaignID, input.ExpectedVersion, input.Decision, input.ReasonCode, command)
	if err != nil {
		return Result{}, err
	}
	return mutationResult(input.CampaignID, mutation), nil
}

func (s *Service) ChangePolicy(ctx context.Context, input ChangePolicyInput) (Result, error) {
	if err := input.Metadata.validate(true); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(input.CampaignID) == "" || input.ExpectedVersion < 0 || input.EffectiveAt.IsZero() || !input.EffectiveAt.After(input.Metadata.OccurredAt) {
		return Result{}, invalidInput(CommandChangePolicy, "campaign, expected version, and future effective time are required")
	}
	if len(input.Benefits) == 0 && len(input.Applicability) == 0 && input.IssuerAndFunding == nil {
		return Result{}, invalidInput(CommandChangePolicy, "at least one policy field must change")
	}
	if err := s.approvals.VerifyApproval(ctx, input.Metadata.ApprovalRef, CommandChangePolicy); err != nil {
		return Result{}, verificationError(CommandChangePolicy, err)
	}
	current, err := s.repository.Get(ctx, input.CampaignID)
	if err != nil {
		return Result{}, err
	}
	version := current.CurrentPolicyVersion + 1
	benefits := append([]campaign.Benefit(nil), current.Benefits...)
	if len(input.Benefits) > 0 {
		benefits = append([]campaign.Benefit(nil), input.Benefits...)
	}
	for index := range benefits {
		benefits[index].ID = stableID("benefit", CommandChangePolicy, input.Metadata.BusinessKey+":"+strconv.Itoa(index))
		benefits[index].PolicyVersion = version
	}
	applicability := append([]campaign.ApplicabilityPolicy(nil), current.Applicability...)
	if len(input.Applicability) > 0 {
		applicability = append([]campaign.ApplicabilityPolicy(nil), input.Applicability...)
	}
	for index := range applicability {
		applicability[index].ID = stableID("applicability", CommandChangePolicy, input.Metadata.BusinessKey+":"+strconv.Itoa(index))
		applicability[index].PolicyVersion = version
		applicability[index].EffectiveFrom = input.EffectiveAt
		if applicability[index].SnapshotLabel == "" {
			applicability[index].SnapshotLabel = current.OwnerSnapshot.PayloadHash
		}
	}
	funding := current.IssuerAndFunding
	if input.IssuerAndFunding != nil {
		funding = *input.IssuerAndFunding
	}
	funding.ApprovalRef = input.Metadata.ApprovalRef
	policy := campaign.PolicyVersion{
		Version: version, EffectiveAt: input.EffectiveAt, Benefits: benefits,
		Applicability: applicability, IssuerAndFunding: funding,
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(true)
	command, err := campaignCommand(CommandChangePolicy, input.Metadata, hashInput)
	if err != nil {
		return Result{}, err
	}
	mutation, err := s.repository.AddPolicyVersion(ctx, input.CampaignID, input.ExpectedVersion, policy, command)
	if err != nil {
		return Result{}, err
	}
	return mutationResult(input.CampaignID, mutation), nil
}

func (s *Service) ReserveQuantity(ctx context.Context, input ReserveQuantityInput) (QuantityResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return QuantityResult{}, err
	}
	if strings.TrimSpace(input.CampaignID) == "" || strings.TrimSpace(input.IssueRequestID) == "" || input.Quantity <= 0 || input.ExpectedVersion < 0 {
		return QuantityResult{}, invalidInput(CommandReserveQuantity, "campaign, issue request, positive quantity, and expected version are required")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := campaignCommand(CommandReserveQuantity, input.Metadata, hashInput)
	if err != nil {
		return QuantityResult{}, err
	}
	mutation, err := s.repository.ReserveQuantity(ctx, input.CampaignID, input.IssueRequestID, input.Quantity, input.ExpectedVersion, input.Metadata.OccurredAt, command)
	if err != nil {
		return QuantityResult{}, err
	}
	return quantityResult(input.CampaignID, mutation), nil
}

func (s *Service) ConfirmQuantity(ctx context.Context, input DecideQuantityInput) (QuantityResult, error) {
	return s.decideQuantity(ctx, CommandConfirmQuantity, input)
}

func (s *Service) ReleaseQuantity(ctx context.Context, input DecideQuantityInput) (QuantityResult, error) {
	return s.decideQuantity(ctx, CommandReleaseQuantity, input)
}

func (s *Service) decideQuantity(ctx context.Context, operation string, input DecideQuantityInput) (QuantityResult, error) {
	if err := input.Metadata.validate(false); err != nil {
		return QuantityResult{}, err
	}
	if strings.TrimSpace(input.CampaignID) == "" || strings.TrimSpace(input.IssueRequestID) == "" || input.ExpectedVersion < 0 {
		return QuantityResult{}, invalidInput(operation, "campaign, issue request, and expected version are required")
	}
	hashInput := input
	hashInput.Metadata = input.Metadata.canonical(false)
	command, err := campaignCommand(operation, input.Metadata, hashInput)
	if err != nil {
		return QuantityResult{}, err
	}
	var mutation campaign.QuantityMutation
	if operation == CommandConfirmQuantity {
		mutation, err = s.repository.ConfirmQuantity(ctx, input.CampaignID, input.IssueRequestID, input.ExpectedVersion, command)
	} else {
		mutation, err = s.repository.ReleaseQuantity(ctx, input.CampaignID, input.IssueRequestID, input.ExpectedVersion, command)
	}
	if err != nil {
		return QuantityResult{}, err
	}
	return quantityResult(input.CampaignID, mutation), nil
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

func campaignCommand(operation string, metadata CommandMetadata, payload any) (campaign.Command, error) {
	requestHash, err := canonicalHash(payload)
	if err != nil {
		return campaign.Command{}, err
	}
	causationID := metadata.CausationID
	if causationID == "" {
		causationID = metadata.CommandID
	}
	return campaign.Command{
		OperationType: operation, BusinessKey: metadata.BusinessKey, RequestHash: requestHash,
		CorrelationID: metadata.CorrelationID, CausationID: causationID, TraceID: metadata.TraceID,
		ApprovalRef: metadata.ApprovalRef, OccurredAt: metadata.OccurredAt.UTC(),
		LeaseUntil: metadata.LeaseUntil.UTC(), ExpiresAt: metadata.ExpiresAt.UTC(),
	}, nil
}

func canonicalHash(payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", oops.In("coupon_campaign_application").Code("campaign.request_hash_failed").Wrap(err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func stableID(kind, operation, businessKey string) string {
	prefix := ""
	if kind == "campaign" {
		prefix = "camp_"
	}
	return prefix + uuid.NewSHA1(uuid.NameSpaceOID, []byte(kind+"\x00"+operation+"\x00"+businessKey)).String()
}

func mutationResult(campaignID string, mutation campaign.Mutation) Result {
	return Result{CampaignID: campaignID, ResultRef: mutation.ResultRef, ResponseSnapshot: mutation.ResponseSnapshot, Replayed: mutation.Replayed}
}

func quantityResult(campaignID string, mutation campaign.QuantityMutation) QuantityResult {
	return QuantityResult{
		CampaignID: campaignID, ResultRef: mutation.ResultRef, ResponseSnapshot: mutation.ResponseSnapshot,
		Reservation: mutation.Reservation, Version: mutation.Version, Replayed: mutation.Replayed,
		Rejected: mutation.Rejected, ReasonCode: mutation.ReasonCode,
	}
}

func invalidInput(operation, message string) error {
	return oops.In("coupon_campaign_application").Code("campaign.input_invalid").With("operation", operation).New(message)
}

func verificationError(operation string, err error) error {
	return oops.In("coupon_campaign_application").Code("campaign.verification_failed").With("operation", operation).Wrap(err)
}
