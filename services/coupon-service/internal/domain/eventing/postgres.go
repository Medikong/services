package eventing

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PostgresOutbox struct {
	pool *pgxpool.Pool
}

func NewPostgresOutbox(pool *pgxpool.Pool) *PostgresOutbox {
	return &PostgresOutbox{pool: pool}
}

func (r *PostgresOutbox) Claim(ctx context.Context, workerID string, batch int, lease time.Duration) ([]OutboxItem, error) {
	if r == nil || r.pool == nil {
		return nil, oops.In("coupon_outbox_repository").Code("coupon.pool_required").New("postgres pool is required")
	}
	if batch < 1 || lease <= 0 || workerID == "" {
		return nil, oops.In("coupon_outbox_repository").Code("coupon.outbox_claim_invalid").New("outbox claim settings are invalid")
	}
	rows, err := r.pool.Query(ctx, `
		WITH candidates AS (
			SELECT candidate.event_id
			FROM domain_outbox AS candidate
			WHERE (
				(candidate.publish_status = 'pending' AND candidate.next_attempt_at <= now()) OR
				(candidate.publish_status = 'publishing' AND candidate.lease_until < now())
			)
			AND NOT EXISTS (
				SELECT 1
				FROM domain_outbox AS predecessor
				WHERE predecessor.aggregate_type=candidate.aggregate_type
				  AND predecessor.aggregate_id=candidate.aggregate_id
				  AND (
					predecessor.aggregate_version<candidate.aggregate_version OR
					(predecessor.aggregate_version=candidate.aggregate_version AND predecessor.event_sequence<candidate.event_sequence)
				  )
				  AND predecessor.publish_status<>'published'
			)
			ORDER BY candidate.event_sequence
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE domain_outbox AS item
		SET publish_status='publishing', lease_owner=$2, lease_until=now()+$3::interval,
			attempt_count=item.attempt_count+1
		FROM candidates
		WHERE item.event_id=candidates.event_id
		RETURNING item.event_id, item.event_document_id, item.event_type,
			item.aggregate_type, item.aggregate_id, item.aggregate_version,
			item.occurred_at, item.correlation_id, COALESCE(item.causation_id, ''),
			item.payload_schema_version, item.payload, item.attempt_count,
			COALESCE(item.trace_id, ''), item.lease_owner, item.lease_until
	`, batch, workerID, lease.String())
	if err != nil {
		return nil, oops.In("coupon_outbox_repository").Code("coupon.outbox_claim_failed").Wrap(err)
	}
	defer rows.Close()
	items := make([]OutboxItem, 0, batch)
	for rows.Next() {
		var item OutboxItem
		var payload []byte
		if err := rows.Scan(
			&item.Envelope.EventID, &item.Envelope.EventDocumentID, &item.Envelope.EventType,
			&item.Envelope.AggregateType, &item.Envelope.AggregateID, &item.Envelope.AggregateVersion,
			&item.Envelope.OccurredAt, &item.Envelope.CorrelationID, &item.Envelope.CausationID,
			&item.Envelope.PayloadSchemaVersion, &payload, &item.AttemptCount,
			&item.TraceID, &item.LeaseOwner, &item.LeaseUntil,
		); err != nil {
			return nil, oops.In("coupon_outbox_repository").Code("coupon.outbox_scan_failed").Wrap(err)
		}
		data, err := decodeData(payload)
		if err != nil {
			return nil, oops.In("coupon_outbox_repository").Code("coupon.outbox_payload_decode_failed").Wrap(err)
		}
		item.Envelope.Data = data
		item.Envelope.TraceID = item.TraceID
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.In("coupon_outbox_repository").Code("coupon.outbox_rows_failed").Wrap(err)
	}
	return items, nil
}

func (r *PostgresOutbox) MarkPublished(ctx context.Context, eventID uuid.UUID, workerID string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE domain_outbox
		SET publish_status='published', published_at=now(), lease_owner=NULL, lease_until=NULL,
			last_error_code=NULL
		WHERE event_id=$1 AND publish_status='publishing' AND lease_owner=$2
	`, eventID, workerID)
	if err != nil {
		return oops.In("coupon_outbox_repository").Code("coupon.outbox_publish_ack_failed").Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_outbox_repository").Code("coupon.outbox_lease_lost").New("outbox lease was lost before publish acknowledgement")
	}
	return nil
}

func (r *PostgresOutbox) MarkFailed(ctx context.Context, eventID uuid.UUID, workerID string, next time.Time, code string, terminal bool) error {
	status := "pending"
	if terminal {
		status = "dead_letter"
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE domain_outbox
		SET publish_status=$3, next_attempt_at=$4, lease_owner=NULL, lease_until=NULL,
			last_error_code=$5
		WHERE event_id=$1 AND publish_status='publishing' AND lease_owner=$2
	`, eventID, workerID, status, next, code)
	if err != nil {
		return oops.In("coupon_outbox_repository").Code("coupon.outbox_failure_record_failed").Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_outbox_repository").Code("coupon.outbox_lease_lost").New("outbox lease was lost before failure acknowledgement")
	}
	return nil
}

var _ OutboxRepository = (*PostgresOutbox)(nil)
