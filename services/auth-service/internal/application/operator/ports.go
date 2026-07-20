package operator

import (
	"context"
	"encoding/json"
	"time"

	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainoperator "github.com/Medikong/services/services/auth-service/internal/domain/operator"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainpolicy "github.com/Medikong/services/services/auth-service/internal/domain/policy"
	"github.com/google/uuid"
)

type OperatorRepository interface {
	GetUser(context.Context, uuid.UUID) (domainoperator.UserView, error)
	ApplyManual(context.Context, domainoperator.ManualAction) (int64, error)
	FindManualResult(context.Context, uuid.UUID) (domainoperator.ManualResult, error)
}

type PolicyRepository interface {
	ListActiveForUpdate(context.Context) ([]domainpolicy.Snapshot, error)
	FindGlobalActive(context.Context) (domainpolicy.GlobalSnapshot, error)
	FindGlobalActiveForUpdate(context.Context) (domainpolicy.GlobalSnapshot, error)
	SupersedeAndInsert(context.Context, domainpolicy.Snapshot, json.RawMessage, string, uuid.UUID) (domainpolicy.Snapshot, error)
	ActivateGlobal(context.Context, json.RawMessage, uuid.UUID, string) (domainpolicy.GlobalSnapshot, error)
}

type IdempotencyRepository interface {
	FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error)
	CreateProcessing(context.Context, domainidempotency.Record, string) error
	ClaimProcessing(context.Context, domainidempotency.Record, string) (domainidempotency.Record, bool, error)
	AttachReplayPayload(context.Context, uuid.UUID, uuid.UUID) error
	Complete(context.Context, uuid.UUID, string) error
	CreateReplayPayload(context.Context, domainidempotency.ReplayPayload) error
	FindReplayPayloadForUpdate(context.Context, uuid.UUID) (domainidempotency.ReplayPayload, error)
	RecordReplay(context.Context, uuid.UUID) error
}

type OutboxAppender interface {
	Append(context.Context, domainoutbox.Event) error
}

type AuditAppender interface {
	Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error
}

type TxRepositories struct {
	Operators   OperatorRepository
	Policies    PolicyRepository
	Idempotency IdempotencyRepository
	Outbox      OutboxAppender
	Audit       AuditAppender
}

type Transactor interface {
	WithinTransaction(context.Context, func(TxRepositories) error) error
}

type Cryptography interface {
	Hash(...string) []byte
	SealPolicyUpdate(PolicyUpdateOutput) ([]byte, error)
	OpenPolicyUpdate([]byte) (PolicyUpdateOutput, error)
}

type AuthorizationDecisionPort interface {
	Verify(context.Context, string, string, string, string) error
}

type Clock interface {
	Now() time.Time
}
