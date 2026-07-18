package outbox

import (
	"context"
	"time"

	"github.com/samber/oops"
)

func (r *PostgresRepository) ClaimPublishBatchByType(ctx context.Context, workerID, eventType string, batchSize int, lease time.Duration) ([]ClaimedEvent, error) {
	if workerID == "" || eventType == "" || batchSize < 1 || lease <= 0 {
		return nil, oops.In("auth_outbox").Code("typed_claim.invalid").New("invalid typed auth outbox claim")
	}
	rows, err := r.pool.Query(ctx, `
		WITH candidates AS (
			SELECT event_id
			FROM auth_outbox_events
			WHERE event_type = $4 AND (
				(publish_status = 'pending' AND next_attempt_at <= now())
				OR (publish_status = 'publishing' AND lease_until <= now())
			)
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
	`, batchSize, workerID, lease.String(), eventType)
	if err != nil {
		return nil, oops.In("auth_outbox").Code("typed_claim.query_failed").Wrap(err)
	}
	defer rows.Close()
	result := make([]ClaimedEvent, 0, batchSize)
	for rows.Next() {
		var event ClaimedEvent
		if err := rows.Scan(
			&event.ID, &event.AggregateType, &event.AggregateID, &event.Version,
			&event.Type, &event.Payload, &event.CorrelationID, &event.OccurredAt, &event.Attempts,
		); err != nil {
			return nil, oops.In("auth_outbox").Code("typed_claim.scan_failed").Wrap(err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.In("auth_outbox").Code("typed_claim.rows_failed").Wrap(err)
	}
	return result, nil
}
