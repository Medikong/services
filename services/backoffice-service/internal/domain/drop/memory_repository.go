package drop

import (
	"context"
	"sync"
)

type MemoryRepository struct {
	mu     sync.Mutex
	checks map[string]Readiness
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{checks: map[string]Readiness{}}
}

func (s *MemoryRepository) PrepareLocal(_ context.Context, input PrepareDropInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[input.DropID] = Readiness{
		DropID: input.DropID,
		Ready:  false,
		Checks: map[string]Check{
			"product":   {Ready: true},
			"drop":      {Ready: true},
			"inventory": {Ready: true},
			"coupon":    {Ready: false, Reason: "coupon policy not prepared"},
		},
	}
	return nil
}

func (s *MemoryRepository) MarkCouponPrepared(_ context.Context, dropID string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	readiness := s.checks[dropID]
	readiness.Checks["coupon"] = Check{Ready: true}
	readiness.Ready = true
	s.checks[dropID] = readiness
	return nil
}

func (s *MemoryRepository) Readiness(_ context.Context, dropID string) (Readiness, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	readiness, ok := s.checks[dropID]
	if !ok {
		return Readiness{
			DropID: dropID,
			Ready:  false,
			Checks: map[string]Check{
				"product":   {Ready: false, Reason: "product not prepared"},
				"drop":      {Ready: false, Reason: "drop not prepared"},
				"inventory": {Ready: false, Reason: "inventory not prepared"},
				"coupon":    {Ready: false, Reason: "coupon policy not prepared"},
			},
		}, nil
	}
	return readiness, nil
}
