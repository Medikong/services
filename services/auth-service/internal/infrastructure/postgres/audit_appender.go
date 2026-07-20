package postgres

import (
	"context"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AuditAppender struct {
	tx pgx.Tx
}

func NewAuditAppender(tx pgx.Tx) *AuditAppender {
	return &AuditAppender{tx: tx}
}

func (a *AuditAppender) Append(ctx context.Context, name, actorType string, actorID, resourceID uuid.UUID, payload map[string]string, idempotencyKey string) error {
	encoded, err := audit.MarshalPayload(payload, "email", "phone", "code", "destination", "identity", "user_id")
	if err != nil {
		return err
	}
	event := audit.NewEvent(name, 1,
		audit.Actor{Type: actorType, ID: actorID.String()},
		audit.Resource{Type: "Registration", ID: resourceID.String()},
		encoded, idempotencyKey,
	)
	_, err = audit.Append(ctx, a.tx, event)
	return err
}
