package challenge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type DeliveryConfig struct {
	WorkerID       string
	EmailURL       string
	SMSURL         string
	EmailToken     string
	SMSToken       string
	RequestTimeout time.Duration
	PollInterval   time.Duration
	Lease          time.Duration
	BatchSize      int
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

type claimedDelivery struct {
	ID         uuid.UUID
	Channel    Channel
	Ciphertext []byte
	ExpiresAt  time.Time
	Attempts   int
}

type deliverySecret struct {
	Code        string `json:"code"`
	Destination string `json:"destination"`
}

type providerResponse struct {
	RequestID string `json:"requestId"`
}

type DeliveryService struct {
	repository *PostgresRepository
	keys       security.Keys
	client     *http.Client
	config     DeliveryConfig
}

func NewDeliveryService(repository *PostgresRepository, keys security.Keys, config DeliveryConfig) (*DeliveryService, error) {
	if repository == nil || strings.TrimSpace(config.WorkerID) == "" || strings.TrimSpace(config.EmailURL) == "" || strings.TrimSpace(config.SMSURL) == "" || config.RequestTimeout <= 0 || config.PollInterval <= 0 || config.Lease < 2*config.RequestTimeout || config.BatchSize < 1 || config.MaxAttempts < 1 || config.BaseBackoff <= 0 || config.MaxBackoff < config.BaseBackoff {
		return nil, oops.In("auth_delivery").Code("delivery.invalid_config").New("invalid verification delivery configuration")
	}
	return &DeliveryService{
		repository: repository,
		keys:       keys,
		client:     &http.Client{Timeout: config.RequestTimeout},
		config:     config,
	}, nil
}

func (s *DeliveryService) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()
	for {
		if err := s.runOnce(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *DeliveryService) runOnce(ctx context.Context) error {
	deliveries, err := s.repository.claimDeliveries(ctx, s.config.WorkerID, s.config.BatchSize, s.config.Lease)
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		var secret deliverySecret
		if err := s.keys.Open(delivery.Ciphertext, &secret); err != nil || len(secret.Code) != 6 || strings.TrimSpace(secret.Destination) == "" {
			if markErr := s.repository.failDelivery(ctx, delivery.ID, s.config.WorkerID, "payload_invalid"); markErr != nil {
				return markErr
			}
			continue
		}
		requestID, retry, errorCode := s.send(ctx, delivery, secret)
		secret = deliverySecret{}
		if errorCode == "" {
			if err := s.repository.deliver(ctx, delivery.ID, s.config.WorkerID, requestID); err != nil {
				return err
			}
			continue
		}
		if !retry || delivery.Attempts >= s.config.MaxAttempts || !time.Now().UTC().Before(delivery.ExpiresAt) {
			if err := s.repository.failDelivery(ctx, delivery.ID, s.config.WorkerID, errorCode); err != nil {
				return err
			}
			continue
		}
		if err := s.repository.retryDelivery(ctx, delivery.ID, s.config.WorkerID, deliveryBackoff(s.config, delivery.Attempts), errorCode); err != nil {
			return err
		}
	}
	return nil
}

func (s *DeliveryService) send(ctx context.Context, delivery claimedDelivery, secret deliverySecret) (string, bool, string) {
	endpoint, token := s.config.EmailURL, s.config.EmailToken
	if delivery.Channel == ChannelSMSCode {
		endpoint, token = s.config.SMSURL, s.config.SMSToken
	}
	body, err := json.Marshal(map[string]string{
		"deliveryId": delivery.ID.String(), "destination": secret.Destination, "code": secret.Code,
	})
	if err != nil {
		return "", false, "payload_invalid"
	}
	requestCtx, cancel := context.WithTimeout(ctx, s.config.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", false, "provider_request_invalid"
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", delivery.ID.String())
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return "", true, "provider_timeout"
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusTooManyRequests {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", true, "provider_rate_limited"
	}
	if response.StatusCode >= 500 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", true, "provider_unavailable"
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return "", false, "provider_rejected"
	}
	var result providerResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil || strings.TrimSpace(result.RequestID) == "" {
		return "", true, "provider_response_invalid"
	}
	return result.RequestID, false, ""
}

func deliveryBackoff(config DeliveryConfig, attempts int) time.Duration {
	delay := config.BaseBackoff
	for step := 1; step < attempts && delay < config.MaxBackoff; step++ {
		delay *= 2
	}
	if delay > config.MaxBackoff {
		return config.MaxBackoff
	}
	return delay
}

func (r *PostgresRepository) claimDeliveries(ctx context.Context, workerID string, batchSize int, lease time.Duration) ([]claimedDelivery, error) {
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
		return nil, oops.In("auth_delivery").Code("delivery.claim_failed").Wrap(err)
	}
	defer rows.Close()
	var result []claimedDelivery
	for rows.Next() {
		var delivery claimedDelivery
		if err := rows.Scan(&delivery.ID, &delivery.Channel, &delivery.Ciphertext, &delivery.ExpiresAt, &delivery.Attempts); err != nil {
			return nil, oops.In("auth_delivery").Code("delivery.scan_failed").Wrap(err)
		}
		result = append(result, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.In("auth_delivery").Code("delivery.rows_failed").Wrap(err)
	}
	return result, nil
}

func (r *PostgresRepository) deliver(ctx context.Context, deliveryID uuid.UUID, workerID, providerRequestID string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_verification_delivery_payloads
		SET delivery_status = 'delivered', provider_request_id = $3, delivered_at = now(),
			payload_ciphertext = NULL, payload_key_id = NULL, aad_hash = NULL,
			destroyed_at = now(), lease_owner = NULL, lease_until = NULL, last_error_code = NULL
		WHERE delivery_payload_id = $1 AND delivery_status = 'pending' AND lease_owner = $2
	`, deliveryID, workerID, providerRequestID)
	if err != nil {
		return oops.In("auth_delivery").Code("delivery.mark_delivered_failed").Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("auth_delivery").Code("delivery.lease_lost").New("verification delivery lease was lost")
	}
	return nil
}

func (r *PostgresRepository) retryDelivery(ctx context.Context, deliveryID uuid.UUID, workerID string, delay time.Duration, errorCode string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_verification_delivery_payloads
		SET next_attempt_at = now() + $3::interval, lease_owner = NULL, lease_until = NULL,
			last_error_code = $4
		WHERE delivery_payload_id = $1 AND delivery_status = 'pending' AND lease_owner = $2
	`, deliveryID, workerID, delay.String(), errorCode)
	if err != nil {
		return oops.In("auth_delivery").Code("delivery.retry_failed").Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("auth_delivery").Code("delivery.lease_lost").New("verification delivery lease was lost")
	}
	return nil
}

func (r *PostgresRepository) failDelivery(ctx context.Context, deliveryID uuid.UUID, workerID, errorCode string) error {
	result, err := r.pool.Exec(ctx, `
		UPDATE auth_verification_delivery_payloads
		SET delivery_status = 'failed', payload_ciphertext = NULL, payload_key_id = NULL,
			aad_hash = NULL, destroyed_at = now(), lease_owner = NULL, lease_until = NULL,
			last_error_code = $3
		WHERE delivery_payload_id = $1 AND delivery_status = 'pending' AND lease_owner = $2
	`, deliveryID, workerID, errorCode)
	if err != nil {
		return oops.In("auth_delivery").Code("delivery.mark_failed_failed").Wrap(err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("auth_delivery").Code("delivery.lease_lost").New("verification delivery lease was lost")
	}
	return nil
}
