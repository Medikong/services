package operator

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("operator target not found")
	ErrInvalidManualAction = errors.New("invalid operator manual action")
)

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
	ApplyManual(context.Context, ManualAction) (int64, error)
	FindManualResult(context.Context, uuid.UUID) (ManualResult, error)
}

func (a ManualAction) Validate() error {
	if a.ID == uuid.Nil || a.OperatorID == uuid.Nil || strings.TrimSpace(a.CaseID) == "" ||
		strings.TrimSpace(a.TargetID) == "" || strings.TrimSpace(a.ReasonCode) == "" ||
		strings.TrimSpace(a.ApprovalID) == "" || strings.TrimSpace(a.EvidenceRef) == "" ||
		a.ExpectedVersion < 0 {
		return ErrInvalidManualAction
	}
	return ValidateManualTarget(a.Action, a.TargetType)
}

func ValidateManualTarget(action, targetType string) error {
	validTarget := (action == "unlock_identity" && targetType == "identity") ||
		((action == "revoke_identity_link" || action == "approve_relink") && targetType == "identity_link") ||
		(action == "revoke_sessions" && targetType == "session")
	if !validTarget {
		return ErrInvalidManualAction
	}
	return nil
}
