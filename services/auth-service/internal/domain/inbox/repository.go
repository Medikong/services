package inbox

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repository interface {
	Receive(context.Context, pgx.Tx, Message) (Message, bool, error)
	MarkProcessed(context.Context, pgx.Tx, string, uuid.UUID) error
	MarkRejected(context.Context, pgx.Tx, string, uuid.UUID, string) error
}
