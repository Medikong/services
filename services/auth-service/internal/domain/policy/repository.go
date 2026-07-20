package policy

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("policy not found")

type Repository interface {
	ListActive(context.Context) ([]Snapshot, error)
	ListActiveForUpdate(context.Context) ([]Snapshot, error)
	FindActiveForUpdate(context.Context, string) (Snapshot, error)
	SupersedeAndInsert(context.Context, Snapshot, json.RawMessage, string, uuid.UUID) (Snapshot, error)
	FindGlobalActive(context.Context) (GlobalSnapshot, error)
	FindGlobalActiveForUpdate(context.Context) (GlobalSnapshot, error)
	ActivateGlobal(context.Context, json.RawMessage, uuid.UUID, string) (GlobalSnapshot, error)
}
