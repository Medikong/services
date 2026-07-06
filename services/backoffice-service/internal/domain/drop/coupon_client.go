package drop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-platform/telemetry"
)

type CouponClient interface {
	PreparePolicy(ctx context.Context, input PrepareDropInput) error
}

type HTTPCouponClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPCouponClient(baseURL string) HTTPCouponClient {
	return HTTPCouponClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (c HTTPCouponClient) PreparePolicy(ctx context.Context, input PrepareDropInput) error {
	payload := map[string]any{
		"policyId":      input.CouponPolicy.PolicyID,
		"dropId":        input.DropID,
		"name":          input.CouponPolicy.Name,
		"totalQuantity": input.CouponPolicy.TotalQuantity,
		"status":        "ready",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/internal/coupon-policies", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	telemetry.Inject(ctx, req.Header)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("coupon policy prepare failed: status=%d", resp.StatusCode)
	}
	return nil
}
