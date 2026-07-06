package drop

import (
	"context"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
)

type fakeCouponClient struct{}

func (fakeCouponClient) PreparePolicy(context.Context, PrepareDropInput) error {
	return nil
}

func TestPrepareDropRequiresOperatorAndMarksReadiness(t *testing.T) {
	svc := NewService(NewMemoryRepository(), fakeCouponClient{})
	ctx := context.Background()
	input := PrepareDropInput{
		ProductID:     "product-1",
		ProductName:   "Drop Hoodie",
		DropID:        "drop-1",
		SaleStartsAt:  "2026-07-05T10:00:00Z",
		StockQuantity: 10,
		CouponPolicy:  CouponPolicyInput{PolicyID: "policy-1", Name: "Launch", TotalQuantity: 5},
	}
	readiness, err := svc.PrepareDrop(ctx, principal.Principal{Type: principal.TypeUser, UserID: "op-1", Roles: []string{"operator"}}, input)
	if err != nil {
		t.Fatalf("PrepareDrop() error = %v", err)
	}
	if !readiness.Ready {
		t.Fatalf("readiness = %+v", readiness)
	}
}
