package challenge

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound                  = errors.New("verification challenge not found")
	ErrVersionConflict           = errors.New("verification challenge version conflict")
	ErrVirtualProjectionDisabled = errors.New("virtual verification projection is disabled")
)

// Repository is the VerificationChallenge persistence port. The verifier runs
// only after the concrete transaction has locked the challenge row.
type Repository interface {
	Create(ctx context.Context, tx pgx.Tx, challenge Challenge) error
	Find(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID) (Challenge, error)
	FindForUpdate(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID) (Challenge, error)
	Issue(ctx context.Context, tx pgx.Tx, challenge Challenge) error
	Consume(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID, now time.Time, verify func(Challenge) bool) (Challenge, ConsumeResult, error)
	Save(ctx context.Context, tx pgx.Tx, challenge *Challenge) error
	StoreDeliveryPayload(ctx context.Context, tx pgx.Tx, payload DeliveryPayload) error
	StoreVirtualProjection(ctx context.Context, tx pgx.Tx, projection VirtualProjection) error
	FindVirtualProjection(ctx context.Context, tx pgx.Tx, challengeID uuid.UUID, now time.Time) (VirtualProjection, error)
}
