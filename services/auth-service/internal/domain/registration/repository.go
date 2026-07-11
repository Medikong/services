package registration

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound        = errors.New("registration not found")
	ErrVersionConflict = errors.New("registration version conflict")
)

// Repository is the Registration persistence port. The caller opens one pgx
// transaction so row locks can span this aggregate and its related writes.
type Repository interface {
	Create(ctx context.Context, tx pgx.Tx, registration Registration) error
	Find(ctx context.Context, tx pgx.Tx, registrationID uuid.UUID) (Registration, error)
	FindForUpdate(ctx context.Context, tx pgx.Tx, registrationID uuid.UUID) (Registration, error)
	Save(ctx context.Context, tx pgx.Tx, registration *Registration) error
}
