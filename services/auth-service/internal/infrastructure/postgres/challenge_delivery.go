package postgres

import (
	"context"
	"time"

	applicationchallenge "github.com/Medikong/services/services/auth-service/internal/application/challenge"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type ChallengeDeliveryRepository struct {
	pool *pgxpool.Pool
}

func NewChallengeDeliveryRepository(pool *pgxpool.Pool) *ChallengeDeliveryRepository {
	return &ChallengeDeliveryRepository{pool: pool}
}

func (r *ChallengeDeliveryRepository) Claim(ctx context.Context, workerID string, batchSize int, lease time.Duration) ([]applicationchallenge.ClaimedDelivery, error) {
	rows, err := r.pool.Query(ctx, `
		WITH candidates AS (
			SELECT delivery_payload_id
			FROM auth_verification_delivery_payloads
			WHERE delivery_status = 'pending' AND next_attempt_at <= now() AND expires_at > now()
				AND (lease_until IS NULL OR lease_until <= now())
			ORDER BY created_at, delivery_payload_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		), claimed AS (
			UPDATE auth_verification_delivery_payloads delivery
			SET delivery_attempts = delivery_attempts + 1, lease_owner = $2,
				lease_until = now() + $3::interval, last_error_code = NULL
			FROM candidates
			WHERE delivery.delivery_payload_id = candidates.delivery_payload_id
			RETURNING delivery.delivery_payload_id, delivery.challenge_id, delivery.payload_ciphertext,
				delivery.expires_at, delivery.delivery_attempts
		)
		SELECT claimed.delivery_payload_id, challenge.channel, claimed.payload_ciphertext,
			claimed.expires_at, claimed.delivery_attempts
		FROM claimed
		JOIN auth_challenges challenge ON challenge.challenge_id = claimed.challenge_id
	`, batchSize, workerID, lease.String())
	if err != nil {
		return nil, oops.In("challenge_delivery_repository").Code("delivery.claim_failed").Wrap(err)
	}
	defer rows.Close()
	var result []applicationchallenge.ClaimedDelivery
	for rows.Next() {
		var delivery applicationchallenge.ClaimedDelivery
		if err := rows.Scan(&delivery.ID, &delivery.Channel, &delivery.Ciphertext, &delivery.ExpiresAt, &delivery.Attempts); err != nil {
			return nil, oops.In("challenge_delivery_repository").Code("delivery.scan_failed").Wrap(err)
		}
		result = append(result, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.In("challenge_delivery_repository").Code("delivery.rows_failed").Wrap(err)
	}
	return result, nil
}

func (r *ChallengeDeliveryRepository) MarkDelivered(ctx context.Context, deliveryID uuid.UUID, workerID, providerRequestID string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_verification_delivery_payloads
		SET delivery_status = 'delivered', provider_request_id = $3, delivered_at = now(),
			payload_ciphertext = NULL, payload_key_id = NULL, aad_hash = NULL,
			destroyed_at = now(), lease_owner = NULL, lease_until = NULL, last_error_code = NULL
		WHERE delivery_payload_id = $1 AND delivery_status = 'pending' AND lease_owner = $2
	`, deliveryID, workerID, providerRequestID)
	if err != nil {
		return oops.In("challenge_delivery_repository").Code("delivery.mark_delivered_failed").Wrap(err)
	}
	return requireDeliveryLease(result.RowsAffected())
}

func (r *ChallengeDeliveryRepository) Retry(ctx context.Context, deliveryID uuid.UUID, workerID string, delay time.Duration, errorCode string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_verification_delivery_payloads
		SET next_attempt_at = now() + $3::interval, lease_owner = NULL, lease_until = NULL,
			last_error_code = $4
		WHERE delivery_payload_id = $1 AND delivery_status = 'pending' AND lease_owner = $2
	`, deliveryID, workerID, delay.String(), errorCode)
	if err != nil {
		return oops.In("challenge_delivery_repository").Code("delivery.retry_failed").Wrap(err)
	}
	return requireDeliveryLease(result.RowsAffected())
}

func (r *ChallengeDeliveryRepository) Fail(ctx context.Context, deliveryID uuid.UUID, workerID, errorCode string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_verification_delivery_payloads
		SET delivery_status = 'failed', payload_ciphertext = NULL, payload_key_id = NULL,
			aad_hash = NULL, destroyed_at = now(), lease_owner = NULL, lease_until = NULL,
			last_error_code = $3
		WHERE delivery_payload_id = $1 AND delivery_status = 'pending' AND lease_owner = $2
	`, deliveryID, workerID, errorCode)
	if err != nil {
		return oops.In("challenge_delivery_repository").Code("delivery.mark_failed_failed").Wrap(err)
	}
	return requireDeliveryLease(result.RowsAffected())
}

func requireDeliveryLease(rowsAffected int64) error {
	if rowsAffected == 1 {
		return nil
	}
	return oops.In("challenge_delivery_repository").Code("delivery.lease_lost").New("verification delivery lease was lost")
}

var _ applicationchallenge.DeliveryRepository = (*ChallengeDeliveryRepository)(nil)
