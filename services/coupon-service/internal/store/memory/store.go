package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/Medikong/services/services/coupon-service/internal/model"
	"github.com/Medikong/services/services/coupon-service/internal/repository"
)

type Store struct {
	mu       sync.Mutex
	policies map[string]model.Policy
	coupons  map[string]model.Coupon
	next     int
}

func New() *Store {
	return &Store{policies: map[string]model.Policy{}, coupons: map[string]model.Coupon{}}
}

func (s *Store) UpsertPolicy(_ context.Context, input repository.PolicyInput) (model.Policy, error) {
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

func (s *Store) GetPolicy(_ context.Context, policyID string) (model.Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[policyID]
	if !ok {
		return model.Policy{}, repository.ErrPolicyNotFound
	}
	return policy, nil
}

func (s *Store) Issue(_ context.Context, policyID string, userID string, _ string) (model.IssueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[policyID]
	if !ok {
		return model.IssueResult{}, repository.ErrPolicyNotFound
	}
	if policy.Status != "ready" {
		return model.IssueResult{}, repository.ErrPolicyNotReady
	}
	key := policyID + ":" + userID
	if coupon, ok := s.coupons[key]; ok {
		return model.IssueResult{Result: "duplicate", Coupon: coupon}, nil
	}
	if policy.IssuedCount >= policy.TotalQuantity {
		return model.IssueResult{}, repository.ErrSoldOut
	}
	s.next++
	coupon := model.Coupon{CouponID: fmt.Sprintf("coupon-%d", s.next), PolicyID: policyID, DropID: policy.DropID, UserID: userID, Status: "issued"}
	policy.IssuedCount++
	s.policies[policyID] = policy
	s.coupons[key] = coupon
	return model.IssueResult{Result: "issued", Coupon: coupon}, nil
}

func (s *Store) ListByUser(_ context.Context, userID string) ([]model.Coupon, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var coupons []model.Coupon
	for _, coupon := range s.coupons {
		if coupon.UserID == userID {
			coupons = append(coupons, coupon)
		}
	}
	return coupons, nil
}
