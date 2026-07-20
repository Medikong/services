package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

var ErrLeaseLost = errors.New("auth outbox lease lost")

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Append(ctx context.Context, tx pgx.Tx, event domainoutbox.Event) error {
	return Append(ctx, tx, event)
}

// Append persists an event through a transaction already owned by a use case.
func Append(ctx context.Context, tx pgx.Tx, event domainoutbox.Event) error {
	if event.ID == uuid.Nil || event.AggregateID == uuid.Nil || event.CorrelationID == uuid.Nil || event.Type == "" || event.AggregateType == "" || event.Version < 0 || !json.Valid(event.Payload) {
		return fmt.Errorf("invalid auth outbox event")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO auth_outbox_events (
			event_id, aggregate_type, aggregate_id, aggregate_version, event_type,
			payload, correlation_id, occurred_at, publish_status, next_attempt_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, now(), 'pending', now())
	`, event.ID, event.AggregateType, event.AggregateID, event.Version, event.Type, event.Payload, event.CorrelationID)
	return err
}

func (r *PostgresRepository) ClaimPublishBatch(ctx context.Context, workerID string, batchSize int, lease time.Duration) ([]ClaimedEvent, error) {
	if workerID == "" || batchSize < 1 || lease <= 0 {
		return nil, fmt.Errorf("invalid auth outbox claim")
	}
	rows, err := r.pool.Query(ctx, `
		WITH candidates AS (
			SELECT event_id
			FROM auth_outbox_events
			WHERE (publish_status = 'pending' AND next_attempt_at <= now())
				OR (publish_status = 'publishing' AND lease_until <= now())
			ORDER BY occurred_at, event_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE auth_outbox_events event
		SET publish_status = 'publishing', publish_attempts = event.publish_attempts + 1,
			lease_owner = $2, lease_until = now() + $3::interval,
			last_error_code = NULL
		FROM candidates
		WHERE event.event_id = candidates.event_id
		RETURNING event.event_id, event.aggregate_type, event.aggregate_id,
			event.aggregate_version, event.event_type, event.payload,
			event.correlation_id, event.occurred_at, event.publish_attempts
	`, batchSize, workerID, lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ClaimedEvent, 0, batchSize)
	for rows.Next() {
		var event ClaimedEvent
		if err := rows.Scan(
			&event.ID, &event.AggregateType, &event.AggregateID, &event.Version,
			&event.Type, &event.Payload, &event.CorrelationID, &event.OccurredAt, &event.Attempts,
		); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) MarkPublished(ctx context.Context, eventID uuid.UUID, workerID string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_outbox_events
		SET publish_status = 'published', published_at = now(), lease_owner = NULL,
			lease_until = NULL, last_error_code = NULL
		WHERE event_id = $1 AND publish_status = 'publishing' AND lease_owner = $2
	`, eventID, workerID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (r *PostgresRepository) ReleaseForRetry(ctx context.Context, eventID uuid.UUID, workerID string, delay time.Duration, errorCode string) error {
	if delay <= 0 || errorCode == "" {
		return fmt.Errorf("invalid auth outbox retry")
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_outbox_events
		SET publish_status = 'pending', next_attempt_at = now() + $3::interval,
			lease_owner = NULL, lease_until = NULL, last_error_code = $4
		WHERE event_id = $1 AND publish_status = 'publishing' AND lease_owner = $2
	`, eventID, workerID, delay.String(), errorCode)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (r *PostgresRepository) MarkDeadLetter(ctx context.Context, eventID uuid.UUID, workerID, errorCode string) error {
	if errorCode == "" {
		return fmt.Errorf("invalid auth outbox dead letter")
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_outbox_events
		SET publish_status = 'dead_letter', lease_owner = NULL, lease_until = NULL,
			last_error_code = $3
		WHERE event_id = $1 AND publish_status = 'publishing' AND lease_owner = $2
	`, eventID, workerID, errorCode)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}
