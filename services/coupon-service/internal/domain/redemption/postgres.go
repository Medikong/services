package redemption

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

var _ Repository = (*PostgresRepository)(nil)

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

func (r *PostgresRepository) Find(ctx context.Context, id string) (Redemption, error) {
	if r == nil || r.pool == nil {
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.pool_required").New("postgres pool is required")
	}
	return scanRedemption(r.pool.QueryRow(ctx, redemptionSelect+` WHERE redemption_id = $1`, id))
}

func (r *PostgresRepository) FindConsumingByUserCoupon(ctx context.Context, userCouponID string) (Redemption, bool, error) {
	if r == nil || r.pool == nil {
		return Redemption{}, false, oops.In("coupon_redemption_repository").Code("coupon.pool_required").New("postgres pool is required")
	}
	rows, err := r.pool.Query(ctx, redemptionSelect+`
		WHERE user_coupon_id=$1 AND status IN ('reserved','confirmed','reclaimed')
		ORDER BY updated_at DESC, redemption_id LIMIT 1`, userCouponID)
	if err != nil {
		return Redemption{}, false, oops.In("coupon_redemption_repository").Code("coupon.redemption_read_failed").Wrap(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Redemption{}, false, oops.In("coupon_redemption_repository").Code("coupon.redemption_read_failed").Wrap(err)
		}
		return Redemption{}, false, nil
	}
	result, err := scanRedemption(rows)
	if err != nil {
		return Redemption{}, false, err
	}
	return result, true, nil
}

func (r *PostgresRepository) Evaluate(ctx context.Context, input Evaluation, command reliability.Command) (result Redemption, err error) {
	created, domainEvent, err := NewEvaluation(input)
	if err != nil {
		return Redemption{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_begin_failed").Wrap(err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_redemption_repository", err)
		}
	}()

	replay, err := reliability.Claim(ctx, tx, command, "CouponRedemption", created.ID)
	if err != nil {
		return Redemption{}, err
	}
	if replay.Existing && !replay.Resume {
		if replay.Status != "completed" {
			return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.command_in_progress").New("coupon redemption command is already processing")
		}
		result, err = findRedemptionTx(ctx, tx, created.ID)
		if err != nil {
			return Redemption{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_commit_failed").Wrap(err)
		}
		committed = true
		return result, nil
	}

	inserted, err := insertEvaluation(ctx, tx, created)
	if err != nil {
		return Redemption{}, err
	}
	if !inserted {
		result, err = findRedemptionByBusinessKey(ctx, tx, created.OrderID, created.UserCouponID, created.BusinessKey)
		if err != nil {
			return Redemption{}, err
		}
	} else {
		result = created
		domainEvent.CorrelationID = command.CorrelationID
		domainEvent.CausationID = command.CausationID
		domainEvent.TraceID = command.TraceID
		if err = appendLedger(ctx, tx, result, domainEvent); err != nil {
			return Redemption{}, err
		}
		if err = reliability.AppendOutbox(ctx, tx, domainEvent); err != nil {
			return Redemption{}, err
		}
	}
	if err = reliability.Complete(ctx, tx, command, result.ResultRef.ID, result); err != nil {
		return Redemption{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_commit_failed").Wrap(err)
	}
	committed = true
	return result, nil
}

func (r *PostgresRepository) Reserve(ctx context.Context, id string, expectedVersion int64, until time.Time, command reliability.Command) (Redemption, error) {
	return r.transition(ctx, id, command, func(current *Redemption) ([]reliability.Event, error) {
		event, err := current.Reserve(expectedVersion, until, r.now())
		return singleEvent(event, err)
	})
}

func (r *PostgresRepository) Confirm(ctx context.Context, id string, expectedVersion int64, ref shared.ExternalRef, snapshot any, reason string, command reliability.Command) (Redemption, error) {
	return r.transition(ctx, id, command, func(current *Redemption) ([]reliability.Event, error) {
		return current.Confirm(expectedVersion, ref, snapshot, reason, r.now())
	})
}

func (r *PostgresRepository) Release(ctx context.Context, id string, expectedVersion int64, ref shared.ExternalRef, snapshot any, reason string, command reliability.Command) (Redemption, error) {
	return r.transition(ctx, id, command, func(current *Redemption) ([]reliability.Event, error) {
		event, err := current.Release(expectedVersion, ref, snapshot, reason, r.now())
		return singleEvent(event, err)
	})
}

func (r *PostgresRepository) Reclaim(ctx context.Context, id string, expectedVersion int64, ref shared.ExternalRef, snapshot any, reason string, command reliability.Command) (Redemption, error) {
	return r.transition(ctx, id, command, func(current *Redemption) ([]reliability.Event, error) {
		return current.Reclaim(expectedVersion, ref, snapshot, reason, r.now())
	})
}

func (r *PostgresRepository) Replay(ctx context.Context, input ReplayRequest, command reliability.Command) (outcome ReplayOutcome, err error) {
	if r == nil || r.pool == nil {
		return ReplayOutcome{}, oops.In("coupon_redemption_repository").Code("coupon.pool_required").New("postgres pool is required")
	}
	if err := input.Validate(); err != nil {
		return ReplayOutcome{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ReplayOutcome{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_begin_failed").Wrap(err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_redemption_repository", err)
		}
	}()

	replay, err := reliability.Claim(ctx, tx, command, "CouponRedemption", input.RedemptionID)
	if err != nil {
		return ReplayOutcome{}, err
	}
	if replay.Existing && !replay.Resume {
		if replay.Status != "completed" {
			return ReplayOutcome{}, oops.In("coupon_redemption_repository").Code("coupon.command_in_progress").New("coupon redemption replay is already processing")
		}
		if err := json.Unmarshal(replay.ResponseSnapshot, &outcome); err != nil {
			return ReplayOutcome{}, oops.In("coupon_redemption_repository").Code("coupon.replay_snapshot_decode_failed").Wrap(err)
		}
		if err = tx.Commit(ctx); err != nil {
			return ReplayOutcome{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_commit_failed").Wrap(err)
		}
		committed = true
		return outcome, nil
	}

	current, err := scanRedemption(tx.QueryRow(ctx, redemptionSelect+` WHERE redemption_id=$1 FOR UPDATE`, input.RedemptionID))
	if err != nil {
		return ReplayOutcome{}, err
	}
	outcome.Redemption = current
	if replayAlreadyApplied(current.Status, input.Operation) {
		outcome.ResultKind = ReplayAlreadyApplied
		outcome.ResultRef = current.ResultRef.ID
	} else {
		events, domainErr := replayTransition(&current, input)
		if domainErr != nil {
			outcome.ResultKind = ReplayFailed
			outcome.FailureCode = replayFailureCode(domainErr)
		} else {
			if err = updateRedemption(ctx, tx, current); err != nil {
				return ReplayOutcome{}, err
			}
			for _, domainEvent := range events {
				domainEvent.CorrelationID = command.CorrelationID
				domainEvent.CausationID = command.CausationID
				domainEvent.TraceID = command.TraceID
				if err = appendLedger(ctx, tx, current, domainEvent); err != nil {
					return ReplayOutcome{}, err
				}
				if err = reliability.AppendOutbox(ctx, tx, domainEvent); err != nil {
					return ReplayOutcome{}, err
				}
			}
			outcome.Redemption = current
			outcome.ResultKind = ReplayTransitioned
			outcome.ResultRef = current.ResultRef.ID
		}
	}

	decision := replayDecisionEvent(current, input, outcome)
	decision.CorrelationID = command.CorrelationID
	decision.CausationID = command.CausationID
	decision.TraceID = command.TraceID
	if err = appendLedger(ctx, tx, current, decision); err != nil {
		return ReplayOutcome{}, err
	}
	if err = reliability.AppendOutbox(ctx, tx, decision); err != nil {
		return ReplayOutcome{}, err
	}
	if err = reliability.Complete(ctx, tx, command, decision.ID.String(), outcome); err != nil {
		return ReplayOutcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ReplayOutcome{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_commit_failed").Wrap(err)
	}
	committed = true
	return outcome, nil
}

func (r *PostgresRepository) transition(ctx context.Context, id string, command reliability.Command, mutate func(*Redemption) ([]reliability.Event, error)) (result Redemption, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_begin_failed").Wrap(err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_redemption_repository", err)
		}
	}()

	replay, err := reliability.Claim(ctx, tx, command, "CouponRedemption", id)
	if err != nil {
		return Redemption{}, err
	}
	if replay.Existing && !replay.Resume {
		if replay.Status != "completed" {
			return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.command_in_progress").New("coupon redemption command is already processing")
		}
		result, err = findRedemptionTx(ctx, tx, id)
		if err != nil {
			return Redemption{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_commit_failed").Wrap(err)
		}
		committed = true
		return result, nil
	}

	result, err = scanRedemption(tx.QueryRow(ctx, redemptionSelect+` WHERE redemption_id = $1 FOR UPDATE`, id))
	if err != nil {
		return Redemption{}, err
	}
	events, err := mutate(&result)
	if err != nil {
		return Redemption{}, err
	}
	if err = updateRedemption(ctx, tx, result); err != nil {
		return Redemption{}, err
	}
	for _, domainEvent := range events {
		domainEvent.CorrelationID = command.CorrelationID
		domainEvent.CausationID = command.CausationID
		domainEvent.TraceID = command.TraceID
		if err = appendLedger(ctx, tx, result, domainEvent); err != nil {
			return Redemption{}, err
		}
		if err = reliability.AppendOutbox(ctx, tx, domainEvent); err != nil {
			return Redemption{}, err
		}
	}
	if err = reliability.Complete(ctx, tx, command, result.ResultRef.ID, result); err != nil {
		return Redemption{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.transaction_commit_failed").Wrap(err)
	}
	committed = true
	return result, nil
}

const redemptionSelect = `
	SELECT redemption_id, user_coupon_id, campaign_id, user_id, order_id,
		operation_type, business_key, status, COALESCE(reason_code, ''), policy_version,
		order_snapshot, order_snapshot_hash, evaluated_at, discount_amount::text,
		final_order_amount::text, currency, cost_attribution, reserved_until,
		confirmed_at, released_at, reclaimed_at, result_ref, result_snapshot, version
	FROM coupon_redemptions`

type rowScanner interface {
	Scan(...any) error
}

func scanRedemption(row rowScanner) (Redemption, error) {
	var result Redemption
	var status string
	var orderSnapshot, costAttribution, resultRef, resultSnapshot []byte
	err := row.Scan(
		&result.ID, &result.UserCouponID, &result.CampaignID, &result.UserID, &result.OrderID,
		&result.OperationType, &result.BusinessKey, &status, &result.ReasonCode, &result.PolicyVersion,
		&orderSnapshot, &result.OrderSnapshotHash, &result.EvaluatedAt, &result.Discount.Amount,
		&result.FinalOrderAmount.Amount, &result.Discount.Currency, &costAttribution, &result.ReservedUntil,
		&result.ConfirmedAt, &result.ReleasedAt, &result.ReclaimedAt, &resultRef, &resultSnapshot, &result.Version,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.redemption_not_found").New("coupon redemption was not found")
		}
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.redemption_read_failed").Wrap(err)
	}
	result.Status = Status(status)
	result.FinalOrderAmount.Currency = result.Discount.Currency
	if err := json.Unmarshal(orderSnapshot, &result.OrderSnapshot); err != nil {
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.redemption_snapshot_decode_failed").Wrap(err)
	}
	if err := json.Unmarshal(costAttribution, &result.CostShares); err != nil {
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.redemption_cost_decode_failed").Wrap(err)
	}
	if err := json.Unmarshal(resultRef, &result.ResultRef); err != nil {
		return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.redemption_result_ref_decode_failed").Wrap(err)
	}
	if len(resultSnapshot) > 0 && string(resultSnapshot) != "null" {
		if err := json.Unmarshal(resultSnapshot, &result.ResultSnapshot); err != nil {
			return Redemption{}, oops.In("coupon_redemption_repository").Code("coupon.redemption_result_snapshot_decode_failed").Wrap(err)
		}
	}
	return result, nil
}

func insertEvaluation(ctx context.Context, tx pgx.Tx, value Redemption) (bool, error) {
	orderSnapshot, costAttribution, resultRef, resultSnapshot, err := encodeRedemption(value)
	if err != nil {
		return false, err
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO coupon_redemptions (
			redemption_id, user_coupon_id, campaign_id, user_id, order_id,
			operation_type, business_key, status, reason_code, policy_version,
			order_snapshot, order_snapshot_hash, evaluated_at, discount_amount,
			final_order_amount, currency, cost_attribution, result_ref, result_snapshot, version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT (order_id, user_coupon_id, business_key) DO NOTHING
	`, value.ID, value.UserCouponID, value.CampaignID, value.UserID, value.OrderID,
		value.OperationType, value.BusinessKey, value.Status, value.ReasonCode, value.PolicyVersion,
		orderSnapshot, value.OrderSnapshotHash, value.EvaluatedAt, value.Discount.Amount,
		value.FinalOrderAmount.Amount, value.Discount.Currency, costAttribution, resultRef, resultSnapshot, value.Version)
	if err != nil {
		return false, oops.In("coupon_redemption_repository").Code("coupon.redemption_insert_failed").Wrap(err)
	}
	return command.RowsAffected() == 1, nil
}

func updateRedemption(ctx context.Context, tx pgx.Tx, value Redemption) error {
	orderSnapshot, costAttribution, resultRef, resultSnapshot, err := encodeRedemption(value)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		UPDATE coupon_redemptions
		SET operation_type=$2, status=$3, reason_code=NULLIF($4,''), order_snapshot=$5,
			cost_attribution=$6, reserved_until=$7, confirmed_at=$8, released_at=$9,
			reclaimed_at=$10, result_ref=$11, result_snapshot=$12, version=$13, updated_at=now()
		WHERE redemption_id=$1 AND version=$14
	`, value.ID, value.OperationType, value.Status, value.ReasonCode, orderSnapshot,
		costAttribution, value.ReservedUntil, value.ConfirmedAt, value.ReleasedAt,
		value.ReclaimedAt, resultRef, resultSnapshot, value.Version, value.Version-1)
	if err != nil {
		return oops.In("coupon_redemption_repository").Code("coupon.redemption_update_failed").Wrap(err)
	}
	if command.RowsAffected() != 1 {
		return oops.In("coupon_redemption_repository").Code("coupon.version_conflict").New("coupon redemption changed concurrently")
	}
	return nil
}

func encodeRedemption(value Redemption) ([]byte, []byte, []byte, []byte, error) {
	orderSnapshot, err := json.Marshal(value.OrderSnapshot)
	if err != nil {
		return nil, nil, nil, nil, oops.In("coupon_redemption_repository").Code("coupon.redemption_snapshot_encode_failed").Wrap(err)
	}
	costAttribution, err := json.Marshal(value.CostShares)
	if err != nil {
		return nil, nil, nil, nil, oops.In("coupon_redemption_repository").Code("coupon.redemption_cost_encode_failed").Wrap(err)
	}
	resultRef, err := json.Marshal(value.ResultRef)
	if err != nil {
		return nil, nil, nil, nil, oops.In("coupon_redemption_repository").Code("coupon.redemption_result_ref_encode_failed").Wrap(err)
	}
	resultSnapshot, err := json.Marshal(value.ResultSnapshot)
	if err != nil {
		return nil, nil, nil, nil, oops.In("coupon_redemption_repository").Code("coupon.redemption_result_snapshot_encode_failed").Wrap(err)
	}
	return orderSnapshot, costAttribution, resultRef, resultSnapshot, nil
}

func findRedemptionTx(ctx context.Context, tx pgx.Tx, id string) (Redemption, error) {
	return scanRedemption(tx.QueryRow(ctx, redemptionSelect+` WHERE redemption_id = $1`, id))
}

func findRedemptionByBusinessKey(ctx context.Context, tx pgx.Tx, orderID, userCouponID, businessKey string) (Redemption, error) {
	return scanRedemption(tx.QueryRow(ctx, redemptionSelect+` WHERE order_id=$1 AND user_coupon_id=$2 AND business_key=$3`, orderID, userCouponID, businessKey))
}

func appendLedger(ctx context.Context, tx pgx.Tx, value Redemption, domainEvent reliability.Event) error {
	amount, err := json.Marshal(map[string]any{
		"discount": value.Discount, "final_order_amount": value.FinalOrderAmount, "cost_shares": value.CostShares,
	})
	if err != nil {
		return oops.In("coupon_redemption_repository").Code("coupon.redemption_ledger_amount_encode_failed").Wrap(err)
	}
	payload, err := json.Marshal(domainEvent.Data)
	if err != nil {
		return oops.In("coupon_redemption_repository").Code("coupon.redemption_ledger_payload_encode_failed").Wrap(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO coupon_redemption_ledger (
			ledger_id, redemption_id, order_id, user_coupon_id, event_type,
			amount_snapshot, result_ref, payload, occurred_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, uuid.New(), value.ID, value.OrderID, value.UserCouponID, domainEvent.Type,
		amount, value.ResultRef.ID, payload, domainEvent.OccurredAt)
	if err != nil {
		return oops.In("coupon_redemption_repository").Code("coupon.redemption_ledger_append_failed").Wrap(err)
	}
	return nil
}

func singleEvent(event reliability.Event, err error) ([]reliability.Event, error) {
	if err != nil {
		return nil, err
	}
	return []reliability.Event{event}, nil
}

func replayAlreadyApplied(status Status, operation ReplayOperation) bool {
	switch operation {
	case ReplayReserve:
		return status == StatusReserved || status == StatusConfirmed || status == StatusReleased || status == StatusReclaimed
	case ReplayConfirm:
		return status == StatusConfirmed || status == StatusReclaimed
	case ReplayRelease:
		return status == StatusReleased
	case ReplayReclaim:
		return status == StatusReclaimed
	default:
		return false
	}
}

func replayTransition(current *Redemption, input ReplayRequest) ([]reliability.Event, error) {
	switch input.Operation {
	case ReplayReserve:
		domainEvent, err := current.Reserve(input.ExpectedVersion, *input.ReservedUntil, input.ReplayedAt)
		return singleEvent(domainEvent, err)
	case ReplayConfirm:
		return current.Confirm(input.ExpectedVersion, *input.ResultRef, input.ResultSnapshot, input.ReasonCode, input.ReplayedAt)
	case ReplayRelease:
		domainEvent, err := current.Release(input.ExpectedVersion, *input.ResultRef, input.ResultSnapshot, input.ReasonCode, input.ReplayedAt)
		return singleEvent(domainEvent, err)
	case ReplayReclaim:
		return current.Reclaim(input.ExpectedVersion, *input.ResultRef, input.ResultSnapshot, input.ReasonCode, input.ReplayedAt)
	default:
		return nil, oops.In("coupon_redemption_repository").Code("coupon.redemption_replay_operation_invalid").New("coupon redemption replay operation is not supported")
	}
}

func replayFailureCode(err error) string {
	if value, ok := oops.AsOops(err); ok {
		if code := fmt.Sprint(value.Code()); code != "" && code != "<nil>" {
			return code
		}
	}
	return "coupon.redemption_replay_failed"
}

func replayDecisionEvent(value Redemption, input ReplayRequest, outcome ReplayOutcome) reliability.Event {
	return reliability.Event{
		ID: uuid.New(), DocumentID: "EVT.A.19-41", Type: "coupon.redemption.replay_decided",
		AggregateType: "CouponRedemption", AggregateID: value.ID, AggregateVersion: value.Version,
		PayloadSchemaVersion: 1, OccurredAt: input.ReplayedAt,
		Data: map[string]any{
			"recovery_id": input.RecoveryID, "attempt_id": input.AttemptID,
			"business_key": input.BusinessKey, "redemption_id": value.ID,
			"result_kind": outcome.ResultKind, "result_ref": outcome.ResultRef,
			"failure_code": outcome.FailureCode,
		},
	}
}
