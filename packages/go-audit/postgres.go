package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type Record struct {
	Event    Event
	Attempts int
}

func Append(ctx context.Context, tx pgx.Tx, event Event) (bool, error) {
	if tx == nil {
		return false, oops.In("audit_postgres").Code("audit.transaction_required").New("pgx transaction is required")
	}
	if err := event.Validate(); err != nil {
		return false, err
	}
	actor, resource, metadata, err := encodeEnvelope(event)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(ctx, `
		INSERT INTO audit_outbox (
			id, event_name, event_version, occurred_at, actor, resource,
			payload, metadata, idempotency_key
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (event_name, idempotency_key) DO NOTHING
	`, event.ID, event.Name, event.Version, event.OccurredAt, actor, resource, event.Payload, metadata, event.IdempotencyKey)
	if err != nil {
		return false, oops.In("audit_postgres").Code("audit.append_failed").With("event_id", event.ID.String()).Wrap(err)
	}
	return result.RowsAffected() == 1, nil
}

func Claim(ctx context.Context, pool *pgxpool.Pool, workerID string, batchSize int, lease time.Duration) ([]Record, error) {
	rows, err := pool.Query(ctx, `
		WITH candidates AS (
			SELECT id
			FROM audit_outbox
			WHERE (status = 'pending' AND available_at <= now())
			   OR (status = 'processing' AND lease_until < now())
			ORDER BY occurred_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE audit_outbox AS outbox
		SET status = 'processing',
			lease_owner = $2,
			lease_until = now() + ($3 * interval '1 millisecond'),
			attempt_count = attempt_count + 1
		FROM candidates
		WHERE outbox.id = candidates.id
		RETURNING outbox.id, outbox.event_name, outbox.event_version,
			outbox.occurred_at, outbox.actor, outbox.resource, outbox.payload,
			outbox.metadata, outbox.idempotency_key, outbox.attempt_count
	`, batchSize, workerID, lease.Milliseconds())
	if err != nil {
		return nil, oops.In("audit_postgres").Code("audit.claim_failed").Wrap(err)
	}
	defer rows.Close()

	records := make([]Record, 0, batchSize)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.In("audit_postgres").Code("audit.claim_rows_failed").Wrap(err)
	}
	return records, nil
}

func Archive(ctx context.Context, pool *pgxpool.Pool, event Event) error {
	actor, resource, metadata, err := encodeEnvelope(event)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO audit_events (
			id, event_name, event_version, occurred_at, actor, resource,
			payload, metadata, idempotency_key
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO NOTHING
	`, event.ID, event.Name, event.Version, event.OccurredAt, actor, resource, event.Payload, metadata, event.IdempotencyKey)
	if err != nil {
		return oops.In("audit_postgres").Code("audit.archive_failed").With("event_id", event.ID.String()).Wrap(err)
	}
	return nil
}

func MarkDelivered(ctx context.Context, pool *pgxpool.Pool, workerID string, eventID uuid.UUID) error {
	result, err := pool.Exec(ctx, `
		UPDATE audit_outbox
		SET status = 'delivered', delivered_at = now(), lease_owner = NULL,
			lease_until = NULL, last_error = NULL
		WHERE id = $1 AND status = 'processing' AND lease_owner = $2
	`, eventID, workerID)
	if err != nil {
		return oops.In("audit_postgres").Code("audit.mark_delivered_failed").With("event_id", eventID.String()).Wrap(err)
	}
	return requireOneRow(result.RowsAffected(), eventID, "mark delivered")
}

func MarkFailed(
	ctx context.Context,
	pool *pgxpool.Pool,
	workerID string,
	eventID uuid.UUID,
	attempts int,
	maxAttempts int,
	retryAfter time.Duration,
	cause error,
) error {
	status := "pending"
	if attempts >= maxAttempts {
		status = "dead"
	}
	message := "unknown audit delivery error"
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > 4000 {
		message = message[:4000]
	}
	result, err := pool.Exec(ctx, `
		UPDATE audit_outbox
		SET status = $3,
			available_at = now() + ($4 * interval '1 millisecond'),
			lease_owner = NULL,
			lease_until = NULL,
			last_error = $5
		WHERE id = $1 AND status = 'processing' AND lease_owner = $2
	`, eventID, workerID, status, retryAfter.Milliseconds(), message)
	if err != nil {
		return oops.In("audit_postgres").Code("audit.mark_failed_failed").With("event_id", eventID.String()).Wrap(err)
	}
	return requireOneRow(result.RowsAffected(), eventID, "mark failed")
}

func DeleteDeliveredBefore(ctx context.Context, pool *pgxpool.Pool, before time.Time, limit int) (int64, error) {
	result, err := pool.Exec(ctx, `
		WITH expired AS (
			SELECT id FROM audit_outbox
			WHERE status = 'delivered' AND delivered_at < $1
			ORDER BY delivered_at
			LIMIT $2
		)
		DELETE FROM audit_outbox USING expired WHERE audit_outbox.id = expired.id
	`, before, limit)
	if err != nil {
		return 0, oops.In("audit_postgres").Code("audit.cleanup_failed").Wrap(err)
	}
	return result.RowsAffected(), nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (Record, error) {
	var record Record
	var actor, resource, payload, metadata []byte
	if err := row.Scan(
		&record.Event.ID,
		&record.Event.Name,
		&record.Event.Version,
		&record.Event.OccurredAt,
		&actor,
		&resource,
		&payload,
		&metadata,
		&record.Event.IdempotencyKey,
		&record.Attempts,
	); err != nil {
		return Record{}, oops.In("audit_postgres").Code("audit.scan_failed").Wrap(err)
	}
	if err := json.Unmarshal(actor, &record.Event.Actor); err != nil {
		return Record{}, oops.In("audit_postgres").Code("audit.actor_decode_failed").Wrap(err)
	}
	if err := json.Unmarshal(resource, &record.Event.Resource); err != nil {
		return Record{}, oops.In("audit_postgres").Code("audit.resource_decode_failed").Wrap(err)
	}
	if err := json.Unmarshal(metadata, &record.Event.Metadata); err != nil {
		return Record{}, oops.In("audit_postgres").Code("audit.metadata_decode_failed").Wrap(err)
	}
	record.Event.Payload = append(json.RawMessage(nil), payload...)
	return record, nil
}

func encodeEnvelope(event Event) ([]byte, []byte, []byte, error) {
	actor, err := json.Marshal(event.Actor)
	if err != nil {
		return nil, nil, nil, oops.In("audit_postgres").Code("audit.actor_encode_failed").Wrap(err)
	}
	resource, err := json.Marshal(event.Resource)
	if err != nil {
		return nil, nil, nil, oops.In("audit_postgres").Code("audit.resource_encode_failed").Wrap(err)
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return nil, nil, nil, oops.In("audit_postgres").Code("audit.metadata_encode_failed").Wrap(err)
	}
	return actor, resource, metadata, nil
}

func requireOneRow(rowsAffected int64, eventID uuid.UUID, operation string) error {
	if rowsAffected == 1 {
		return nil
	}
	return oops.
		In("audit_postgres").
		Code("audit.lease_lost").
		With("event_id", eventID.String(), "operation", operation).
		New("audit outbox lease is no longer owned")
}
