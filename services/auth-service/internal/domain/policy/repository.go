package policy

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repository interface {
	ListActive(context.Context) ([]Snapshot, error)
	ListActiveForUpdate(context.Context, pgx.Tx) ([]Snapshot, error)
	FindActiveForUpdate(context.Context, pgx.Tx, string) (Snapshot, error)
	SupersedeAndInsert(context.Context, pgx.Tx, Snapshot, json.RawMessage, string, uuid.UUID) (Snapshot, error)
	FindGlobalActive(context.Context) (GlobalSnapshot, error)
	FindGlobalActiveForUpdate(context.Context, pgx.Tx) (GlobalSnapshot, error)
	ActivateGlobal(context.Context, pgx.Tx, json.RawMessage, uuid.UUID, string) (GlobalSnapshot, error)
}
