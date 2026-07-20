package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	applicationsessionprojection "github.com/Medikong/services/services/auth-service/internal/application/sessionprojection"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

var ErrSessionStatusProjectionLeaseLost = errors.New("auth session status projection lease lost")

type SessionStatusProjectionAppender struct {
	tx pgx.Tx
}

func NewSessionStatusProjectionAppender(tx pgx.Tx) *SessionStatusProjectionAppender {
	return &SessionStatusProjectionAppender{tx: tx}
}

func (a *SessionStatusProjectionAppender) Enqueue(ctx context.Context, changes []domainsession.StatusChange) error {
	if a == nil || a.tx == nil {
		return oops.In("session_status_projection").Code("projection.transaction_required").New("session status projection transaction is required")
	}
	for _, change := range changes {
		if err := change.Validate(); err != nil {
			return oops.In("session_status_projection").Code("projection.invalid_change").Wrap(err)
		}
		_, err := a.tx.Exec(ctx, `
			INSERT INTO auth_session_status_projection_jobs (
				job_id, session_id, user_id, session_version, target_status,
				valid_until, occurred_at, delivery_status, available_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', now())
			ON CONFLICT (session_id, session_version) DO NOTHING
		`, uuid.New(), change.SessionID, change.UserID, change.Version, change.Status, change.ValidUntil, change.OccurredAt)
		if err != nil {
			return oops.In("session_status_projection").Code("projection.enqueue_failed").Wrap(err)
		}
	}
	return nil
}

type SessionStatusProjectionRepository struct {
	pool *pgxpool.Pool
}

func NewSessionStatusProjectionRepository(pool *pgxpool.Pool) *SessionStatusProjectionRepository {
	return &SessionStatusProjectionRepository{pool: pool}
}

func (r *SessionStatusProjectionRepository) Claim(ctx context.Context, workerID string, batchSize int, lease time.Duration) ([]applicationsessionprojection.ClaimedChange, error) {
	if r == nil || r.pool == nil || strings.TrimSpace(workerID) == "" || batchSize < 1 || lease <= 0 {
		return nil, oops.In("session_status_projection").Code("projection.invalid_claim").New("invalid session status projection claim")
	}
	rows, err := r.pool.Query(ctx, `
		WITH candidates AS (
			SELECT job_id
			FROM auth_session_status_projection_jobs
			WHERE (delivery_status = 'pending' AND available_at <= now())
			   OR (delivery_status = 'processing' AND lease_until <= now())
			ORDER BY occurred_at, job_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE auth_session_status_projection_jobs AS job
		SET delivery_status = 'processing', attempt_count = job.attempt_count + 1,
			lease_owner = $2, lease_until = now() + $3::interval,
			last_error_code = NULL
		FROM candidates
		WHERE job.job_id = candidates.job_id
		RETURNING job.job_id, job.session_id, job.user_id, job.target_status,
			job.session_version, job.valid_until, job.occurred_at, job.attempt_count
	`, batchSize, workerID, lease.String())
	if err != nil {
		return nil, oops.In("session_status_projection").Code("projection.claim_failed").Wrap(err)
	}
	defer rows.Close()

	claimed := make([]applicationsessionprojection.ClaimedChange, 0, batchSize)
	for rows.Next() {
		var change applicationsessionprojection.ClaimedChange
		if err := rows.Scan(
			&change.JobID,
			&change.SessionID,
			&change.UserID,
			&change.Status,
			&change.Version,
			&change.ValidUntil,
			&change.OccurredAt,
			&change.Attempts,
		); err != nil {
			return nil, oops.In("session_status_projection").Code("projection.scan_failed").Wrap(err)
		}
		claimed = append(claimed, change)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.In("session_status_projection").Code("projection.rows_failed").Wrap(err)
	}
	return claimed, nil
}

func (r *SessionStatusProjectionRepository) MarkDelivered(ctx context.Context, jobID uuid.UUID, workerID string) error {
	if jobID == uuid.Nil || strings.TrimSpace(workerID) == "" {
		return oops.In("session_status_projection").Code("projection.invalid_delivery").New("invalid session status projection delivery")
	}
	var delivered bool
	err := r.pool.QueryRow(ctx, `
		WITH delivered_job AS (
			UPDATE auth_session_status_projection_jobs
			SET delivery_status = 'delivered', delivered_at = now(),
				lease_owner = NULL, lease_until = NULL, last_error_code = NULL
			WHERE job_id = $1 AND delivery_status = 'processing' AND lease_owner = $2
			RETURNING session_id, session_version
		), acknowledged_status_event AS (
			UPDATE auth_outbox_events AS event
			SET publish_status = 'published', published_at = COALESCE(event.published_at, now()),
				lease_owner = NULL, lease_until = NULL, last_error_code = NULL
			FROM delivered_job AS job
			WHERE event.aggregate_type = 'Session'
			  AND event.aggregate_id = job.session_id
			  AND event.aggregate_version = job.session_version
			  AND event.event_type = 'Auth.SessionStatusCacheUpdated'
			RETURNING event.event_id
		)
		SELECT EXISTS (SELECT 1 FROM delivered_job)
	`, jobID, workerID).Scan(&delivered)
	if err != nil {
		return oops.In("session_status_projection").Code("projection.mark_delivered_failed").Wrap(err)
	}
	if !delivered {
		return ErrSessionStatusProjectionLeaseLost
	}
	return nil
}

func (r *SessionStatusProjectionRepository) ReleaseForRetry(ctx context.Context, jobID uuid.UUID, workerID string, delay time.Duration, errorCode string) error {
	if jobID == uuid.Nil || strings.TrimSpace(workerID) == "" || delay <= 0 || strings.TrimSpace(errorCode) == "" {
		return oops.In("session_status_projection").Code("projection.invalid_retry").New("invalid session status projection retry")
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_session_status_projection_jobs
		SET delivery_status = 'pending', available_at = now() + $3::interval,
			lease_owner = NULL, lease_until = NULL, last_error_code = $4
		WHERE job_id = $1 AND delivery_status = 'processing' AND lease_owner = $2
	`, jobID, workerID, delay.String(), errorCode)
	if err != nil {
		return oops.In("session_status_projection").Code("projection.retry_failed").Wrap(err)
	}
	return requireSessionStatusProjectionLease(result.RowsAffected())
}

func (r *SessionStatusProjectionRepository) DeleteDeliveredBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if before.IsZero() || limit < 1 {
		return 0, oops.In("session_status_projection").Code("projection.invalid_cleanup").New("invalid session status projection cleanup")
	}
	result, err := r.pool.Exec(ctx, `
		WITH expired AS (
			SELECT job_id
			FROM auth_session_status_projection_jobs
			WHERE delivery_status = 'delivered' AND delivered_at < $1
			ORDER BY delivered_at, job_id
			LIMIT $2
		)
		DELETE FROM auth_session_status_projection_jobs AS job
		USING expired
		WHERE job.job_id = expired.job_id
	`, before, limit)
	if err != nil {
		return 0, oops.In("session_status_projection").Code("projection.cleanup_failed").Wrap(err)
	}
	return result.RowsAffected(), nil
}

func requireSessionStatusProjectionLease(rowsAffected int64) error {
	if rowsAffected == 1 {
		return nil
	}
	return ErrSessionStatusProjectionLeaseLost
}

var _ applicationsessionprojection.ProjectionAppender = (*SessionStatusProjectionAppender)(nil)
var _ applicationsessionprojection.Repository = (*SessionStatusProjectionRepository)(nil)
