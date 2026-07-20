package app

import (
	"context"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/Medikong/services/packages/go-audit"
	"github.com/Medikong/services/packages/go-platform/logger"
	applicationchallenge "github.com/Medikong/services/services/auth-service/internal/application/challenge"
	applicationsessionprojection "github.com/Medikong/services/services/auth-service/internal/application/sessionprojection"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/cryptography"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/messaging/outbox"
	postgresinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/postgres"
	"github.com/Medikong/services/services/auth-service/internal/infrastructure/provider/verification"
	redisinfra "github.com/Medikong/services/services/auth-service/internal/infrastructure/redis"
	"github.com/Medikong/services/services/auth-service/internal/platform/config"
	"github.com/Medikong/services/services/auth-service/internal/platform/observability"
	"github.com/samber/oops"
)

type workerWiring struct {
	runAudit             func(context.Context) error
	runDomainOutbox      func(context.Context) error
	runDelivery          func(context.Context) error
	runSessionProjection func(context.Context) error
	kafkaPublisher       *outbox.KafkaPublisher
	cleanup              func(context.Context) (int64, error)
}

func wireWorker(ctx context.Context, cfg config.WorkerConfig, resources Resources, metrics *observability.Metrics, publisher outbox.Publisher) (workerWiring, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return workerWiring{}, oops.In("auth_worker").Code("worker.hostname_failed").Wrap(err)
	}
	auditWorker, err := audit.NewWorker(audit.WorkerConfig{
		Pool:           resources.DB,
		WorkerID:       hostname + ":" + uuid.NewString(),
		BatchSize:      cfg.Audit.BatchSize,
		PollInterval:   cfg.Audit.PollInterval,
		Lease:          cfg.Audit.Lease,
		PublishTimeout: cfg.Audit.PublishTimeout,
		MaxAttempts:    cfg.Audit.MaxAttempts,
		BaseBackoff:    cfg.Audit.BaseBackoff,
		MaxBackoff:     cfg.Audit.MaxBackoff,
		Publish: func(ctx context.Context, event audit.Event) error {
			return audit.Archive(ctx, resources.AuditSink, event)
		},
		OnAttempt: func(ctx context.Context, attempt audit.Attempt) {
			metrics.RecordAuditAttempt(attempt.Result)
			args := []any{"event_id", attempt.EventID, "attempt", attempt.Attempts, "result", attempt.Result}
			if attempt.Err != nil {
				args = append(args, logger.Err(attempt.Err))
			}
			switch attempt.Result {
			case "dead":
				logger.Error(ctx, "audit.outbox.attempted", args...)
			case "retry":
				logger.Warn(ctx, "audit.outbox.attempted", args...)
			default:
				logger.Info(ctx, "audit.outbox.attempted", args...)
			}
		},
	})
	if err != nil {
		return workerWiring{}, err
	}

	var kafkaPublisher *outbox.KafkaPublisher
	if publisher == nil && cfg.Broker.Enabled {
		kafkaPublisher, err = outbox.NewKafkaPublisher(
			ctx,
			cfg.Broker.Brokers,
			cfg.Broker.Topic,
			cfg.Broker.PublishTimeout,
		)
		if err != nil {
			return workerWiring{}, err
		}
		publisher = kafkaPublisher
	}
	closeKafkaOnError := func(err error) (workerWiring, error) {
		if kafkaPublisher != nil {
			kafkaPublisher.Close()
		}
		return workerWiring{}, err
	}

	var runDomainOutbox func(context.Context) error
	if publisher != nil {
		relay, relayErr := outbox.New(outbox.NewPostgresRepository(resources.DB), publisher, outbox.Config{
			WorkerID: hostname + ":auth-outbox:" + uuid.NewString(), BatchSize: cfg.Audit.BatchSize,
			PollInterval: cfg.Audit.PollInterval, Lease: cfg.Audit.Lease, MaxAttempts: cfg.Audit.MaxAttempts,
			BaseBackoff: cfg.Audit.BaseBackoff, MaxBackoff: cfg.Audit.MaxBackoff,
		})
		if relayErr != nil {
			return closeKafkaOnError(relayErr)
		}
		runDomainOutbox = relay.Run
	}

	var runDelivery func(context.Context) error
	if cfg.Delivery.Enabled {
		provider, providerErr := verification.New(verification.Config{
			EmailURL: cfg.Delivery.EmailURL, SMSURL: cfg.Delivery.SMSURL,
			EmailToken: cfg.Delivery.EmailBearerToken, SMSToken: cfg.Delivery.SMSBearerToken,
			RequestTimeout: cfg.Delivery.RequestTimeout,
		})
		if providerErr != nil {
			return closeKafkaOnError(providerErr)
		}
		delivery, deliveryErr := applicationchallenge.NewDeliveryService(
			postgresinfra.NewChallengeDeliveryRepository(resources.DB),
			cryptography.NewChallengePayloadOpener(cryptography.Keys{ReplayKey: []byte(cfg.Auth.ReplayEncryptionKey)}),
			provider,
			applicationchallenge.DeliveryConfig{
				WorkerID:       hostname + ":auth-delivery:" + uuid.NewString(),
				RequestTimeout: cfg.Delivery.RequestTimeout, PollInterval: cfg.Delivery.PollInterval,
				Lease: cfg.Delivery.Lease, BatchSize: cfg.Delivery.BatchSize, MaxAttempts: cfg.Delivery.MaxAttempts,
				BaseBackoff: cfg.Delivery.BaseBackoff, MaxBackoff: cfg.Delivery.MaxBackoff,
			},
		)
		if deliveryErr != nil {
			return closeKafkaOnError(deliveryErr)
		}
		runDelivery = delivery.Run
	}

	var runSessionProjection func(context.Context) error
	var sessionProjectionRepository *postgresinfra.SessionStatusProjectionRepository
	if resources.Redis != nil {
		sink, sinkErr := redisinfra.NewSessionProjection(
			postgresinfra.NewSessionRepository(resources.DB),
			resources.Redis,
			cfg.SessionStatus.Timeout,
			cfg.SessionStatus.DBFallbackTimeout,
			cfg.SessionStatus.CacheTTL,
			cfg.SessionStatus.TombstoneTTL,
		)
		if sinkErr != nil {
			return closeKafkaOnError(sinkErr)
		}
		sessionProjectionRepository = postgresinfra.NewSessionStatusProjectionRepository(resources.DB)
		relay, relayErr := applicationsessionprojection.New(
			sessionProjectionRepository,
			sink,
			applicationsessionprojection.Config{
				WorkerID:     hostname + ":session-projection:" + uuid.NewString(),
				BatchSize:    cfg.Audit.BatchSize,
				PollInterval: cfg.Audit.PollInterval,
				Lease:        cfg.Audit.Lease,
				ApplyTimeout: cfg.SessionStatus.Timeout,
				BaseBackoff:  cfg.Audit.BaseBackoff,
				MaxBackoff:   cfg.Audit.MaxBackoff,
				OnAttempt:    metrics.RecordSessionProjectionAttempt,
			},
		)
		if relayErr != nil {
			return closeKafkaOnError(relayErr)
		}
		runSessionProjection = relay.Run
	}

	return workerWiring{
		runAudit:             auditWorker.Run,
		runDomainOutbox:      runDomainOutbox,
		runDelivery:          runDelivery,
		runSessionProjection: runSessionProjection,
		kafkaPublisher:       kafkaPublisher,
		cleanup: func(ctx context.Context) (int64, error) {
			deleted, err := audit.DeleteDeliveredBefore(
				ctx,
				resources.DB,
				time.Now().UTC().Add(-cfg.Audit.Retention),
				cfg.Audit.CleanupLimit,
			)
			if err != nil || sessionProjectionRepository == nil {
				return deleted, err
			}
			projectionDeleted, err := sessionProjectionRepository.DeleteDeliveredBefore(
				ctx,
				time.Now().UTC().Add(-cfg.Audit.Retention),
				cfg.Audit.CleanupLimit,
			)
			return deleted + projectionDeleted, err
		},
	}, nil
}
