package operations

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

var _ Repository = (*PostgresRepository)(nil)

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, control Control, domainEvent reliability.Event, command reliability.Command) (result Control, err error) {
	if err = r.ready(); err != nil {
		return Control{}, err
	}
	if err = control.Validate(); err != nil {
		return Control{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Control{}, dbError("begin_create", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_operations_repository", err)
		}
	}()
	if result, done, replayErr := replayControl(ctx, tx, command, control.ID); replayErr != nil || done {
		return commitReplay(ctx, tx, result, replayErr, &committed)
	}
	_, err = tx.Exec(ctx, `INSERT INTO coupon_operational_controls (
		control_id,active,effective_from,block_issuance,block_redemption,notice_message,notice_active,
		notice_effective_from,operation_request_ref,approval_ref,reason_code,version,created_at,updated_at
	) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14)`,
		control.ID, control.Active, control.EffectiveFrom, control.BlockIssuance, control.BlockRedemption,
		control.Notice.Message, control.Notice.Active, nullableTime(control.Notice.EffectiveFrom), control.OperationRequestRef,
		control.ApprovalRef, control.ReasonCode, control.Version, control.CreatedAt, control.UpdatedAt)
	if err != nil {
		return Control{}, dbError("insert_control", err)
	}
	for _, scope := range control.Scopes {
		if _, err = tx.Exec(ctx, `INSERT INTO coupon_operational_scopes (control_id,scope_type,scope_ref,created_at) VALUES ($1,$2,$3,$4)`, control.ID, scope.Type, scope.Ref, control.CreatedAt); err != nil {
			return Control{}, dbError("insert_scope", err)
		}
	}
	if err = persistEvent(ctx, tx, control, domainEvent, command); err != nil {
		return Control{}, err
	}
	if err = reliability.Complete(ctx, tx, command, control.ID, control); err != nil {
		return Control{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Control{}, dbError("commit_create", err)
	}
	committed = true
	return control, nil
}

func (r *PostgresRepository) Find(ctx context.Context, id string) (Control, error) {
	if err := r.ready(); err != nil {
		return Control{}, err
	}
	return findControl(ctx, r.pool, id, false)
}

func (r *PostgresRepository) FindEffective(ctx context.Context, scope Scope, at time.Time) ([]Control, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT c.control_id FROM coupon_operational_controls c
		JOIN coupon_operational_scopes s ON s.control_id=c.control_id
		WHERE s.scope_type=$1 AND s.scope_ref=$2 AND c.active AND c.effective_from <= $3
		ORDER BY c.effective_from DESC,c.control_id`, scope.Type, scope.Ref, at)
	if err != nil {
		return nil, dbError("find_effective", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, dbError("scan_effective_id", err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, dbError("find_effective", err)
	}
	rows.Close()
	controls := make([]Control, 0, len(ids))
	for _, id := range ids {
		control, findErr := findControl(ctx, r.pool, id, false)
		if findErr != nil {
			return nil, findErr
		}
		controls = append(controls, control)
	}
	return controls, nil
}

func (r *PostgresRepository) ApplyNotice(ctx context.Context, id string, input NoticeUpdate, command reliability.Command) (result Control, err error) {
	if err = r.ready(); err != nil {
		return Control{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Control{}, dbError("begin_notice", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_operations_repository", err)
		}
	}()
	if result, done, replayErr := replayControl(ctx, tx, command, id); replayErr != nil || done {
		return commitReplay(ctx, tx, result, replayErr, &committed)
	}
	result, err = findControl(ctx, tx, id, true)
	if err != nil {
		return Control{}, err
	}
	domainEvent, err := result.ApplyNotice(input)
	if err != nil {
		return Control{}, err
	}
	update, err := tx.Exec(ctx, `UPDATE coupon_operational_controls SET notice_message=$2,notice_active=$3,
		notice_effective_from=$4,version=$5,updated_at=$6 WHERE control_id=$1 AND version=$7`,
		result.ID, result.Notice.Message, result.Notice.Active, result.Notice.EffectiveFrom,
		result.Version, result.UpdatedAt, input.ExpectedVersion)
	if err != nil {
		return Control{}, dbError("update_notice", err)
	}
	if update.RowsAffected() != 1 {
		return Control{}, oops.In("coupon_operations_repository").Code("coupon.version_conflict").New("coupon operational control changed concurrently")
	}
	if err = persistEvent(ctx, tx, result, domainEvent, command); err != nil {
		return Control{}, err
	}
	if err = reliability.Complete(ctx, tx, command, result.ID, result); err != nil {
		return Control{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Control{}, dbError("commit_notice", err)
	}
	committed = true
	return result, nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const controlSelect = `SELECT control_id,active,effective_from,block_issuance,block_redemption,
	COALESCE(notice_message,''),notice_active,notice_effective_from,operation_request_ref,approval_ref,
	COALESCE(reason_code,''),version,created_at,updated_at FROM coupon_operational_controls`

type rowScanner interface {
	Scan(...any) error
}

func scanControl(row rowScanner) (Control, error) {
	var control Control
	var noticeEffective *time.Time
	err := row.Scan(&control.ID, &control.Active, &control.EffectiveFrom, &control.BlockIssuance, &control.BlockRedemption,
		&control.Notice.Message, &control.Notice.Active, &noticeEffective, &control.OperationRequestRef, &control.ApprovalRef,
		&control.ReasonCode, &control.Version, &control.CreatedAt, &control.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Control{}, oops.In("coupon_operations_repository").Code("coupon.operational_control_not_found").New("coupon operational control was not found")
	}
	if err != nil {
		return Control{}, dbError("scan_control", err)
	}
	if noticeEffective != nil {
		control.Notice.EffectiveFrom = *noticeEffective
	}
	return control, nil
}

func findControl(ctx context.Context, db queryer, id string, lock bool) (Control, error) {
	query := controlSelect + ` WHERE control_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	control, err := scanControl(db.QueryRow(ctx, query, id))
	if err != nil {
		return Control{}, err
	}
	rows, err := db.Query(ctx, `SELECT scope_type,scope_ref FROM coupon_operational_scopes WHERE control_id=$1 ORDER BY scope_type,scope_ref`, id)
	if err != nil {
		return Control{}, dbError("read_scopes", err)
	}
	defer rows.Close()
	for rows.Next() {
		var scope Scope
		if err = rows.Scan(&scope.Type, &scope.Ref); err != nil {
			return Control{}, dbError("scan_scope", err)
		}
		control.Scopes = append(control.Scopes, scope)
	}
	if err = rows.Err(); err != nil {
		return Control{}, dbError("read_scopes", err)
	}
	return control, nil
}

func persistEvent(ctx context.Context, tx pgx.Tx, control Control, domainEvent reliability.Event, command reliability.Command) error {
	domainEvent.CorrelationID = command.CorrelationID
	domainEvent.CausationID = command.CausationID
	domainEvent.TraceID = command.TraceID
	scope, err := json.Marshal(control.Scopes)
	if err != nil {
		return oops.In("coupon_operations_repository").Code("coupon.operation_scope_encode_failed").Wrap(err)
	}
	payload, err := json.Marshal(control)
	if err != nil {
		return oops.In("coupon_operations_repository").Code("coupon.operation_ledger_encode_failed").Wrap(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO coupon_operation_ledger (ledger_id,control_id,scope,operation_request_ref,
		approval_ref,event_type,payload,occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.New(), control.ID,
		scope, control.OperationRequestRef, control.ApprovalRef, domainEvent.Type, payload, domainEvent.OccurredAt)
	if err != nil {
		return dbError("append_ledger", err)
	}
	return reliability.AppendOutbox(ctx, tx, domainEvent)
}

func replayControl(ctx context.Context, tx pgx.Tx, command reliability.Command, id string) (Control, bool, error) {
	replay, err := reliability.Claim(ctx, tx, command, "CouponOperationalControl", id)
	if err != nil {
		return Control{}, false, err
	}
	if !replay.Existing || replay.Resume {
		return Control{}, false, nil
	}
	if replay.Status != "completed" {
		return Control{}, false, oops.In("coupon_operations_repository").Code("coupon.command_in_progress").New("coupon operational command is already processing")
	}
	if len(replay.ResponseSnapshot) > 0 {
		var control Control
		if err = json.Unmarshal(replay.ResponseSnapshot, &control); err != nil {
			return Control{}, false, oops.In("coupon_operations_repository").Code("coupon.idempotency_snapshot_decode_failed").Wrap(err)
		}
		return control, true, nil
	}
	control, err := findControl(ctx, tx, id, false)
	return control, true, err
}

func commitReplay(ctx context.Context, tx pgx.Tx, control Control, cause error, committed *bool) (Control, error) {
	if cause != nil {
		return Control{}, cause
	}
	if err := tx.Commit(ctx); err != nil {
		return Control{}, dbError("commit_replay", err)
	}
	*committed = true
	return control, nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (r *PostgresRepository) ready() error {
	if r == nil || r.pool == nil {
		return oops.In("coupon_operations_repository").Code("coupon.pool_required").New("postgres pool is required")
	}
	return nil
}

func dbError(operation string, err error) error {
	return oops.In("coupon_operations_repository").Code("coupon.database_failed").With("operation", operation).Wrap(err)
}
