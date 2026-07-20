package intent

import (
	"context"
	"time"

	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

type Repository interface {
	Create(context.Context, domainintent.CreateParams) error
	FindActiveForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error)
	RotateOwnerProof(context.Context, uuid.UUID, []byte, []byte) error
	CreateActionPayload(context.Context, domainintent.ActionPayload) error
	BindActionPayload(context.Context, uuid.UUID, uuid.UUID) error
	FindConsumedActionForUpdate(context.Context, uuid.UUID, uuid.UUID) (domainintent.Intent, domainintent.ActionPayload, error)
	MarkActionDelivered(context.Context, uuid.UUID) error
}

type IdempotencyRepository interface {
	FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error)
	CreateCompleted(context.Context, domainidempotency.Record, string, string) error
}

type AuditAppender interface {
	Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error
}

type TxRepositories struct {
	Intents     Repository
	Idempotency IdempotencyRepository
	Audit       AuditAppender
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type BootstrapCryptography interface {
	Hash(...string) []byte
	Equal([]byte, ...string) bool
	Opaque(string) (string, error)
	Seal(any) ([]byte, error)
}

type ActionResumeCryptography interface {
	Hash(...string) []byte
	Open([]byte, any) error
}

type Clock interface {
	Now() time.Time
}

type Principal struct {
	Authenticated bool
	SessionID     uuid.UUID
	UserID        uuid.UUID
}
