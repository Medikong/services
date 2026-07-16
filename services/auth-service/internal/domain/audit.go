package domain

import (
	"context"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AppendAudit writes a deliberately small, non-sensitive audit envelope into
// the shared transactional audit outbox. Callers pass only opaque identifiers
// and state names; identities, codes, passwords, and credentials never belong
// in the payload.
func AppendAudit(ctx context.Context, tx pgx.Tx, name, actorType string, actorID, resourceID uuid.UUID, payload any, idempotencyKey string) error {
	encoded, err := audit.MarshalPayload(payload, "email", "phone", "code", "destination", "identity", "user_id")
	if err != nil {
		return err
	}
	event := audit.NewEvent(name, 1,
		audit.Actor{Type: actorType, ID: actorID.String()},
		audit.Resource{Type: "Registration", ID: resourceID.String()},
		encoded, idempotencyKey,
	)
	_, err = audit.Append(ctx, tx, event)
	return err
}
