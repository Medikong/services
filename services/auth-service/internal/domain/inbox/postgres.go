package inbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("auth inbox message not found")

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Receive records transport idempotency before application state is changed.
// The returned boolean is true only for the first delivery of a source event.
func (r *PostgresRepository) Receive(ctx context.Context, tx pgx.Tx, message Message) (Message, bool, error) {
	if tx == nil || message.Consumer == "" || message.SourceEventID == uuid.Nil || message.Type == "" || message.SchemaVersion < 1 || message.BusinessKey == uuid.Nil || message.LinkRequestID == uuid.Nil || message.CausationID == uuid.Nil || len(message.PayloadHash) != 32 {
		return Message{}, false, fmt.Errorf("invalid auth inbox message")
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO auth_inbox_messages (
			consumer_name, source_event_id, message_type, schema_version, business_key,
			link_request_id, causation_id, payload, payload_hash, process_status, received_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'received', now())
		ON CONFLICT (consumer_name, source_event_id) DO NOTHING
	`, message.Consumer, message.SourceEventID, message.Type, message.SchemaVersion, message.BusinessKey,
		message.LinkRequestID, message.CausationID, message.Payload, message.PayloadHash)
	if err != nil {
		return Message{}, false, err
	}
	if result.RowsAffected() == 1 {
		message.Status = StatusReceived
		return message, true, nil
	}
	var existing Message
	err = tx.QueryRow(ctx, `
		SELECT consumer_name, source_event_id, message_type, schema_version, business_key,
			link_request_id, causation_id, payload, payload_hash, process_status, received_at
		FROM auth_inbox_messages
		WHERE consumer_name = $1 AND source_event_id = $2
		FOR UPDATE
	`, message.Consumer, message.SourceEventID).Scan(
		&existing.Consumer, &existing.SourceEventID, &existing.Type, &existing.SchemaVersion,
		&existing.BusinessKey, &existing.LinkRequestID, &existing.CausationID, &existing.Payload,
		&existing.PayloadHash, &existing.Status, &existing.ReceivedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, false, ErrNotFound
	}
	return existing, false, err
}

func (r *PostgresRepository) MarkProcessed(ctx context.Context, tx pgx.Tx, consumer string, sourceEventID uuid.UUID) error {
	result, err := tx.Exec(ctx, `
		UPDATE auth_inbox_messages
		SET process_status = 'processed', processed_at = now(), last_error_code = NULL
		WHERE consumer_name = $1 AND source_event_id = $2 AND process_status = 'received'
	`, consumer, sourceEventID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) MarkRejected(ctx context.Context, tx pgx.Tx, consumer string, sourceEventID uuid.UUID, errorCode string) error {
	if errorCode == "" {
		return fmt.Errorf("missing auth inbox rejection code")
	}
	result, err := tx.Exec(ctx, `
		UPDATE auth_inbox_messages
		SET process_status = 'rejected', processed_at = now(), last_error_code = $3
		WHERE consumer_name = $1 AND source_event_id = $2 AND process_status = 'received'
	`, consumer, sourceEventID, errorCode)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
