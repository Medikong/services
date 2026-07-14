package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	campaignapp "github.com/Medikong/services/services/coupon-service/internal/application/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/application/commandworker"
	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	redemptionapp "github.com/Medikong/services/services/coupon-service/internal/application/redemption"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	domaineventing "github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCommandDispatcherRegistryMatchesDurableCoverage(t *testing.T) {
	want := append([]string(nil), commandworker.SupportedDocumentIDs...)
	sort.Strings(want)
	require.Equal(t, want, commandDispatcherDocumentIDs())

	dispatcher := &commandDispatcher{}
	for id, handler := range dispatcher.handlers() {
		require.False(t, reflect.ValueOf(handler).IsNil(), id)
	}
}

func TestCommandDispatcherNeverFallsBackToQueueTargetForMissingPayloadCorrelation(t *testing.T) {
	dispatcher := dispatcherFixture()
	eventID := uuid.New()
	raw := policyEnvelope(t, policy.Envelope{
		EventID: eventID, EventDocumentID: "EVT.A.19-36", EventType: "coupon.issue.pending",
		AggregateType: "CouponIssueRequest", AggregateID: "ireq_payload01", AggregateVersion: 1,
		OccurredAt: time.Now().UTC(), CorrelationID: "corr-1", PayloadSchemaVersion: 1,
		Data: map[string]any{"version": 1},
	})
	_, err := dispatcher.Dispatch(context.Background(), commandRequest(eventID, "CMD.A.19-07", "ireq_queue001", raw))
	require.Error(t, err)
	require.Contains(t, err.Error(), "correlation")
}

func TestCommandDispatcherPreservesRecoveryCorrelationAndMetadata(t *testing.T) {
	now := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	eventID := uuid.New()
	recoveryValue := recovery.Recovery{
		ID: "rcvy_12345678", RedemptionID: "redm_12345678", CurrentAttemptID: "att_12345678",
		BusinessKey: "order:coupon:confirm", OriginalOperationType: recovery.OperationConfirm,
		OriginalPayloadRef: "payload:immutable:1", OriginalPayloadHash: "sha256:abcdef",
	}
	replayer := &replayerFake{result: redemptionapp.RecoveryResultCommand{
		RecoveryID: recoveryValue.ID, AttemptID: recoveryValue.CurrentAttemptID,
		BusinessKey: recoveryValue.BusinessKey, Kind: recovery.ResultTransitioned, ResultRef: "payment:result:1",
	}}
	dispatcher := dispatcherFixture()
	dispatcher.redemptions = replayer
	dispatcher.recoveryReader = recoveryReaderFake{value: recoveryValue}
	dispatcher.now = func() time.Time { return now }
	raw := policyEnvelope(t, policy.Envelope{
		EventID: eventID, EventDocumentID: "EVT.A.19-39", EventType: "coupon.recovery.retry_pending",
		AggregateType: "CouponEventRecovery", AggregateID: recoveryValue.ID, AggregateVersion: 1,
		OccurredAt: now.Add(-time.Minute), CorrelationID: "corr-recovery", PayloadSchemaVersion: 1,
		Data: map[string]any{
			"recovery_id": recoveryValue.ID, "redemption_id": recoveryValue.RedemptionID,
			"attempt_id": recoveryValue.CurrentAttemptID, "business_key": recoveryValue.BusinessKey,
			"original_operation_type": recoveryValue.OriginalOperationType,
			"original_payload_ref":    recoveryValue.OriginalPayloadRef,
		},
	})
	request := commandRequest(eventID, "CMD.A.19-32", recoveryValue.RedemptionID, raw)
	request.BusinessKey = "POLICY.A.19-21:event:CMD.A.19-32"
	request.CorrelationID = "corr-recovery"
	request.TraceID = "trace-recovery"

	resultRef, err := dispatcher.Dispatch(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "payment:result:1", resultRef)
	require.Equal(t, recoveryValue.ID, replayer.input.RecoveryID)
	require.Equal(t, recoveryValue.RedemptionID, replayer.input.RedemptionID)
	require.Equal(t, recoveryValue.CurrentAttemptID, replayer.input.AttemptID)
	require.Equal(t, recoveryValue.BusinessKey, replayer.input.BusinessKey)
	require.Equal(t, recoveryValue.OriginalPayloadRef, replayer.input.OriginalPayloadRef)
	require.Equal(t, recoveryValue.OriginalPayloadHash, replayer.input.OriginalPayloadHash)
	require.Equal(t, request.BusinessKey, replayer.metadata.BusinessKey)
	require.Equal(t, eventID.String(), replayer.metadata.CausationID)
	require.Equal(t, "trace-recovery", replayer.metadata.TraceID)
}

func TestQuantityGateRejectionIsNotTheFinalDecision(t *testing.T) {
	gate := &quantityGateFake{admit: campaign.GateResult{Signal: campaign.GateRejected}}
	campaigns := &campaignServiceFake{reserveResult: campaignapp.QuantityResult{ResultRef: "quantity:db:reserved"}}
	dispatcher := quantityDispatcher(campaigns, gate, config.RedisFailureClosed, nil)

	resultRef, err := dispatcher.Dispatch(context.Background(), quantityRequest(t))
	require.NoError(t, err)
	require.Equal(t, "quantity:db:reserved", resultRef)
	require.Equal(t, 1, campaigns.reserveCalls)
	require.Equal(t, 1, gate.admitCalls)
	require.Zero(t, gate.completeCalls)
	require.Zero(t, gate.compensateCalls)
}

func TestQuantityGateCompensatesAnAuthoritativeDatabaseRejection(t *testing.T) {
	gate := &quantityGateFake{
		admit:      campaign.GateResult{Signal: campaign.GateAdmitted},
		compensate: campaign.GateResult{Signal: campaign.GateCompensated},
	}
	campaigns := &campaignServiceFake{reserveResult: campaignapp.QuantityResult{
		ResultRef: "quantity:db:rejected", Rejected: true, ReasonCode: "quantity_exhausted",
	}}
	dispatcher := quantityDispatcher(campaigns, gate, config.RedisFailureClosed, nil)

	resultRef, err := dispatcher.Dispatch(context.Background(), quantityRequest(t))
	require.NoError(t, err)
	require.Equal(t, "quantity:db:rejected", resultRef)
	require.Equal(t, 1, campaigns.reserveCalls)
	require.Equal(t, 1, gate.compensateCalls)
	require.Zero(t, gate.completeCalls)
}

func TestQuantityGateDBFallbackStillCallsPostgresOnRedisFailure(t *testing.T) {
	redisErr := errors.New("redis unavailable")
	gate := &quantityGateFake{admitErr: redisErr}
	campaigns := &campaignServiceFake{reserveResult: campaignapp.QuantityResult{ResultRef: "quantity:db:reserved"}}
	var hookOperation string
	var hookErr error
	dispatcher := quantityDispatcher(campaigns, gate, config.RedisFailureDBFallback, func(operation string, err error) {
		hookOperation, hookErr = operation, err
	})

	resultRef, err := dispatcher.Dispatch(context.Background(), quantityRequest(t))
	require.NoError(t, err)
	require.Equal(t, "quantity:db:reserved", resultRef)
	require.Equal(t, 1, campaigns.reserveCalls)
	require.Equal(t, "admit", hookOperation)
	require.ErrorIs(t, hookErr, redisErr)
}

func dispatcherFixture() *commandDispatcher {
	return &commandDispatcher{
		commandLease: time.Minute, idempotencyTTL: time.Hour,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func quantityDispatcher(campaigns *campaignServiceFake, gate *quantityGateFake, mode config.RedisFailureMode, hook QuantityGateFailureHook) *commandDispatcher {
	dispatcher := dispatcherFixture()
	dispatcher.campaigns = campaigns
	dispatcher.campaignReader = campaignReaderFake{value: campaign.Campaign{ID: "camp_12345678", TotalQuantity: 10, Version: 3}}
	dispatcher.reservations = reservationReaderFake{}
	dispatcher.quantityOptions = QuantityGateOptions{Gate: gate, FailureMode: mode, FailureHook: hook}
	return dispatcher
}

func quantityRequest(t *testing.T) domaineventing.CommandRequest {
	t.Helper()
	eventID := uuid.New()
	raw := policyEnvelope(t, policy.Envelope{
		EventID: eventID, EventDocumentID: "EVT.A.19-07", EventType: "coupon.issue.accepted",
		AggregateType: "CouponIssueRequest", AggregateID: "ireq_12345678", AggregateVersion: 0,
		OccurredAt: time.Now().UTC(), CorrelationID: "corr-quantity", PayloadSchemaVersion: 1,
		Data: map[string]any{"campaign_id": "camp_12345678", "issue_request_id": "ireq_12345678"},
	})
	request := commandRequest(eventID, "CMD.A.19-26", "camp_12345678", raw)
	request.CorrelationID = "corr-quantity"
	return request
}

func commandRequest(eventID uuid.UUID, documentID, aggregateID string, payload json.RawMessage) domaineventing.CommandRequest {
	return domaineventing.CommandRequest{
		ID: uuid.New(), CommandDocumentID: documentID, SourceEventID: &eventID,
		AggregateType: "test", AggregateID: aggregateID, BusinessKey: "policy:" + eventID.String() + ":" + documentID,
		CorrelationID: "corr-1", Payload: payload,
	}
}

func policyEnvelope(t *testing.T, envelope policy.Envelope) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(envelope)
	require.NoError(t, err)
	return encoded
}

type campaignServiceFake struct {
	reserveCalls  int
	reserveResult campaignapp.QuantityResult
	reserveErr    error
}

func (f *campaignServiceFake) ReserveQuantity(context.Context, campaignapp.ReserveQuantityInput) (campaignapp.QuantityResult, error) {
	f.reserveCalls++
	return f.reserveResult, f.reserveErr
}

func (f *campaignServiceFake) ConfirmQuantity(context.Context, campaignapp.DecideQuantityInput) (campaignapp.QuantityResult, error) {
	return campaignapp.QuantityResult{}, nil
}

func (f *campaignServiceFake) ReleaseQuantity(context.Context, campaignapp.DecideQuantityInput) (campaignapp.QuantityResult, error) {
	return campaignapp.QuantityResult{}, nil
}

type campaignReaderFake struct {
	value campaign.Campaign
	err   error
}

func (f campaignReaderFake) Get(context.Context, string) (campaign.Campaign, error) {
	return f.value, f.err
}

type reservationReaderFake struct {
	value  campaign.QuantityReservation
	exists bool
	err    error
}

func (f reservationReaderFake) FindQuantityReservation(context.Context, string, string) (campaign.QuantityReservation, bool, error) {
	return f.value, f.exists, f.err
}

type quantityGateFake struct {
	admitCalls, completeCalls, compensateCalls int
	admit, complete, compensate                campaign.GateResult
	admitErr, completeErr, compensateErr       error
}

func (f *quantityGateFake) Admit(context.Context, string, string, int64, int64) (campaign.GateResult, error) {
	f.admitCalls++
	return f.admit, f.admitErr
}

func (f *quantityGateFake) Complete(context.Context, string, string, int64) (campaign.GateResult, error) {
	f.completeCalls++
	return f.complete, f.completeErr
}

func (f *quantityGateFake) Compensate(context.Context, string, string, int64) (campaign.GateResult, error) {
	f.compensateCalls++
	return f.compensate, f.compensateErr
}

type recoveryReaderFake struct {
	value recovery.Recovery
	err   error
}

func (f recoveryReaderFake) Find(context.Context, string) (recovery.Recovery, error) {
	return f.value, f.err
}

type replayerFake struct {
	input    redemptionapp.ReplayInput
	metadata redemptionapp.Metadata
	result   redemptionapp.RecoveryResultCommand
	err      error
}

func (f *replayerFake) Replay(_ context.Context, input redemptionapp.ReplayInput, metadata redemptionapp.Metadata) (redemptionapp.RecoveryResultCommand, error) {
	f.input, f.metadata = input, metadata
	return f.result, f.err
}
