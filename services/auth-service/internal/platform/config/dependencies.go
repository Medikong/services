package config

import (
	"os"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type BrokerConfig struct {
	Enabled        bool
	Brokers        []string
	Topic          string
	PublishTimeout time.Duration
}

func loadBroker() (BrokerConfig, error) {
	enabled, err := boolEnv("AUTH_OUTBOX_BROKER_ENABLED", false)
	if err != nil {
		return BrokerConfig{}, err
	}
	publishTimeout, err := durationEnv("AUTH_OUTBOX_PUBLISH_TIMEOUT", 3*time.Second)
	if err != nil {
		return BrokerConfig{}, err
	}
	config := BrokerConfig{
		Enabled:        enabled,
		Brokers:        stringListEnv("AUTH_OUTBOX_BROKERS", ""),
		Topic:          strings.TrimSpace(os.Getenv("AUTH_OUTBOX_TOPIC")),
		PublishTimeout: publishTimeout,
	}
	return config, config.Validate()
}

func (c BrokerConfig) Validate() error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.PublishTimeout, validation.Min(time.Nanosecond)),
	)
	if err != nil {
		return configErr.With("config", "broker").Wrap(err)
	}
	if c.Enabled && (len(c.Brokers) == 0 || c.Topic == "") {
		return configErr.With("config", "broker").New("AUTH_OUTBOX_BROKERS and AUTH_OUTBOX_TOPIC are required when broker publishing is enabled")
	}
	return nil
}

type DeliveryConfig struct {
	Enabled          bool
	EmailURL         string
	SMSURL           string
	EmailBearerToken string
	SMSBearerToken   string
	RequestTimeout   time.Duration
	PollInterval     time.Duration
	Lease            time.Duration
	BatchSize        int
	MaxAttempts      int
	BaseBackoff      time.Duration
	MaxBackoff       time.Duration
}

func loadDelivery() (DeliveryConfig, error) {
	enabled, err := boolEnv("AUTH_DELIVERY_ENABLED", false)
	if err != nil {
		return DeliveryConfig{}, err
	}
	requestTimeout, err := durationEnv("AUTH_PROVIDER_TIMEOUT", 3*time.Second)
	if err != nil {
		return DeliveryConfig{}, err
	}
	pollInterval, err := durationEnv("AUTH_DELIVERY_POLL_INTERVAL", 250*time.Millisecond)
	if err != nil {
		return DeliveryConfig{}, err
	}
	lease, err := durationEnv("AUTH_DELIVERY_LEASE", 10*time.Second)
	if err != nil {
		return DeliveryConfig{}, err
	}
	batchSize, err := intEnv("AUTH_DELIVERY_BATCH_SIZE", 20)
	if err != nil {
		return DeliveryConfig{}, err
	}
	maxAttempts, err := intEnv("AUTH_DELIVERY_MAX_ATTEMPTS", 10)
	if err != nil {
		return DeliveryConfig{}, err
	}
	baseBackoff, err := durationEnv("AUTH_DELIVERY_BASE_BACKOFF", time.Second)
	if err != nil {
		return DeliveryConfig{}, err
	}
	maxBackoff, err := durationEnv("AUTH_DELIVERY_MAX_BACKOFF", 30*time.Second)
	if err != nil {
		return DeliveryConfig{}, err
	}
	config := DeliveryConfig{
		Enabled:          enabled,
		EmailURL:         strings.TrimSpace(os.Getenv("AUTH_EMAIL_PROVIDER_URL")),
		SMSURL:           strings.TrimSpace(os.Getenv("AUTH_SMS_PROVIDER_URL")),
		EmailBearerToken: os.Getenv("AUTH_EMAIL_PROVIDER_BEARER_TOKEN"),
		SMSBearerToken:   os.Getenv("AUTH_SMS_PROVIDER_BEARER_TOKEN"),
		RequestTimeout:   requestTimeout,
		PollInterval:     pollInterval,
		Lease:            lease,
		BatchSize:        batchSize,
		MaxAttempts:      maxAttempts,
		BaseBackoff:      baseBackoff,
		MaxBackoff:       maxBackoff,
	}
	return config, config.Validate()
}

func (c DeliveryConfig) Validate() error {
	err := validation.ValidateStruct(&c,
		validation.Field(&c.RequestTimeout, validation.Min(time.Nanosecond)),
		validation.Field(&c.PollInterval, validation.Min(time.Nanosecond)),
		validation.Field(&c.Lease, validation.Min(2*c.RequestTimeout).Error("must be at least twice RequestTimeout")),
		validation.Field(&c.BatchSize, validation.Min(1)),
		validation.Field(&c.MaxAttempts, validation.Min(1)),
		validation.Field(&c.BaseBackoff, validation.Min(time.Nanosecond)),
		validation.Field(&c.MaxBackoff, validation.Min(c.BaseBackoff)),
	)
	if err != nil {
		return configErr.With("config", "delivery").Wrap(err)
	}
	if c.Enabled && (c.EmailURL == "" || c.SMSURL == "") {
		return configErr.With("config", "delivery").New("AUTH_EMAIL_PROVIDER_URL and AUTH_SMS_PROVIDER_URL are required when delivery is enabled")
	}
	return nil
}
