package operator

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("operator target not found")

type IdentityView struct {
	IdentityID, LinkID                                uuid.UUID
	Type, MaskedValue, VerificationStatus, LinkStatus string
	RowVersion                                        int64
	Locked                                            bool
	UnlockAvailableAt                                 *time.Time
}
type UserView struct {
	UserID         uuid.UUID
	Status         string
	Version        int64
	Identities     []IdentityView
	ActiveSessions int
}
type ManualAction struct {
	ID                                                                        uuid.UUID
	OperatorID                                                                uuid.UUID
	CaseID, TargetType, TargetID, Action, ReasonCode, ApprovalID, EvidenceRef string
	ExpectedVersion                                                           int64
	IdempotencyID                                                             *uuid.UUID
}
type ManualResult struct {
	ActionID      uuid.UUID
	TargetVersion int64
}
type Repository interface {
	GetUser(context.Context, uuid.UUID) (UserView, error)
	ApplyManual(context.Context, pgx.Tx, ManualAction) (int64, error)
	FindManualResult(context.Context, uuid.UUID) (ManualResult, error)
}
