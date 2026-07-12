package campaign

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

func TestCampaignQuantityRules(t *testing.T) {
	now := time.Now().UTC()
	claimStart := now.Add(-time.Hour)
	claimEnd := now.Add(time.Hour)
	campaign := Campaign{
		ID: "campaign-1", DisplayName: "Launch", Status: StatusActive, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
		CurrentPolicyVersion: 1, TotalQuantity: 10, ReservedQuantity: 4, ConfirmedQuantity: 5, PerUserLimit: 1,
		ClaimStartsAt: &claimStart, ClaimEndsAt: &claimEnd, Version: 1,
		IssuerAndFunding: shared.IssuerAndFunding{
			IssuerType: "platform", IssuerRef: shared.ExternalRef{Context: "coupon", Type: "platform", ID: "platform"},
			FunderType: "platform",
		},
		OwnerSnapshot: shared.SnapshotRef{SourceRef: shared.ExternalRef{Context: "catalog", Type: "drop", ID: "drop-12345678"}, SourceVersion: "1", CapturedAt: now, PayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		Benefits:      []Benefit{{ID: "benefit-1", PolicyVersion: 1, Type: BenefitFixedAmount, Amount: &shared.Money{Amount: "1000", Currency: "KRW"}, Currency: "KRW"}},
		Applicability: []ApplicabilityPolicy{{ID: "policy-1", PolicyVersion: 1, TargetType: "drop", TargetRef: "drop-1", Inclusion: "include", ConditionType: "all", ConditionValue: []byte(`{"schemaVersion":1}`), EffectiveFrom: now, SnapshotLabel: "v1"}},
	}
	require.NoError(t, campaign.Validate())
	require.NoError(t, campaign.CanReserve(1, now))
	require.ErrorIs(t, campaign.CanReserve(2, now), ErrQuantityUnavailable)
	campaign.Status = StatusApproved
	require.NoError(t, campaign.CanReserve(1, now))
	require.True(t, campaign.IsIssuableAt(now))
	campaign.Status = StatusUnderReview
	require.False(t, campaign.IsIssuableAt(now))
	campaign.Status = StatusApproved
	require.False(t, campaign.IsIssuableAt(campaign.EndsAt))
}

func TestReviewTransition(t *testing.T) {
	now := time.Now().UTC()
	claimEnd := now.Add(time.Hour)
	campaign := Campaign{Status: StatusUnderReview, Version: 3, TotalQuantity: 10, PerUserLimit: 1, ClaimStartsAt: &now, ClaimEndsAt: &claimEnd}
	approved, err := campaign.Review(StatusApproved)
	require.NoError(t, err)
	require.Equal(t, StatusApproved, approved.Status)
	require.EqualValues(t, 4, approved.Version)
	_, err = approved.Review(StatusRejected)
	require.ErrorIs(t, err, ErrInvalidTransition)
}

func TestPercentageBenefitRange(t *testing.T) {
	valid := Benefit{ID: "benefit-1", PolicyVersion: 1, Type: BenefitPercentage, Percentage: "12.50"}
	require.NoError(t, valid.Validate())
	valid.Percentage = "100.01"
	require.Error(t, valid.Validate())
	valid.Percentage = "not-a-number"
	require.Error(t, valid.Validate())
}
