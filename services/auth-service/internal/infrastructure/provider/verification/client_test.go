package verification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	applicationchallenge "github.com/Medikong/services/services/auth-service/internal/application/challenge"
	domainchallenge "github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/google/uuid"
)

func TestClientClassifiesProviderResponses(t *testing.T) {
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
				if r.Header.Get("Idempotency-Key") == "" {
					t.Fatal("Idempotency-Key is missing")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client := testClient(t, server.URL, time.Second)
			result := client.Send(context.Background(), applicationchallenge.DeliveryRequest{
				ID: uuid.New(), Channel: domainchallenge.ChannelEmailCode,
				Destination: "masked-destination", Code: "******",
			})
			if result.Retry != test.wantRetry || result.Code != test.wantCode || (result.RequestID != "") != test.wantResult {
				t.Fatalf("Send() = request=%t retry=%t code=%q", result.RequestID != "", result.Retry, result.Code)
			}
		})
	}
}

func TestClientClassifiesTimeoutAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()
	client := testClient(t, server.URL, 10*time.Millisecond)

	result := client.Send(context.Background(), applicationchallenge.DeliveryRequest{
		ID: uuid.New(), Channel: domainchallenge.ChannelSMSCode,
		Destination: "masked-destination", Code: "******",
	})
	if !result.Retry || result.Code != "provider_timeout" {
		t.Fatalf("Send() retry=%t code=%q", result.Retry, result.Code)
	}
}

func testClient(t *testing.T, endpoint string, timeout time.Duration) *Client {
	t.Helper()
	client, err := New(Config{EmailURL: endpoint, SMSURL: endpoint, RequestTimeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
