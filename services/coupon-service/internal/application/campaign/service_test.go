package campaignapp

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

func TestServiceRoutesEachCampaignCommand(t *testing.T) {
	now := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		operation string
		wantCalls []string
		run       func(*Service) error
	}{
		{
			name: "register policy", operation: CommandRegisterPolicy, wantCalls: []string{"create"},
			run: func(service *Service) error {
				_, err := service.RegisterPolicy(context.Background(), validRegisterInput(now))
				return err
			},
		},
		{
			name: "configure first come", operation: CommandConfigureFirstComeLimit, wantCalls: []string{"configure"},
			run: func(service *Service) error {
				_, err := service.ConfigureFirstComeLimit(context.Background(), ConfigureFirstComeLimitInput{
					Metadata: metadata(now, "configure"), CampaignID: "campaign-1", ExpectedVersion: 0,
					Limit: campaign.QuantityLimit{TotalQuantity: 10, PerUserLimit: 1, ClaimStartsAt: now.Add(time.Hour), ClaimEndsAt: now.Add(2 * time.Hour)},
				})
				return err
			},
		},
		{
			name: "review seller coupon", operation: CommandReviewSellerCoupon, wantCalls: []string{"review"},
			run: func(service *Service) error {
				_, err := service.ReviewSellerCoupon(context.Background(), ReviewSellerCouponInput{
					Metadata: metadata(now, "review"), CampaignID: "campaign-1", ExpectedVersion: 1,
					Decision: campaign.StatusHeld, ReasonCode: "NEEDS_EVIDENCE", SellerOwnershipSnapshot: validSnapshot(now),
				})
				return err
			},
		},
		{
			name: "change policy", operation: CommandChangePolicy, wantCalls: []string{"get", "change_policy"},
			run: func(service *Service) error {
				benefit := validBenefit()
				benefit.Amount = &shared.Money{Amount: "7000", Currency: "KRW"}
				_, err := service.ChangePolicy(context.Background(), ChangePolicyInput{
					Metadata: metadata(now, "change"), CampaignID: "campaign-1", ExpectedVersion: 2,
					EffectiveAt: now.Add(time.Hour), Benefits: []campaign.Benefit{benefit},
				})
				return err
			},
		},
		{
			name: "reserve quantity", operation: CommandReserveQuantity, wantCalls: []string{"reserve_quantity"},
			run: func(service *Service) error {
				_, err := service.ReserveQuantity(context.Background(), ReserveQuantityInput{
					Metadata: metadata(now, "reserve"), CampaignID: "campaign-1", IssueRequestID: "issue-1", Quantity: 1, ExpectedVersion: 3,
				})
				return err
			},
		},
		{
			name: "confirm quantity", operation: CommandConfirmQuantity, wantCalls: []string{"confirm_quantity"},
			run: func(service *Service) error {
				_, err := service.ConfirmQuantity(context.Background(), DecideQuantityInput{
					Metadata: metadata(now, "confirm"), CampaignID: "campaign-1", IssueRequestID: "issue-1", ExpectedVersion: 4,
				})
				return err
			},
		},
		{
			name: "release quantity", operation: CommandReleaseQuantity, wantCalls: []string{"release_quantity"},
			run: func(service *Service) error {
				_, err := service.ReleaseQuantity(context.Background(), DecideQuantityInput{
					Metadata: metadata(now, "release"), CampaignID: "campaign-1", IssueRequestID: "issue-1", ExpectedVersion: 4,
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &campaignRepositoryFake{current: validCampaign(now)}
			approval := &approvalFake{}
			seller := &sellerSnapshotFake{}
			service, err := New(Dependencies{Repository: repository, SellerSnapshots: seller, Approvals: approval})
			require.NoError(t, err)

			require.NoError(t, test.run(service))
			require.Equal(t, test.wantCalls, repository.calls)
			require.Len(t, repository.commands, 1)
			require.Equal(t, test.operation, repository.commands[0].OperationType)
			require.Equal(t, "corr-1", repository.commands[0].CorrelationID)
			require.Equal(t, "command-1", repository.commands[0].CausationID)
			require.True(t, strings.HasPrefix(repository.commands[0].RequestHash, "sha256:"))
		})
	}
}

func TestCommandHashAndGeneratedIDsAreStableAcrossDeliveryMetadata(t *testing.T) {
	now := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	repository := &campaignRepositoryFake{current: validCampaign(now)}
	service, err := New(Dependencies{Repository: repository, SellerSnapshots: &sellerSnapshotFake{}, Approvals: &approvalFake{}})
	require.NoError(t, err)

	first := validRegisterInput(now)
	second := first
	second.Metadata.CommandID = "command-2"
	second.Metadata.CorrelationID = "corr-2"
	second.Metadata.OccurredAt = now.Add(time.Minute)
	second.Metadata.LeaseUntil = second.Metadata.OccurredAt.Add(time.Minute)
	second.Metadata.ExpiresAt = second.Metadata.OccurredAt.Add(24 * time.Hour)
	_, err = service.RegisterPolicy(context.Background(), first)
	require.NoError(t, err)
	_, err = service.RegisterPolicy(context.Background(), second)
	require.NoError(t, err)

	require.Len(t, repository.created, 2)
	require.Equal(t, repository.created[0].ID, repository.created[1].ID)
	require.Regexp(t, regexp.MustCompile(`^camp_[A-Za-z0-9_-]{8,120}$`), repository.created[0].ID)
	require.Equal(t, repository.commands[0].RequestHash, repository.commands[1].RequestHash)
}

func TestCommandMetadataRequiresFiniteLeaseAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	repository := &campaignRepositoryFake{current: validCampaign(now)}
	service, err := New(Dependencies{Repository: repository, SellerSnapshots: &sellerSnapshotFake{}, Approvals: &approvalFake{}})
	require.NoError(t, err)
	input := ConfigureFirstComeLimitInput{
		Metadata: metadata(now, "configure"), CampaignID: "campaign-1", ExpectedVersion: 0,
		Limit: campaign.QuantityLimit{TotalQuantity: 10, PerUserLimit: 1, ClaimStartsAt: now.Add(time.Hour), ClaimEndsAt: now.Add(2 * time.Hour)},
	}
	input.Metadata.LeaseUntil = time.Time{}

	_, err = service.ConfigureFirstComeLimit(context.Background(), input)
	require.Error(t, err)
	require.Empty(t, repository.calls)
}

func TestRegisterAndReviewVerifyExternalEvidenceBeforeMutation(t *testing.T) {
	now := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	repository := &campaignRepositoryFake{current: validCampaign(now)}
	approval := &approvalFake{}
	seller := &sellerSnapshotFake{}
	service, err := New(Dependencies{Repository: repository, SellerSnapshots: seller, Approvals: approval})
	require.NoError(t, err)

	_, err = service.RegisterPolicy(context.Background(), validRegisterInput(now))
	require.NoError(t, err)
	_, err = service.ReviewSellerCoupon(context.Background(), ReviewSellerCouponInput{
		Metadata: metadata(now, "review"), CampaignID: "campaign-1", ExpectedVersion: 1,
		Decision: campaign.StatusHeld, ReasonCode: "NEEDS_EVIDENCE", SellerOwnershipSnapshot: validSnapshot(now),
	})
	require.NoError(t, err)
	require.Equal(t, []string{CommandRegisterPolicy, CommandReviewSellerCoupon}, approval.operations)
	require.Equal(t, 2, seller.calls)
}

func TestChangePolicyCarriesOwnerSnapshotAndReversionsApplicability(t *testing.T) {
	now := time.Date(2026, 7, 12, 3, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		applicability []campaign.ApplicabilityPolicy
	}{
		{name: "copied current applicability"},
		{name: "new applicability", applicability: []campaign.ApplicabilityPolicy{{
			TargetType: "drop", TargetRef: "drop-2", Inclusion: "include", ConditionType: "all", ConditionValue: json.RawMessage(`{}`),
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &campaignRepositoryFake{current: validCampaign(now)}
			service, err := New(Dependencies{Repository: repository, SellerSnapshots: &sellerSnapshotFake{}, Approvals: &approvalFake{}})
			require.NoError(t, err)
			effectiveAt := now.Add(time.Hour)
			_, err = service.ChangePolicy(context.Background(), ChangePolicyInput{
				Metadata: metadata(now, "change-policy"), CampaignID: repository.current.ID,
				ExpectedVersion: repository.current.Version, EffectiveAt: effectiveAt,
				Benefits: []campaign.Benefit{validBenefit()}, Applicability: test.applicability,
			})
			require.NoError(t, err)
			require.NotEmpty(t, repository.changedPolicy.Applicability)
			for _, policy := range repository.changedPolicy.Applicability {
				require.Equal(t, int64(2), policy.PolicyVersion)
				require.Equal(t, effectiveAt, policy.EffectiveFrom)
				if test.name == "new applicability" {
					require.Equal(t, repository.current.OwnerSnapshot.PayloadHash, policy.SnapshotLabel)
				}
			}
			require.NotEqual(t, repository.current.Benefits[0].ID, repository.changedPolicy.Benefits[0].ID)
			require.NotEqual(t, repository.current.Applicability[0].ID, repository.changedPolicy.Applicability[0].ID)
		})
	}
}

type campaignRepositoryFake struct {
	calls         []string
	commands      []campaign.Command
	created       []campaign.Campaign
	current       campaign.Campaign
	changedPolicy campaign.PolicyVersion
}

func (f *campaignRepositoryFake) Create(_ context.Context, value campaign.Campaign, command campaign.Command) (campaign.Mutation, error) {
	f.calls = append(f.calls, "create")
	f.commands = append(f.commands, command)
	f.created = append(f.created, value)
	return campaign.Mutation{ResultRef: "campaign:" + value.ID, ResponseSnapshot: json.RawMessage(`{"status":"under_review"}`)}, nil
}

func (f *campaignRepositoryFake) Get(context.Context, string) (campaign.Campaign, error) {
	f.calls = append(f.calls, "get")
	return f.current, nil
}

func (f *campaignRepositoryFake) ConfigureIssuance(_ context.Context, _ string, _ int64, _ campaign.QuantityLimit, command campaign.Command) (campaign.Mutation, error) {
	f.calls = append(f.calls, "configure")
	f.commands = append(f.commands, command)
	return campaign.Mutation{ResultRef: "campaign:configured"}, nil
}

func (f *campaignRepositoryFake) Review(_ context.Context, _ string, _ int64, _ campaign.Status, _ string, command campaign.Command) (campaign.Mutation, error) {
	f.calls = append(f.calls, "review")
	f.commands = append(f.commands, command)
	return campaign.Mutation{ResultRef: "campaign:reviewed"}, nil
}

func (f *campaignRepositoryFake) AddPolicyVersion(_ context.Context, _ string, _ int64, policy campaign.PolicyVersion, command campaign.Command) (campaign.Mutation, error) {
	f.calls = append(f.calls, "change_policy")
	f.commands = append(f.commands, command)
	f.changedPolicy = policy
	return campaign.Mutation{ResultRef: "campaign:policy"}, nil
}

func (f *campaignRepositoryFake) ReserveQuantity(_ context.Context, campaignID, issueRequestID string, quantity, _ int64, at time.Time, command campaign.Command) (campaign.QuantityMutation, error) {
	f.calls = append(f.calls, "reserve_quantity")
	f.commands = append(f.commands, command)
	return campaign.QuantityMutation{Mutation: campaign.Mutation{ResultRef: "quantity:reserved"}, Reservation: campaign.QuantityReservation{CampaignID: campaignID, IssueRequestID: issueRequestID, Quantity: quantity, State: campaign.ReservationReserved, ReservedAt: at}}, nil
}

func (f *campaignRepositoryFake) ConfirmQuantity(_ context.Context, campaignID, issueRequestID string, _ int64, command campaign.Command) (campaign.QuantityMutation, error) {
	f.calls = append(f.calls, "confirm_quantity")
	f.commands = append(f.commands, command)
	return campaign.QuantityMutation{Mutation: campaign.Mutation{ResultRef: "quantity:confirmed"}, Reservation: campaign.QuantityReservation{CampaignID: campaignID, IssueRequestID: issueRequestID, State: campaign.ReservationConfirmed}}, nil
}

func (f *campaignRepositoryFake) ReleaseQuantity(_ context.Context, campaignID, issueRequestID string, _ int64, command campaign.Command) (campaign.QuantityMutation, error) {
	f.calls = append(f.calls, "release_quantity")
	f.commands = append(f.commands, command)
	return campaign.QuantityMutation{Mutation: campaign.Mutation{ResultRef: "quantity:released"}, Reservation: campaign.QuantityReservation{CampaignID: campaignID, IssueRequestID: issueRequestID, State: campaign.ReservationReleased}}, nil
}

type approvalFake struct {
	operations []string
}

func (f *approvalFake) VerifyApproval(_ context.Context, _ string, operation string) error {
	f.operations = append(f.operations, operation)
	return nil
}

type sellerSnapshotFake struct {
	calls int
}

func (f *sellerSnapshotFake) VerifySellerOwnership(context.Context, shared.SnapshotRef) error {
	f.calls++
	return nil
}

func metadata(now time.Time, key string) CommandMetadata {
	return CommandMetadata{
		CommandID: "command-1", BusinessKey: key, CorrelationID: "corr-1",
		TraceID: "trace-1", ApprovalRef: "approval-1", OccurredAt: now,
		LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
	}
}

func validRegisterInput(now time.Time) RegisterPolicyInput {
	return RegisterPolicyInput{
		Metadata: metadata(now, "register"), DisplayName: "Seller launch coupon",
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(24 * time.Hour),
		Benefits: []campaign.Benefit{validBenefit()},
		Applicability: []campaign.ApplicabilityPolicy{{
			TargetType: "seller", TargetRef: "seller-1", Inclusion: "include", ConditionType: "all",
			ConditionValue: json.RawMessage(`{}`),
		}},
		IssuerAndFunding: shared.IssuerAndFunding{
			IssuerType: "seller", IssuerRef: shared.ExternalRef{Context: "seller", Type: "seller", ID: "seller-1"},
			FunderType: "seller", FunderRef: &shared.ExternalRef{Context: "seller", Type: "seller", ID: "seller-1"},
		},
		OwnerSnapshot: validSnapshot(now), ExternalBusinessRef: "operation-1",
	}
}

func validCampaign(now time.Time) campaign.Campaign {
	claimStart := now.Add(-time.Hour)
	claimEnd := now.Add(time.Hour)
	return campaign.Campaign{
		ID: "campaign-1", DisplayName: "Campaign", Status: campaign.StatusActive,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), CurrentPolicyVersion: 1,
		TotalQuantity: 100, PerUserLimit: 1, ClaimStartsAt: &claimStart, ClaimEndsAt: &claimEnd,
		IssuerAndFunding: shared.IssuerAndFunding{
			IssuerType: "platform", IssuerRef: shared.ExternalRef{Context: "operator", Type: "workload", ID: "operator-1"}, FunderType: "platform",
		},
		OwnerSnapshot: validSnapshot(now), Version: 2, Benefits: []campaign.Benefit{validBenefit()},
		Applicability: []campaign.ApplicabilityPolicy{{
			ID: "policy-1", PolicyVersion: 1, TargetType: "seller", TargetRef: "seller-1", Inclusion: "include",
			ConditionType: "all", ConditionValue: json.RawMessage(`{}`), EffectiveFrom: now.Add(-time.Hour), SnapshotLabel: "snapshot-1",
		}},
	}
}

func validBenefit() campaign.Benefit {
	return campaign.Benefit{ID: "benefit-1", PolicyVersion: 1, Type: campaign.BenefitFixedAmount, Amount: &shared.Money{Amount: "5000", Currency: "KRW"}, Currency: "KRW"}
}

func validSnapshot(now time.Time) shared.SnapshotRef {
	return shared.SnapshotRef{
		SourceRef:     shared.ExternalRef{Context: "seller", Type: "catalog", ID: "seller-1"},
		SourceVersion: "v1", CapturedAt: now, PayloadHash: "sha256:" + strings.Repeat("a", 43),
	}
}

var _ ports.OperationApprovalPort = (*approvalFake)(nil)
var _ ports.SellerCatalogSnapshotPort = (*sellerSnapshotFake)(nil)
