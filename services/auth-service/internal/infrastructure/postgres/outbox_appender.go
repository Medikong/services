package postgres

import (
	"context"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	outboxinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/messaging/outbox"
	"github.com/jackc/pgx/v5"
)

type OutboxAppender struct {
	tx pgx.Tx
}

func NewOutboxAppender(tx pgx.Tx) *OutboxAppender {
	return &OutboxAppender{tx: tx}
}

func (a *OutboxAppender) Append(ctx context.Context, event domainoutbox.Event) error {
	return outboxinfra.Append(ctx, a.tx, event)
}

var _ applicationsession.OutboxAppender = (*OutboxAppender)(nil)
