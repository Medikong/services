package coupon

import (
	"context"
	"errors"
	"testing"

	"github.com/Medikong/services/packages/go-authz/principal"
)

func TestIssueDuplicateAndSoldOut(t *testing.T) {
	svc := NewService(NewMemoryRepository())
	ctx := context.Background()
	if _, err := svc.PreparePolicy(ctx, PreparePolicyInput{PolicyID: "policy-1", DropID: "drop-1", Name: "Launch", TotalQuantity: 1}); err != nil {
		t.Fatalf("PreparePolicy() error = %v", err)
	}
	user := principal.Principal{Type: principal.TypeUser, UserID: "user-1"}
	first, err := svc.Issue(ctx, user, IssueInput{PolicyID: "policy-1"}, "idem-1")
	if err != nil {
		t.Fatalf("Issue() first error = %v", err)
	}
	if first.Result != "issued" {
		t.Fatalf("first result = %q", first.Result)
	}
	duplicate, err := svc.Issue(ctx, user, IssueInput{PolicyID: "policy-1"}, "idem-1")
	if err != nil {
		t.Fatalf("Issue() duplicate error = %v", err)
	}
	if duplicate.Result != "duplicate" {
		t.Fatalf("duplicate result = %q", duplicate.Result)
	}
	_, err = svc.Issue(ctx, principal.Principal{Type: principal.TypeUser, UserID: "user-2"}, IssueInput{PolicyID: "policy-1"}, "idem-2")
	if err == nil {
		t.Fatalf("Issue() sold out error = nil")
	}
}

func TestIssueUsesGateSoldOutBeforeStore(t *testing.T) {
	store := NewMemoryRepository()
	gate := &fakeGate{admit: Decision{Result: ResultSoldOut, PolicyID: "policy-1", UserID: "user-1"}}
	svc := NewService(store, WithIssueGate(gate))

	_, err := svc.Issue(context.Background(), principal.Principal{Type: principal.TypeUser, UserID: "user-1"}, IssueInput{PolicyID: "policy-1"}, "idem-1")
	if !errors.Is(err, ErrSoldOut) {
		t.Fatalf("Issue() error = %v, want sold out", err)
	}
	if gate.compensated {
		t.Fatalf("Compensate() called for sold_out decision")
	}
}

func TestIssueCompensatesGateCandidateWhenStoreFinalizeFails(t *testing.T) {
	gate := &fakeGate{admit: Decision{Result: ResultIssuedCandidate, PolicyID: "policy-1", UserID: "user-1"}}
	svc := NewService(NewMemoryRepository(), WithIssueGate(gate))

	_, err := svc.Issue(context.Background(), principal.Principal{Type: principal.TypeUser, UserID: "user-1"}, IssueInput{PolicyID: "policy-1"}, "idem-1")
	if !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("Issue() error = %v, want policy not found", err)
	}
	if !gate.compensated {
		t.Fatalf("Compensate() was not called")
	}
	if gate.completed {
		t.Fatalf("Complete() called after failed finalize")
	}
}

func TestIssueReturnsRedisDuplicateWithoutStoreLockPath(t *testing.T) {
	coupon := Coupon{CouponID: "coupon-redis-1", PolicyID: "policy-1", DropID: "drop-1", UserID: "user-1", Status: "issued"}
	gate := &fakeGate{admit: Decision{Result: ResultDuplicate, PolicyID: "policy-1", UserID: "user-1", Coupon: coupon}}
	svc := NewService(NewMemoryRepository(), WithIssueGate(gate))

	result, err := svc.Issue(context.Background(), principal.Principal{Type: principal.TypeUser, UserID: "user-1"}, IssueInput{PolicyID: "policy-1"}, "idem-1")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if result.Result != "duplicate" || result.Coupon.CouponID != coupon.CouponID {
		t.Fatalf("Issue() = %#v, want redis duplicate coupon", result)
	}
}

type fakeGate struct {
	admit       Decision
	err         error
	completed   bool
	compensated bool
}

func (g *fakeGate) PreparePolicy(context.Context, Policy) error {
	return nil
}

func (g *fakeGate) Admit(context.Context, IssueRequest) (Decision, error) {
	if g.err != nil {
		return Decision{}, g.err
	}
	return g.admit, nil
}

func (g *fakeGate) Complete(context.Context, Decision, IssueResult) error {
	g.completed = true
	return nil
}

func (g *fakeGate) Compensate(context.Context, Decision) error {
	g.compensated = true
	return nil
}
