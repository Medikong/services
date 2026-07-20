package challenge

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound                  = errors.New("verification challenge not found")
	ErrVersionConflict           = errors.New("verification challenge version conflict")
	ErrVirtualProjectionDisabled = errors.New("virtual verification projection is disabled")
)

// Repository is transaction-scoped by its adapter. Keeping the transaction
// out of this contract lets the domain describe persistence without naming a
// database driver.
type Repository interface {
	Create(ctx context.Context, challenge Challenge) error
	Find(ctx context.Context, challengeID uuid.UUID) (Challenge, error)
	FindForUpdate(ctx context.Context, challengeID uuid.UUID) (Challenge, error)
	Issue(ctx context.Context, challenge Challenge) error
	Save(ctx context.Context, challenge *Challenge) error
	StoreDeliveryPayload(ctx context.Context, payload DeliveryPayload) error
	StoreVirtualProjection(ctx context.Context, projection VirtualProjection) error
	FindVirtualProjection(ctx context.Context, challengeID uuid.UUID, now time.Time) (VirtualProjection, error)
}

// Consumer is the smallest persistence role needed to consume a Challenge.
// Callers may own narrower role interfaces that satisfy this contract.
type Consumer interface {
	FindForUpdate(context.Context, uuid.UUID) (Challenge, error)
	Save(context.Context, *Challenge) error
}

// Consume locks through the transaction-scoped repository, applies the pure
// state transition, and persists a changed terminal or attempt state.
func Consume(ctx context.Context, repository Consumer, challengeID uuid.UUID, now time.Time, verify func(Challenge) bool) (Challenge, ConsumeResult, error) {
	current, err := repository.FindForUpdate(ctx, challengeID)
	if err != nil {
		return Challenge{}, ConsumeResult{}, err
	}
	matches := false
	if verify != nil {
		matches = verify(current)
	}
	result, domainErr := current.Consume(now, matches)
	if result.Changed {
		if err := repository.Save(ctx, &current); err != nil {
			return Challenge{}, ConsumeResult{}, err
		}
	}
	if domainErr != nil {
		result.Failure = consumeFailure(domainErr)
	}
	return current, result, nil
}

func consumeFailure(err error) ConsumeFailure {
	switch {
	case errors.Is(err, ErrChallengeExpired):
		return ConsumeFailureExpired
	case errors.Is(err, ErrVerificationFailed):
		return ConsumeFailureMismatch
	case errors.Is(err, ErrChallengeClosed):
		return ConsumeFailureClosed
	default:
		return ConsumeFailureInvalid
	}
}
