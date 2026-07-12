package idempotency

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repository interface {
	FindForUpdate(context.Context, pgx.Tx, string, []byte, []byte) (Record, error)
	CreateProcessing(context.Context, pgx.Tx, Record, string) error
	ClaimProcessing(context.Context, pgx.Tx, Record, string) (Record, bool, error)
	CreateCompleted(context.Context, pgx.Tx, Record, string, string) error
	AttachReplayPayload(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error
	Complete(context.Context, pgx.Tx, uuid.UUID, string) error
	CreateReplayPayload(context.Context, pgx.Tx, ReplayPayload) error
	FindReplayPayloadForUpdate(context.Context, pgx.Tx, uuid.UUID) (ReplayPayload, error)
	RecordReplay(context.Context, pgx.Tx, uuid.UUID) error
	DestroyReplayPayload(context.Context, pgx.Tx, uuid.UUID) error
}
