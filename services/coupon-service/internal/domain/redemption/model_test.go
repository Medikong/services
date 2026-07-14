package redemption

import (
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/stretchr/testify/require"
)

func TestRedemptionStateMachine(t *testing.T) {
	now := time.Now().UTC()
	redemption, _, err := NewEvaluation(Evaluation{
		RedemptionID: "redm_12345678", UserCouponID: "ucpn_12345678", CampaignID: "camp_12345678",
		UserID: "usr_1", OrderID: "order:1", BusinessKey: "validate:1", Eligible: true,
		PolicyVersion: 1, OrderSnapshot: map[string]any{"ref": "snapshot:1"}, OrderSnapshotHash: "sha256:test",
		Discount: shared.Money{Amount: "1000", Currency: "KRW"}, FinalOrderAmount: shared.Money{Amount: "9000", Currency: "KRW"},
		CostShares: []CostShare{{BearerType: "platform", Amount: shared.Money{Amount: "1000", Currency: "KRW"}}}, EvaluatedAt: now,
	})
	require.NoError(t, err)
	_, err = redemption.Reserve(0, now.Add(time.Minute), now)
	require.NoError(t, err)
	resultRef := shared.ExternalRef{Context: "payment", Type: "confirmation", ID: "payment:1"}
	events, err := redemption.Confirm(1, resultRef, nil, "payment_confirmed", now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, StatusConfirmed, redemption.Status)
	_, err = redemption.Release(2, resultRef, nil, "late_release", now.Add(2*time.Second))
	require.Error(t, err)
}

func TestCostAttributionMustMatchDiscount(t *testing.T) {
	err := validateAmounts(
		shared.Money{Amount: "1000", Currency: "KRW"},
		shared.Money{Amount: "9000", Currency: "KRW"},
		[]CostShare{{BearerType: "platform", Amount: shared.Money{Amount: "999", Currency: "KRW"}}},
	)
	require.Error(t, err)
}

func TestReplayRequestRequiresImmutableCorrelationAndOperationPayload(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	resultRef := shared.ExternalRef{Context: "payment", Type: "confirmation", ID: "payment:1"}
	request := ReplayRequest{
		RecoveryID: "rcvy_12345678", AttemptID: "att_12345678", BusinessKey: "order:coupon:confirm",
		RedemptionID: "redm_12345678", Operation: ReplayConfirm, ExpectedVersion: 1,
		ResultRef: &resultRef, ReasonCode: "payment_confirmed", ReplayedAt: now,
	}
	require.NoError(t, request.Validate())

	request.RedemptionID = ""
	require.Error(t, request.Validate())
	request.RedemptionID = "redm_12345678"
	request.Operation = ReplayReserve
	request.ResultRef = nil
	require.Error(t, request.Validate())
	deadline := now.Add(time.Minute)
	request.ReservedUntil = &deadline
	require.NoError(t, request.Validate())
}
