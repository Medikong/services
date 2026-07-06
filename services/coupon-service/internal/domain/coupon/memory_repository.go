package coupon

import (
	"context"
	"fmt"
	"sync"
)

type MemoryRepository struct {
	mu       sync.Mutex
	policies map[string]Policy
	coupons  map[string]Coupon
	next     int
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{policies: map[string]Policy{}, coupons: map[string]Coupon{}}
}

func (s *MemoryRepository) UpsertPolicy(_ context.Context, input PolicyInput) (Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy := s.policies[input.PolicyID]
	policy.PolicyID = input.PolicyID
	policy.DropID = input.DropID
	policy.Name = input.Name
	policy.TotalQuantity = input.TotalQuantity
	policy.Status = input.Status
	s.policies[input.PolicyID] = policy
	return policy, nil
}

func (s *MemoryRepository) GetPolicy(_ context.Context, policyID string) (Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[policyID]
	if !ok {
		return Policy{}, ErrPolicyNotFound
	}
	return policy, nil
}

func (s *MemoryRepository) Issue(_ context.Context, policyID string, userID string, _ string) (IssueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[policyID]
	if !ok {
		return IssueResult{}, ErrPolicyNotFound
	}
	if policy.Status != "ready" {
		return IssueResult{}, ErrPolicyNotReady
	}
	key := policyID + ":" + userID
	if coupon, ok := s.coupons[key]; ok {
		return IssueResult{Result: "duplicate", Coupon: coupon}, nil
	}
	if policy.IssuedCount >= policy.TotalQuantity {
		return IssueResult{}, ErrSoldOut
	}
	s.next++
	coupon := Coupon{CouponID: fmt.Sprintf("coupon-%d", s.next), PolicyID: policyID, DropID: policy.DropID, UserID: userID, Status: "issued"}
	policy.IssuedCount++
	s.policies[policyID] = policy
	s.coupons[key] = coupon
	return IssueResult{Result: "issued", Coupon: coupon}, nil
}

func (s *MemoryRepository) ListByUser(_ context.Context, userID string) ([]Coupon, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var coupons []Coupon
	for _, coupon := range s.coupons {
		if coupon.UserID == userID {
			coupons = append(coupons, coupon)
		}
	}
	return coupons, nil
}
