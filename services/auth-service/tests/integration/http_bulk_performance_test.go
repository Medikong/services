//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestHTTPDevelopmentBulkTokensTenThousandPerformance(t *testing.T) {
	if os.Getenv("RUN_AUTH_BULK_10000") != "1" {
		t.Skip("set RUN_AUTH_BULK_10000=1 to run the 10,000-token performance probe")
	}
	harness := newDevelopmentHTTPHarness(t)
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		harness.baseURL+"/api/v1/dev/auth/test-tokens/bulk",
		bytes.NewBufferString(`{"count":10000,"ttlSeconds":86400}`),
	)
	if err != nil {
		t.Fatalf("create bulk performance request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Dev-Access-Token", httpE2EDevelopmentToken)

	client := &http.Client{Timeout: 2 * time.Minute}
	startedAt := time.Now()
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("execute 10,000-token request after %s: %v", time.Since(startedAt), err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBytes, err := io.Copy(io.Discard, response.Body)
	if err != nil {
		t.Fatalf("read 10,000-token response: %v", err)
	}
	duration := time.Since(startedAt)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("10,000-token response status=%d duration=%s bytes=%d", response.StatusCode, duration, responseBytes)
	}
	var sessionCount int
	if err := harness.db.QueryRow(harness.ctx, `SELECT count(*) FROM auth_sessions`).Scan(&sessionCount); err != nil {
		t.Fatalf("count generated sessions: %v", err)
	}
	if sessionCount != 10000 {
		t.Fatalf("generated sessions=%d, want 10000", sessionCount)
	}
	t.Logf("10,000 tokens: duration=%s response_bytes=%d", duration, responseBytes)
}
