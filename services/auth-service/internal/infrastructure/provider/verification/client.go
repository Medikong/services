package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	applicationchallenge "github.com/Medikong/services/services/auth-service/internal/application/challenge"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/samber/oops"
)

type Config struct {
	EmailURL       string
	SMSURL         string
	EmailToken     string
	SMSToken       string
	RequestTimeout time.Duration
}

type Client struct {
	httpClient *http.Client
	config     Config
}

func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.EmailURL) == "" || strings.TrimSpace(config.SMSURL) == "" || config.RequestTimeout <= 0 {
		return nil, oops.In("verification_provider").Code("provider.invalid_config").New("invalid verification provider configuration")
	}
	return &Client{httpClient: &http.Client{Timeout: config.RequestTimeout}, config: config}, nil
}

func (c *Client) Send(ctx context.Context, delivery applicationchallenge.DeliveryRequest) applicationchallenge.SendResult {
	endpoint, token := c.config.EmailURL, c.config.EmailToken
	if delivery.Channel == domainchallenge.ChannelSMSCode {
		endpoint, token = c.config.SMSURL, c.config.SMSToken
	}
	body, err := json.Marshal(map[string]string{
		"deliveryId":  delivery.ID.String(),
		"destination": delivery.Destination,
		"code":        delivery.Code,
	})
	if err != nil {
		return applicationchallenge.SendResult{Code: "payload_invalid"}
	}
	requestCtx, cancel := context.WithTimeout(ctx, c.config.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return applicationchallenge.SendResult{Code: "provider_request_invalid"}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", delivery.ID.String())
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return applicationchallenge.SendResult{Retry: true, Code: "provider_timeout"}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusTooManyRequests {
		discard(response.Body)
		return applicationchallenge.SendResult{Retry: true, Code: "provider_rate_limited"}
	}
	if response.StatusCode >= http.StatusInternalServerError {
		discard(response.Body)
		return applicationchallenge.SendResult{Retry: true, Code: "provider_unavailable"}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		discard(response.Body)
		return applicationchallenge.SendResult{Code: "provider_rejected"}
	}
	var result struct {
		RequestID string `json:"requestId"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil || strings.TrimSpace(result.RequestID) == "" {
		return applicationchallenge.SendResult{Retry: true, Code: "provider_response_invalid"}
	}
	return applicationchallenge.SendResult{RequestID: result.RequestID}
}

func discard(body io.Reader) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 4096))
}

var _ applicationchallenge.Sender = (*Client)(nil)
