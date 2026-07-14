package user

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/user-service/internal/security"
)

type ChangeStatusInput struct {
	UserID              uuid.UUID
	TargetStatus        string
	ReasonCode          string
	ExpectedUserVersion int64
	ChangedBy           string
	IdempotencyKey      string
}

type ChangeStatusOutput struct {
	StatusChangeID        uuid.UUID
	UserID                uuid.UUID
	AccountStatus         AccountStatus
	UserVersion           int64
	ChangedAt             time.Time
	UserStatusChangeProof string
	Replayed              bool
}

func (s *UserService) GetAccountStatus(ctx context.Context, id uuid.UUID) (User, error) {
	return s.GetOwnProfile(ctx, id)
}

func (s *UserService) ChangeUserAccountStatus(ctx context.Context, input ChangeStatusInput) (ChangeStatusOutput, error) {
	if input.ExpectedUserVersion < 1 {
		return ChangeStatusOutput{}, inputError(oops.New("expectedUserVersion must be at least 1"))
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return ChangeStatusOutput{}, inputError(err)
	}
	target, err := ParseStatus(input.TargetStatus)
	if err != nil {
		return ChangeStatusOutput{}, categorizedError(ErrTransitionInvalid, err)
	}
	reasonCode := strings.TrimSpace(input.ReasonCode)
	if !reasonCodePattern.MatchString(reasonCode) || strings.TrimSpace(input.ChangedBy) == "" || len(input.ChangedBy) > 128 {
		return ChangeStatusOutput{}, inputError(oops.New("reasonCode or operator principal is invalid"))
	}
	requestHash, err := hashRequest(struct {
		Target   AccountStatus `json:"targetStatus"`
		Reason   string        `json:"reasonCode"`
		Expected int64         `json:"expectedUserVersion"`
	}{target, reasonCode, input.ExpectedUserVersion})
	if err != nil {
		return ChangeStatusOutput{}, serviceOperationError(operationStatus, err)
	}
	now := s.now()
	record := newIdempotencyRecord(operationStatus, input.UserID.String(), input.IdempotencyKey, requestHash, now, s.idempotencyTTL)
	tx, err := s.repository.Begin(ctx)
	if err != nil {
		return ChangeStatusOutput{}, serviceOperationError(operationStatus, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, replayed, err := s.repository.ClaimIdempotency(ctx, tx, record)
	if err != nil {
		return ChangeStatusOutput{}, serviceOperationError(operationStatus, err)
	}
	if replayed {
		changeID, parseErr := uuid.Parse(*claimed.ResultID)
		if parseErr != nil {
			return ChangeStatusOutput{}, serviceOperationError(operationStatus, parseErr)
		}
		history, queryErr := s.repository.GetStatusHistory(ctx, tx, changeID)
		if queryErr != nil {
			return ChangeStatusOutput{}, serviceOperationError(operationStatus, queryErr)
		}
		_ = tx.Rollback(ctx)
		proof, proofErr := s.signStatusProof(history.ID, history.UserID, history.ChangedStatus, *claimed.ResultVersion, history.ChangedAt)
		if proofErr != nil {
			return ChangeStatusOutput{}, serviceOperationError(operationStatus, proofErr)
		}
		return ChangeStatusOutput{StatusChangeID: history.ID, UserID: history.UserID, AccountStatus: history.ChangedStatus, UserVersion: *claimed.ResultVersion, ChangedAt: history.ChangedAt, UserStatusChangeProof: proof, Replayed: true}, nil
	}
	result, err := s.repository.ChangeStatus(ctx, tx, input.UserID, target, input.ExpectedUserVersion, uuid.New(), reasonCode, input.ChangedBy, now)
	if err != nil {
		return ChangeStatusOutput{}, serviceOperationError(operationStatus, err)
	}
	if err := s.repository.CompleteIdempotency(ctx, tx, record, "status_change", result.StatusChangeID.String(), result.Version); err != nil {
		return ChangeStatusOutput{}, serviceOperationError(operationStatus, err)
	}
	if err := commit(ctx, tx); err != nil {
		return ChangeStatusOutput{}, serviceOperationError(operationStatus, err)
	}
	proof, err := s.signStatusProof(result.StatusChangeID, result.UserID, result.ChangedStatus, result.Version, result.ChangedAt)
	if err != nil {
		return ChangeStatusOutput{}, serviceOperationError(operationStatus, err)
	}
	return ChangeStatusOutput{StatusChangeID: result.StatusChangeID, UserID: result.UserID, AccountStatus: result.ChangedStatus, UserVersion: result.Version, ChangedAt: result.ChangedAt, UserStatusChangeProof: proof}, nil
}

func (s *UserService) signStatusProof(changeID, userID uuid.UUID, status AccountStatus, version int64, changedAt time.Time) (string, error) {
	return s.userProofs.Sign(security.ProofClaims{
		Audience:       "auth-service",
		Purpose:        "apply_user_status",
		StatusChangeID: changeID.String(),
		UserID:         userID.String(),
		AccountStatus:  string(status),
		UserVersion:    version,
		ChangedAt:      changedAt.UTC().Unix(),
		Nonce:          uuid.NewString(),
	}, s.proofTTL)
}
