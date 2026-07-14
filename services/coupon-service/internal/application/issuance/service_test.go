package issuanceapp

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/couponcode"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
)

func TestServiceRoutesEachIssuanceCommandToOneMutationRepository(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	next := now.Add(time.Minute)
	tests := []struct {
		name             string
		operation        string
		wantCampaignRead int
		wantIssueCalls   []string
		wantCodeCalls    []string
		wantUserCalls    []string
		run              func(*Service) error
	}{
		{
			name: "claim", operation: CommandClaim, wantCampaignRead: 1, wantIssueCalls: []string{"create"},
			run: func(service *Service) error {
				_, err := service.Claim(context.Background(), ClaimInput{Metadata: issuanceMetadata(now, "claim"), CampaignID: "campaign-1", UserID: "user-1"})
				return err
			},
		},
		{
			name: "redeem code", operation: CommandRedeemCode, wantCampaignRead: 1, wantCodeCalls: []string{"find_by_hash", "reserve"},
			run: func(service *Service) error {
				_, err := service.RedeemCode(context.Background(), RedeemCodeInput{Metadata: issuanceMetadata(now, "redeem"), UserID: "user-1", Code: "ABCD-1234"})
				return err
			},
		},
		{
			name: "issue user coupon", operation: CommandIssueUserCoupon, wantIssueCalls: []string{"get"}, wantUserCalls: []string{"grant"},
			run: func(service *Service) error {
				_, err := service.IssueUserCoupon(context.Background(), IssueUserCouponInput{Metadata: issuanceMetadata(now, "issue"), IssueRequestID: "issue-1", ExpectedIssueRequestVersion: 2})
				return err
			},
		},
		{
			name: "create issue request", operation: CommandCreateIssueRequest, wantCampaignRead: 1, wantIssueCalls: []string{"create"},
			run: func(service *Service) error {
				_, err := service.CreateIssueRequest(context.Background(), CreateIssueRequestInput{
					Metadata: issuanceMetadata(now, "bulk"), CampaignID: "campaign-1", UserID: "user-1",
					SourceType: issuerequest.SourceBulk, SourceRef: "bulk-job-1:user-1",
				})
				return err
			},
		},
		{
			name: "record failure", operation: CommandRecordFailure, wantIssueCalls: []string{"record_failure"},
			run: func(service *Service) error {
				_, err := service.RecordFailure(context.Background(), RecordFailureInput{
					Metadata: issuanceMetadata(now, "failure"), IssueRequestID: "issue-1", ExpectedVersion: 2,
					FailureCode: "POSTGRES_UNAVAILABLE", FailureResultRef: "attempt-1", Retryable: true, NextAttemptAt: &next,
				})
				return err
			},
		},
		{
			name: "confirm code", operation: CommandConfirmCode, wantCodeCalls: []string{"confirm"},
			run: func(service *Service) error {
				_, err := service.ConfirmCode(context.Background(), ConfirmCodeInput{
					Metadata: issuanceMetadata(now, "confirm-code"), CodeID: "code-1", IssueRequestID: "issue-1",
					UserCouponID: "user-coupon-1", ExpectedBatchVersion: 3,
				})
				return err
			},
		},
		{
			name: "release code", operation: CommandReleaseCode, wantCodeCalls: []string{"release"},
			run: func(service *Service) error {
				_, err := service.ReleaseCode(context.Background(), ReleaseCodeInput{
					Metadata: issuanceMetadata(now, "release-code"), CodeID: "code-1", IssueRequestID: "issue-1",
					FailureResultRef: "failure-1", ExpectedBatchVersion: 3,
				})
				return err
			},
		},
		{
			name: "retry issue", operation: CommandRetryIssue, wantIssueCalls: []string{"retry"},
			run: func(service *Service) error {
				_, err := service.RetryIssue(context.Background(), RetryIssueInput{Metadata: issuanceMetadata(now, "retry"), IssueRequestID: "issue-1", ExpectedVersion: 2, NextAttemptAt: next})
				return err
			},
		},
		{
			name: "finalize failure", operation: CommandFinalizeFailure, wantIssueCalls: []string{"finalize_failure"},
			run: func(service *Service) error {
				_, err := service.FinalizeFailure(context.Background(), FinalizeFailureInput{Metadata: issuanceMetadata(now, "finalize"), IssueRequestID: "issue-1", ExpectedVersion: 2, FailureCode: "RETRY_EXHAUSTED"})
				return err
			},
		},
		{
			name: "record success", operation: CommandRecordSuccess, wantIssueCalls: []string{"complete"},
			run: func(service *Service) error {
				_, err := service.RecordSuccess(context.Background(), RecordSuccessInput{Metadata: issuanceMetadata(now, "success"), IssueRequestID: "issue-1", ExpectedVersion: 2, UserCouponID: "user-coupon-1"})
				return err
			},
		},
		{
			name: "reject", operation: CommandReject, wantIssueCalls: []string{"reject"},
			run: func(service *Service) error {
				_, err := service.Reject(context.Background(), RejectInput{Metadata: issuanceMetadata(now, "reject"), IssueRequestID: "issue-1", ExpectedVersion: 0, ReasonCode: "QUANTITY_UNAVAILABLE", SourceResultRef: "quantity-result-1"})
				return err
			},
		},
		{
			name: "mark pending", operation: CommandMarkPending, wantIssueCalls: []string{"mark_pending"},
			run: func(service *Service) error {
				_, err := service.MarkPending(context.Background(), MarkPendingInput{Metadata: issuanceMetadata(now, "pending"), IssueRequestID: "issue-1", ExpectedVersion: 0, ReservationResultRef: "quantity-result-1"})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			campaigns := &campaignReaderFake{campaign: activeCampaign(now)}
			issues := &issueRepositoryFake{current: processingRequest(t, now)}
			codes := &codeRepositoryFake{}
			users := &userCouponRepositoryFake{}
			service := newIssuanceService(t, campaigns, issues, codes, users, &approvalPortFake{}, &casePortFake{})

			require.NoError(t, test.run(service))
			require.Equal(t, test.wantCampaignRead, campaigns.calls)
			require.Equal(t, test.wantIssueCalls, issues.calls)
			require.Equal(t, test.wantCodeCalls, codes.calls)
			require.Equal(t, test.wantUserCalls, users.calls)
			require.Equal(t, 1, issues.mutations+codes.mutations+users.mutations)
			command := capturedOperation(issues.commands, codes.commands, users.commands)
			require.Equal(t, test.operation, command.operation)
			require.Equal(t, "corr-1", command.correlationID)
			require.Equal(t, "command-1", command.causationID)
			require.True(t, strings.HasPrefix(command.requestHash, "sha256:"))
		})
	}
}

func TestClaimReturnsAcceptedWithoutGuessingExternalUserEligibility(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	issues := &issueRepositoryFake{}
	service := newIssuanceService(t, &campaignReaderFake{campaign: activeCampaign(now)}, issues, &codeRepositoryFake{}, &userCouponRepositoryFake{}, &approvalPortFake{}, &casePortFake{})

	result, err := service.Claim(context.Background(), ClaimInput{Metadata: issuanceMetadata(now, "claim"), CampaignID: "campaign-1", UserID: "user-1"})
	require.NoError(t, err)
	require.Equal(t, issuerequest.StatusAccepted, result.Status)
	require.Equal(t, issuerequest.SourceClaim, issues.created.SourceType)
	var funding snapshotEnvelope
	require.NoError(t, json.Unmarshal(issues.created.IssuerAndFundingSnapshot, &funding))
	require.Equal(t, int64(4), funding.Version)
	require.True(t, strings.HasPrefix(funding.PayloadHash, "sha256:"))
	var policy snapshotEnvelope
	require.NoError(t, json.Unmarshal(issues.created.PolicySnapshot, &policy))
	require.Equal(t, funding.Version, policy.Version)
	frozenPolicy, err := decodePolicySnapshot(issues.created.PolicySnapshot)
	require.NoError(t, err)
	require.NotNil(t, frozenPolicy.EligibilitySnapshot)
}

func TestClaimFailsClosedBeforeMutation(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		eligibility ports.UserEligibility
		controls    []operations.Control
		wantError   error
	}{
		{
			name:        "ineligible user",
			eligibility: ports.UserEligibility{Eligible: false, Snapshot: issuanceSnapshot(now)},
			wantError:   ErrUserIneligible,
		},
		{
			name:        "issuance operationally blocked",
			eligibility: ports.UserEligibility{Eligible: true, Snapshot: issuanceSnapshot(now)},
			controls:    []operations.Control{{Active: true, EffectiveFrom: now.Add(-time.Minute), BlockIssuance: true}},
			wantError:   ErrIssuanceBlocked,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := &issueRepositoryFake{}
			service := newIssuanceServiceWithAdmission(t, &campaignReaderFake{campaign: activeCampaign(now)}, issues, &codeRepositoryFake{}, &userCouponRepositoryFake{}, &approvalPortFake{}, &casePortFake{}, &eligibilityPortFake{result: test.eligibility}, &operationalControlReaderFake{controls: test.controls})
			_, err := service.Claim(context.Background(), ClaimInput{Metadata: issuanceMetadata(now, "claim-blocked"), CampaignID: "campaign-1", UserID: "user-1"})
			require.ErrorIs(t, err, test.wantError)
			require.Zero(t, issues.mutations)
		})
	}
}

func TestNonClaimIssueSourceDoesNotReuseClaimEligibility(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	eligibility := &eligibilityPortFake{result: ports.UserEligibility{Eligible: false, Snapshot: issuanceSnapshot(now)}}
	issues := &issueRepositoryFake{}
	service := newIssuanceServiceWithAdmission(t, &campaignReaderFake{campaign: activeCampaign(now)}, issues, &codeRepositoryFake{}, &userCouponRepositoryFake{}, &approvalPortFake{}, &casePortFake{}, eligibility, &operationalControlReaderFake{})

	_, err := service.CreateIssueRequest(context.Background(), CreateIssueRequestInput{
		Metadata: issuanceMetadata(now, "bulk"), CampaignID: "campaign-1", UserID: "user-1",
		SourceType: issuerequest.SourceBulk, SourceRef: "bulk-job-1:user-1",
	})
	require.NoError(t, err)
	require.Zero(t, eligibility.calls)
	require.Equal(t, 1, issues.mutations)
}

func TestRedeemCodePassesOnlyFingerprintAndUserCorrelation(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	codes := &codeRepositoryFake{}
	service := newIssuanceService(t, &campaignReaderFake{campaign: activeCampaign(now)}, &issueRepositoryFake{}, codes, &userCouponRepositoryFake{}, &approvalPortFake{}, &casePortFake{})

	result, err := service.RedeemCode(context.Background(), RedeemCodeInput{Metadata: issuanceMetadata(now, "redeem"), UserID: "user-1", Code: " abcd-1234 "})
	require.NoError(t, err)
	require.NotEmpty(t, result.IssueRequestID)
	require.Equal(t, "user-1", codes.reserveUserID)
	require.Len(t, codes.reserveHash, sha256Size)
	require.False(t, bytes.Contains(codes.reserveHash, []byte("ABCD-1234")))
	require.NotContains(t, codes.commands[0].RequestHash, "ABCD-1234")
}

func TestRedeemCodeRejectsCampaignAndUserPolicyBeforeReservation(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		campaign    campaign.Campaign
		eligibility ports.UserEligibility
		controls    []operations.Control
		wantReason  string
	}{
		{
			name: "inactive campaign", campaign: func() campaign.Campaign {
				value := activeCampaign(now)
				value.Status = campaign.StatusHeld
				return value
			}(),
			eligibility: ports.UserEligibility{Eligible: true, Snapshot: issuanceSnapshot(now)},
			wantReason:  "campaign_inactive",
		},
		{
			name: "ineligible user", campaign: activeCampaign(now),
			eligibility: ports.UserEligibility{Eligible: false, Snapshot: issuanceSnapshot(now)},
			wantReason:  "user_ineligible",
		},
		{
			name: "operational stop", campaign: activeCampaign(now),
			eligibility: ports.UserEligibility{Eligible: true, Snapshot: issuanceSnapshot(now)},
			controls: []operations.Control{{
				Active: true, BlockIssuance: true, EffectiveFrom: now.Add(-time.Minute),
			}},
			wantReason: "issuance_blocked",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			codes := &codeRepositoryFake{}
			service := newIssuanceServiceWithAdmission(t,
				&campaignReaderFake{campaign: test.campaign}, &issueRepositoryFake{}, codes,
				&userCouponRepositoryFake{}, &approvalPortFake{}, &casePortFake{},
				&eligibilityPortFake{result: test.eligibility},
				&operationalControlReaderFake{controls: test.controls},
			)

			result, err := service.RedeemCode(context.Background(), RedeemCodeInput{
				Metadata: issuanceMetadata(now, "redeem-"+test.name), UserID: "user-1", Code: "ABCD-1234",
			})
			require.NoError(t, err)
			require.True(t, result.Rejected)
			require.Equal(t, test.wantReason, result.ReasonCode)
			require.Equal(t, []string{"find_by_hash", "reject"}, codes.calls)
		})
	}
}

func TestCompensationMapsToOperatorGrantAndVerifiesApprovalAndCase(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	issues := &issueRepositoryFake{}
	approvals := &approvalPortFake{}
	cases := &casePortFake{}
	service := newIssuanceService(t, &campaignReaderFake{campaign: activeCampaign(now)}, issues, &codeRepositoryFake{}, &userCouponRepositoryFake{}, approvals, cases)

	_, err := service.CreateCompensationIssueRequest(context.Background(), CreateCompensationIssueRequestInput{
		Metadata: issuanceMetadata(now, "compensation"), CampaignID: "campaign-1", UserID: "user-1",
		SourceRef:  shared.ExternalRef{Context: "cs", Type: "compensation_task", ID: "compensation-task-1"},
		ReasonCode: "SERVICE_RECOVERY", CaseRef: "case-1",
		ApprovalPolicy: issuanceSnapshot(now),
	})
	require.NoError(t, err)
	require.Equal(t, issuerequest.SourceOperatorGrant, issues.created.SourceType)
	require.Equal(t, []string{CommandCreateIssueRequest}, approvals.operations)
	require.Equal(t, []string{"case-1"}, cases.refs)
}

func TestCompensationAllowsApprovalPolicyWithoutApprovalReference(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	issues := &issueRepositoryFake{}
	approvals := &approvalPortFake{}
	cases := &casePortFake{}
	service := newIssuanceService(t, &campaignReaderFake{campaign: activeCampaign(now)}, issues, &codeRepositoryFake{}, &userCouponRepositoryFake{}, approvals, cases)
	metadata := issuanceMetadata(now, "compensation-without-approval")
	metadata.ApprovalRef = ""

	_, err := service.CreateCompensationIssueRequest(context.Background(), CreateCompensationIssueRequestInput{
		Metadata: metadata, CampaignID: "campaign-1", UserID: "user-1",
		SourceRef:  shared.ExternalRef{Context: "cs", Type: "incident", ID: "incident-1"},
		ReasonCode: "SERVICE_RECOVERY", CaseRef: "case-1", ApprovalPolicy: issuanceSnapshot(now),
	})
	require.NoError(t, err)
	require.Empty(t, approvals.operations)
	require.Equal(t, []string{"case-1"}, cases.refs)
}

func TestGeneratedIDsAndHashesAreStableAcrossDeliveryMetadata(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	issues := &issueRepositoryFake{}
	service := newIssuanceService(t, &campaignReaderFake{campaign: activeCampaign(now)}, issues, &codeRepositoryFake{}, &userCouponRepositoryFake{}, &approvalPortFake{}, &casePortFake{})
	first := ClaimInput{Metadata: issuanceMetadata(now, "claim"), CampaignID: "campaign-1", UserID: "user-1"}
	second := first
	second.Metadata.CommandID = "command-2"
	second.Metadata.CorrelationID = "corr-2"
	second.Metadata.OccurredAt = now.Add(time.Minute)
	second.Metadata.LeaseUntil = second.Metadata.OccurredAt.Add(time.Minute)
	second.Metadata.ExpiresAt = second.Metadata.OccurredAt.Add(24 * time.Hour)

	firstResult, err := service.Claim(context.Background(), first)
	require.NoError(t, err)
	firstHash := issues.commands[0].RequestHash
	secondResult, err := service.Claim(context.Background(), second)
	require.NoError(t, err)
	require.Equal(t, firstResult.IssueRequestID, secondResult.IssueRequestID)
	require.Regexp(t, regexp.MustCompile(`^ireq_[A-Za-z0-9_-]{8,120}$`), firstResult.IssueRequestID)
	require.Equal(t, firstHash, issues.commands[1].RequestHash)
}

func TestCommandMetadataRequiresFiniteLeaseAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	codes := &codeRepositoryFake{}
	service := newIssuanceService(t, &campaignReaderFake{campaign: activeCampaign(now)}, &issueRepositoryFake{}, codes, &userCouponRepositoryFake{}, &approvalPortFake{}, &casePortFake{})
	input := RedeemCodeInput{Metadata: issuanceMetadata(now, "redeem"), UserID: "user-1", Code: "ABCD-1234"}
	input.Metadata.ExpiresAt = input.Metadata.LeaseUntil

	_, err := service.RedeemCode(context.Background(), input)
	require.Error(t, err)
	require.Empty(t, codes.calls)
}

func TestIssueUserCouponGeneratesContractID(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	users := &userCouponRepositoryFake{}
	service := newIssuanceService(t, &campaignReaderFake{campaign: activeCampaign(now)}, &issueRepositoryFake{current: processingRequest(t, now)}, &codeRepositoryFake{}, users, &approvalPortFake{}, &casePortFake{})

	result, err := service.IssueUserCoupon(context.Background(), IssueUserCouponInput{Metadata: issuanceMetadata(now, "issue"), IssueRequestID: "issue-1", ExpectedIssueRequestVersion: 2})
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`^ucpn_[A-Za-z0-9_-]{8,120}$`), result.UserCouponID)
	var snapshot map[string]any
	require.NoError(t, json.Unmarshal(users.granted.GrantSnapshot, &snapshot))
	require.Equal(t, "Campaign", snapshot["displayName"])
	require.Equal(t, "fixed_amount", snapshot["benefit"].(map[string]any)["type"])
	require.Equal(t, float64(1), snapshot["applicability"].(map[string]any)["policySchemaVersion"])
	require.Len(t, snapshot["applicability"].(map[string]any)["includeTargets"], 1)
	require.NotContains(t, string(users.granted.GrantSnapshot), "approvalRef")
	require.Contains(t, snapshot, "policySnapshot")
}

func TestIssueUserCouponReplaysCompletedRequestWithoutNewGrant(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	request := processingRequest(t, now)
	request.Status = issuerequest.StatusCompleted
	request.UserCouponID = "ucpn_existing1"
	existing := usercoupon.Coupon{ID: request.UserCouponID, IssueRequestID: request.ID, ResultRef: "user_coupon:ucpn_existing1:granted"}
	issues := &issueRepositoryFake{current: request}
	users := &userCouponRepositoryFake{existing: existing}
	service := newIssuanceService(t, &campaignReaderFake{campaign: activeCampaign(now)}, issues, &codeRepositoryFake{}, users, &approvalPortFake{}, &casePortFake{})

	result, err := service.IssueUserCoupon(context.Background(), IssueUserCouponInput{Metadata: issuanceMetadata(now, "completed-replay"), IssueRequestID: request.ID, ExpectedIssueRequestVersion: request.Version})
	require.NoError(t, err)
	require.True(t, result.Replayed)
	require.Equal(t, existing.ID, result.UserCouponID)
	require.Equal(t, []string{"get_by_issue_request"}, users.calls)
	require.Zero(t, users.mutations)
}

const sha256Size = 32

type campaignReaderFake struct {
	campaign campaign.Campaign
	calls    int
}

func (f *campaignReaderFake) GetEffective(context.Context, string, time.Time) (campaign.Campaign, error) {
	f.calls++
	return f.campaign, nil
}

type issueRepositoryFake struct {
	calls     []string
	commands  []issuerequest.Command
	mutations int
	created   issuerequest.Request
	current   issuerequest.Request
}

func (f *issueRepositoryFake) Create(_ context.Context, request issuerequest.Request, _ issuerequest.Admission, command issuerequest.Command) (issuerequest.Mutation, error) {
	f.calls = append(f.calls, "create")
	f.commands = append(f.commands, command)
	f.mutations++
	f.created = request
	request.ResultRef = "issue_request:" + request.ID + ":accepted"
	return issuerequest.Mutation{Request: request, ResultRef: request.ResultRef}, nil
}

func (f *issueRepositoryFake) Get(context.Context, string) (issuerequest.Request, error) {
	f.calls = append(f.calls, "get")
	return f.current, nil
}

func (f *issueRepositoryFake) FindDue(context.Context, time.Time, int) ([]issuerequest.Request, error) {
	return nil, nil
}

func (f *issueRepositoryFake) MarkPending(_ context.Context, id string, _ int64, command issuerequest.Command) (issuerequest.Mutation, error) {
	return f.transition("mark_pending", id, issuerequest.StatusPending, command), nil
}

func (f *issueRepositoryFake) MarkProcessing(_ context.Context, id string, _ int64, command issuerequest.Command) (issuerequest.Mutation, error) {
	return f.transition("mark_processing", id, issuerequest.StatusProcessing, command), nil
}

func (f *issueRepositoryFake) RecordFailure(_ context.Context, id string, _ int64, _ string, _ bool, _ *time.Time, command issuerequest.Command) (issuerequest.Mutation, error) {
	return f.transition("record_failure", id, issuerequest.StatusFailedRetryable, command), nil
}

func (f *issueRepositoryFake) Retry(_ context.Context, id string, _ int64, _ time.Time, command issuerequest.Command) (issuerequest.Mutation, error) {
	return f.transition("retry", id, issuerequest.StatusRetryPending, command), nil
}

func (f *issueRepositoryFake) Reject(_ context.Context, id string, _ int64, _ string, command issuerequest.Command) (issuerequest.Mutation, error) {
	return f.transition("reject", id, issuerequest.StatusRejected, command), nil
}

func (f *issueRepositoryFake) Complete(_ context.Context, id string, _ int64, _ string, command issuerequest.Command) (issuerequest.Mutation, error) {
	return f.transition("complete", id, issuerequest.StatusCompleted, command), nil
}

func (f *issueRepositoryFake) FinalizeFailure(_ context.Context, id string, _ int64, _ string, command issuerequest.Command) (issuerequest.Mutation, error) {
	return f.transition("finalize_failure", id, issuerequest.StatusFailedFinal, command), nil
}

func (f *issueRepositoryFake) transition(call, id string, status issuerequest.Status, command issuerequest.Command) issuerequest.Mutation {
	f.calls = append(f.calls, call)
	f.commands = append(f.commands, command)
	f.mutations++
	return issuerequest.Mutation{Request: issuerequest.Request{ID: id, Status: status}, ResultRef: "issue_request:" + id + ":" + string(status)}
}

type codeRepositoryFake struct {
	calls         []string
	commands      []couponcode.Command
	mutations     int
	reserveHash   []byte
	reserveUserID string
}

func (f *codeRepositoryFake) FindByHash(context.Context, []byte) (couponcode.Code, error) {
	f.calls = append(f.calls, "find_by_hash")
	return couponcode.Code{ID: "code-1", BatchID: "batch-1", CampaignID: "campaign-1", Status: couponcode.CodeAvailable}, nil
}

func (f *codeRepositoryFake) Reserve(_ context.Context, hash []byte, userID, issueRequestID string, until time.Time, command couponcode.Command) (couponcode.Mutation, error) {
	f.calls = append(f.calls, "reserve")
	f.commands = append(f.commands, command)
	f.mutations++
	f.reserveHash = append([]byte(nil), hash...)
	f.reserveUserID = userID
	return couponcode.Mutation{
		Code:         couponcode.Code{ID: "code-1", CampaignID: "campaign-1", Status: couponcode.CodeReserved, ReservedIssueRequestID: issueRequestID, ReservedUntil: &until},
		BatchVersion: 1, ResultRef: "code:code-1:reserved",
	}, nil
}

func (f *codeRepositoryFake) Reject(_ context.Context, _ []byte, _ string, issueRequestID, reasonCode string, command couponcode.Command) (couponcode.Mutation, error) {
	f.calls = append(f.calls, "reject")
	f.commands = append(f.commands, command)
	f.mutations++
	return couponcode.Mutation{
		Code: couponcode.Code{
			ID: "code-1", BatchID: "batch-1", CampaignID: "campaign-1", Status: couponcode.CodeAvailable,
		},
		BatchVersion: 1, ResultRef: "code:code-1:rejected:" + issueRequestID,
		Rejected: true, ReasonCode: reasonCode,
	}, nil
}

func (f *codeRepositoryFake) Confirm(_ context.Context, codeID, issueRequestID, userCouponID string, _ int64, command couponcode.Command) (couponcode.Mutation, error) {
	f.calls = append(f.calls, "confirm")
	f.commands = append(f.commands, command)
	f.mutations++
	return couponcode.Mutation{Code: couponcode.Code{ID: codeID, ReservedIssueRequestID: issueRequestID, RedeemedUserCouponID: userCouponID, Status: couponcode.CodeRedeemed}, ResultRef: "code:" + codeID + ":redeemed"}, nil
}

func (f *codeRepositoryFake) Release(_ context.Context, codeID, issueRequestID string, _ int64, command couponcode.Command) (couponcode.Mutation, error) {
	f.calls = append(f.calls, "release")
	f.commands = append(f.commands, command)
	f.mutations++
	return couponcode.Mutation{Code: couponcode.Code{ID: codeID, ReservedIssueRequestID: issueRequestID, Status: couponcode.CodeAvailable}, ResultRef: "code:" + codeID + ":released"}, nil
}

type userCouponRepositoryFake struct {
	calls     []string
	commands  []usercoupon.Command
	mutations int
	existing  usercoupon.Coupon
	granted   usercoupon.Coupon
}

func (f *userCouponRepositoryFake) Grant(_ context.Context, coupon usercoupon.Coupon, command usercoupon.Command) (usercoupon.Mutation, error) {
	f.calls = append(f.calls, "grant")
	f.commands = append(f.commands, command)
	f.mutations++
	f.granted = coupon
	return usercoupon.Mutation{Coupon: coupon, ResultRef: coupon.ResultRef}, nil
}

func (f *userCouponRepositoryFake) Get(context.Context, string) (usercoupon.Coupon, error) {
	return usercoupon.Coupon{}, nil
}

func (f *userCouponRepositoryFake) GetByIssueRequest(context.Context, string) (usercoupon.Coupon, error) {
	f.calls = append(f.calls, "get_by_issue_request")
	return f.existing, nil
}

func (f *userCouponRepositoryFake) FindExpirable(context.Context, time.Time, int) ([]usercoupon.Coupon, error) {
	return nil, nil
}

func (f *userCouponRepositoryFake) Expire(context.Context, string, int64, time.Time, usercoupon.Command) (usercoupon.Mutation, error) {
	return usercoupon.Mutation{}, nil
}

type approvalPortFake struct {
	operations []string
}

func (f *approvalPortFake) VerifyApproval(_ context.Context, _ string, operation string) error {
	f.operations = append(f.operations, operation)
	return nil
}

type casePortFake struct {
	refs []string
}

type eligibilityPortFake struct {
	result ports.UserEligibility
	calls  int
}

func (f *eligibilityPortFake) Snapshot(context.Context, string, time.Time) (ports.UserEligibility, error) {
	f.calls++
	return f.result, nil
}

type operationalControlReaderFake struct {
	controls []operations.Control
	calls    int
}

func (f *operationalControlReaderFake) FindEffective(context.Context, operations.Scope, time.Time) ([]operations.Control, error) {
	f.calls++
	return f.controls, nil
}

func (f *casePortFake) VerifyCase(_ context.Context, ref string, _ ports.CSCaseBinding) error {
	f.refs = append(f.refs, ref)
	return nil
}

type capturedCommand struct {
	operation, requestHash, correlationID, causationID string
}

func capturedOperation(issue []issuerequest.Command, code []couponcode.Command, user []usercoupon.Command) capturedCommand {
	if len(issue) > 0 {
		return capturedCommand{issue[0].OperationType, issue[0].RequestHash, issue[0].CorrelationID, issue[0].CausationID}
	}
	if len(code) > 0 {
		return capturedCommand{code[0].OperationType, code[0].RequestHash, code[0].CorrelationID, code[0].CausationID}
	}
	return capturedCommand{user[0].OperationType, user[0].RequestHash, user[0].CorrelationID, user[0].CausationID}
}

func newIssuanceService(t *testing.T, campaigns CampaignReader, issues issuerequest.Repository, codes couponcode.Repository, users usercoupon.Repository, approvals ports.OperationApprovalPort, cases ports.CSCasePort) *Service {
	return newIssuanceServiceWithAdmission(t, campaigns, issues, codes, users, approvals, cases,
		&eligibilityPortFake{result: ports.UserEligibility{Eligible: true, Snapshot: issuanceSnapshot(time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC))}},
		&operationalControlReaderFake{})
}

func newIssuanceServiceWithAdmission(t *testing.T, campaigns CampaignReader, issues issuerequest.Repository, codes couponcode.Repository, users usercoupon.Repository, approvals ports.OperationApprovalPort, cases ports.CSCasePort, eligibility ports.UserEligibilityPort, controls OperationalControlReader) *Service {
	t.Helper()
	service, err := New(Dependencies{
		Campaigns: campaigns, IssueRequests: issues, Codes: codes, UserCoupons: users,
		Approvals: approvals, Cases: cases, UserEligibility: eligibility, OperationalControl: controls,
		CodeHashKey: []byte("01234567890123456789012345678901"), CodeReservationTTL: 5 * time.Minute,
	})
	require.NoError(t, err)
	return service
}

func issuanceMetadata(now time.Time, key string) CommandMetadata {
	return CommandMetadata{
		CommandID: "command-1", BusinessKey: key, CorrelationID: "corr-1",
		TraceID: "trace-1", ApprovalRef: "approval-1", OccurredAt: now,
		LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
	}
}

func activeCampaign(now time.Time) campaign.Campaign {
	claimStart := now.Add(-time.Hour)
	claimEnd := now.Add(time.Hour)
	return campaign.Campaign{
		ID: "campaign-1", DisplayName: "Campaign", Status: campaign.StatusActive,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(24 * time.Hour), CurrentPolicyVersion: 3,
		TotalQuantity: 100, PerUserLimit: 1, ClaimStartsAt: &claimStart, ClaimEndsAt: &claimEnd,
		IssuerAndFunding: shared.IssuerAndFunding{
			IssuerType: "platform", IssuerRef: shared.ExternalRef{Context: "operator", Type: "workload", ID: "operator-1"}, FunderType: "platform", ApprovalRef: "private-approval",
		},
		ApprovalRef: "campaign-approval-1", OwnerSnapshot: issuanceSnapshot(now), Version: 4,
		Benefits: []campaign.Benefit{{
			ID: "benefit-1", PolicyVersion: 3, Type: campaign.BenefitFixedAmount,
			Amount: &shared.Money{Amount: "5000", Currency: "KRW"}, Currency: "KRW",
		}},
		Applicability: []campaign.ApplicabilityPolicy{{
			ID: "policy-1", PolicyVersion: 3, TargetType: "seller", TargetRef: "seller-1", Inclusion: "include",
			ConditionType: "all", ConditionValue: json.RawMessage(`{}`), EffectiveFrom: now.Add(-time.Hour), SnapshotLabel: "snapshot-1",
		}},
	}
}

func processingRequest(t *testing.T, now time.Time) issuerequest.Request {
	t.Helper()
	current := activeCampaign(now)
	funding, err := freezeSnapshot(current.Version, current.IssuerAndFunding)
	require.NoError(t, err)
	policy, err := freezeSnapshot(current.Version, campaignPolicySnapshot{
		CampaignID: current.ID, CampaignVersion: current.Version, PolicyVersion: current.CurrentPolicyVersion,
		DisplayName: current.DisplayName, PerUserLimit: current.PerUserLimit,
		StartsAt: current.StartsAt, EndsAt: current.EndsAt, Benefits: current.Benefits,
		Applicability: current.Applicability, OwnerSnapshot: current.OwnerSnapshot,
	})
	require.NoError(t, err)
	return issuerequest.Request{
		ID: "issue-1", CampaignID: current.ID, UserID: "user-1", BusinessKey: "issue",
		SourceType: issuerequest.SourceClaim, SourceRef: "claim:issue-1", Status: issuerequest.StatusProcessing,
		IssuerAndFundingSnapshot: funding, PolicySnapshot: policy, Version: 2,
	}
}

func issuanceSnapshot(now time.Time) shared.SnapshotRef {
	return shared.SnapshotRef{
		SourceRef:     shared.ExternalRef{Context: "seller", Type: "catalog", ID: "seller-1"},
		SourceVersion: "v1", CapturedAt: now, PayloadHash: "sha256:" + strings.Repeat("b", 43),
	}
}

var _ CampaignReader = (*campaignReaderFake)(nil)
var _ OperationalControlReader = (*operationalControlReaderFake)(nil)
var _ issuerequest.Repository = (*issueRepositoryFake)(nil)
var _ couponcode.Repository = (*codeRepositoryFake)(nil)
var _ usercoupon.Repository = (*userCouponRepositoryFake)(nil)
