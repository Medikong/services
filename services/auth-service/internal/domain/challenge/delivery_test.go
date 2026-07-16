package challenge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
)

func TestDeliverySendClassifiesProviderResponses(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantRetry  bool
		wantCode   string
		wantResult bool
	}{
		{name: "accepted", status: http.StatusAccepted, body: `{"requestId":"provider-request"}`, wantResult: true},
		{name: "rate limited", status: http.StatusTooManyRequests, wantRetry: true, wantCode: "provider_rate_limited"},
		{name: "server failure", status: http.StatusBadGateway, wantRetry: true, wantCode: "provider_unavailable"},
		{name: "rejected", status: http.StatusBadRequest, wantCode: "provider_rejected"},
		{name: "invalid success", status: http.StatusOK, body: `{}`, wantRetry: true, wantCode: "provider_response_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Idempotency-Key"); got == "" {
					t.Fatal("Idempotency-Key is missing")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			service := testDeliveryService(t, server.URL)
			requestID, retry, code := service.send(context.Background(), claimedDelivery{
				ID: uuid.New(), Channel: ChannelEmailCode,
			}, deliverySecret{Destination: "masked@example.test", Code: "123456"})
			if retry != test.wantRetry || code != test.wantCode || (requestID != "") != test.wantResult {
				t.Fatalf("send() = request=%t retry=%t code=%q", requestID != "", retry, code)
			}
		})
	}
}

func TestDeliverySendTimesOutAndBackoffIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()
	service := testDeliveryService(t, server.URL)
	service.config.RequestTimeout = 10 * time.Millisecond
	service.client.Timeout = service.config.RequestTimeout

	_, retry, code := service.send(context.Background(), claimedDelivery{
		ID: uuid.New(), Channel: ChannelSMSCode,
	}, deliverySecret{Destination: "+8210******78", Code: "123456"})
	if !retry || code != "provider_timeout" {
		t.Fatalf("send() retry=%t code=%q", retry, code)
	}
	if got := deliveryBackoff(service.config, 20); got != service.config.MaxBackoff {
		t.Fatalf("deliveryBackoff() = %s, want %s", got, service.config.MaxBackoff)
	}
}

func testDeliveryService(t *testing.T, endpoint string) *DeliveryService {
	t.Helper()
	service, err := NewDeliveryService(&PostgresRepository{}, security.Keys{
		CredentialHMAC: []byte("unit-test-credential-hmac-key-0001"),
		ReplayKey:      []byte("unit-test-replay-encryption-key"),
	}, DeliveryConfig{
		WorkerID: "unit-test", EmailURL: endpoint, SMSURL: endpoint,
		RequestTimeout: time.Second, PollInterval: time.Second, Lease: 2 * time.Second,
		BatchSize: 1, MaxAttempts: 3, BaseBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
