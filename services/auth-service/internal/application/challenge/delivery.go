package challenge

import (
	"context"
	"strings"
	"time"

	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type DeliveryConfig struct {
	WorkerID       string
	RequestTimeout time.Duration
	PollInterval   time.Duration
	Lease          time.Duration
	BatchSize      int
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
}

type ClaimedDelivery struct {
	ID         uuid.UUID
	Channel    domainchallenge.Channel
	Ciphertext []byte
	ExpiresAt  time.Time
	Attempts   int
}

type DeliverySecret struct {
	Code        string
	Destination string
}

type DeliveryRequest struct {
	ID          uuid.UUID
	Channel     domainchallenge.Channel
	Code        string
	Destination string
}

type SendResult struct {
	RequestID string
	Retry     bool
	Code      string
}

// DeliveryRepository is the worker's storage role. Lease and state updates
// stay in PostgreSQL while retry policy remains in this application service.
type DeliveryRepository interface {
	Claim(context.Context, string, int, time.Duration) ([]ClaimedDelivery, error)
	MarkDelivered(context.Context, uuid.UUID, string, string) error
	Retry(context.Context, uuid.UUID, string, time.Duration, string) error
	Fail(context.Context, uuid.UUID, string, string) error
}

type PayloadOpener interface {
	OpenDelivery([]byte) (DeliverySecret, error)
}

type Sender interface {
	Send(context.Context, DeliveryRequest) SendResult
}

type DeliveryService struct {
	repository DeliveryRepository
	opener     PayloadOpener
	sender     Sender
	config     DeliveryConfig
	now        func() time.Time
}

func NewDeliveryService(repository DeliveryRepository, opener PayloadOpener, sender Sender, config DeliveryConfig) (*DeliveryService, error) {
	if repository == nil || opener == nil || sender == nil || strings.TrimSpace(config.WorkerID) == "" ||
		config.RequestTimeout <= 0 || config.PollInterval <= 0 || config.Lease < 2*config.RequestTimeout ||
		config.BatchSize < 1 || config.MaxAttempts < 1 ||
		config.BaseBackoff <= 0 || config.MaxBackoff < config.BaseBackoff {
		return nil, oops.In("challenge_delivery").Code("delivery.invalid_config").New("invalid verification delivery configuration")
	}
	return &DeliveryService{
		repository: repository,
		opener:     opener,
		sender:     sender,
		config:     config,
		now:        func() time.Time { return time.Now().UTC() },
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
	deliveries, err := s.repository.Claim(ctx, s.config.WorkerID, s.config.BatchSize, s.config.Lease)
	if err != nil {
		return err
	}
	for _, delivery := range deliveries {
		secret, err := s.opener.OpenDelivery(delivery.Ciphertext)
		if err != nil || len(secret.Code) != 6 || strings.TrimSpace(secret.Destination) == "" {
			if err := s.repository.Fail(ctx, delivery.ID, s.config.WorkerID, "payload_invalid"); err != nil {
				return err
			}
			continue
		}
		result := s.sender.Send(ctx, DeliveryRequest{
			ID: delivery.ID, Channel: delivery.Channel, Code: secret.Code, Destination: secret.Destination,
		})
		secret = DeliverySecret{}
		if result.Code == "" {
			if err := s.repository.MarkDelivered(ctx, delivery.ID, s.config.WorkerID, result.RequestID); err != nil {
				return err
			}
			continue
		}
		if !result.Retry || delivery.Attempts >= s.config.MaxAttempts || !s.now().Before(delivery.ExpiresAt) {
			if err := s.repository.Fail(ctx, delivery.ID, s.config.WorkerID, result.Code); err != nil {
				return err
			}
			continue
		}
		if err := s.repository.Retry(ctx, delivery.ID, s.config.WorkerID, deliveryBackoff(s.config, delivery.Attempts), result.Code); err != nil {
			return err
		}
	}
	return nil
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
