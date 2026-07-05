package service

import (
	"context"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/services/backoffice-service/internal/model"
	"github.com/Medikong/services/services/backoffice-service/internal/store/memory"
)

type fakeCouponClient struct{}

func (fakeCouponClient) PreparePolicy(context.Context, model.PrepareDropInput) error {
	return nil
}

func TestPrepareDropRequiresOperatorAndMarksReadiness(t *testing.T) {
	svc := New(memory.New(), fakeCouponClient{})
	ctx := context.Background()
	input := model.PrepareDropInput{
		ProductID:     "product-1",
		ProductName:   "Drop Hoodie",
		DropID:        "drop-1",
		SaleStartsAt:  "2026-07-05T10:00:00Z",
		StockQuantity: 10,
		CouponPolicy:  model.CouponPolicyInput{PolicyID: "policy-1", Name: "Launch", TotalQuantity: 5},
	}
	readiness, err := svc.PrepareDrop(ctx, principal.Principal{Type: principal.TypeUser, UserID: "op-1", Roles: []string{"operator"}}, input)
	if err != nil {
		t.Fatalf("PrepareDrop() error = %v", err)
	}
	if !readiness.Ready {
		t.Fatalf("readiness = %+v", readiness)
	}
}
