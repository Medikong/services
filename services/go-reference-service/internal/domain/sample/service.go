package sample

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

const rollbackTimeout = 5 * time.Second

var ErrStaleFence = oops.
	In("reference_sample").
	Code("sample.stale_fence").
	New("fencing token is older than the stored token")

var ErrDuplicateOperation = oops.
	In("reference_sample").
	Code("sample.duplicate_operation").
	New("an audit event already exists for this operation")

type Command struct {
	ResourceID     string
	FenceToken     int64
	Actor          audit.Actor
	RequestID      string
	IdempotencyKey string
	TraceID        string
	SpanID         string
}

type Service struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) (Service, error) {
	if db == nil {
		return Service{}, oops.In("reference_sample").Code("sample.database_required").New("postgres pool is required")
	}
	return Service{db: db}, nil
}

func (s Service) Apply(ctx context.Context, command Command) (err error) {
	if strings.TrimSpace(command.ResourceID) == "" || command.FenceToken <= 0 || strings.TrimSpace(command.IdempotencyKey) == "" {
		return oops.In("reference_sample").Code("sample.invalid_command").New("resource id, fencing token, and idempotency key are required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return oops.In("reference_sample").Code("sample.transaction_begin_failed").Wrap(err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackCtx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = oops.Join(err, oops.In("reference_sample").Code("sample.transaction_rollback_failed").Wrap(rollbackErr))
		}
	}()

	result, err := tx.Exec(ctx, `
		INSERT INTO reference_fenced_writes (resource_id, fence_token, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (resource_id) DO UPDATE
		SET fence_token = EXCLUDED.fence_token, updated_at = now()
		WHERE reference_fenced_writes.fence_token < EXCLUDED.fence_token
	`, command.ResourceID, command.FenceToken)
	if err != nil {
		return oops.In("reference_sample").Code("sample.write_failed").With("resource_id", command.ResourceID).Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return ErrStaleFence
	}

	payload, err := audit.MarshalPayload(map[string]any{"fencingToken": command.FenceToken})
	if err != nil {
		return err
	}
	event := audit.NewEvent(
		"reference.resource.updated",
		1,
		command.Actor,
		audit.Resource{Type: "reference_resource", ID: command.ResourceID},
		payload,
		command.ResourceID+":"+command.IdempotencyKey,
	)
	event.Metadata = map[string]string{
		"request_id": command.RequestID,
		"trace_id":   command.TraceID,
		"span_id":    command.SpanID,
	}
	inserted, err := audit.Append(ctx, tx, event)
	if err != nil {
		return err
	}
	if !inserted {
		return ErrDuplicateOperation
	}
	if err := tx.Commit(ctx); err != nil {
		return oops.In("reference_sample").Code("sample.transaction_commit_failed").Wrap(err)
	}
	return nil
}
