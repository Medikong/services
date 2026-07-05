package memory

import (
	"context"
	"sync"

	"github.com/Medikong/services/services/backoffice-service/internal/model"
)

type Store struct {
	mu     sync.Mutex
	checks map[string]model.Readiness
}

func New() *Store {
	return &Store{checks: map[string]model.Readiness{}}
}

func (s *Store) PrepareLocal(_ context.Context, input model.PrepareDropInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[input.DropID] = model.Readiness{
		DropID: input.DropID,
		Ready:  false,
		Checks: map[string]model.Check{
			"product":   {Ready: true},
			"drop":      {Ready: true},
			"inventory": {Ready: true},
			"coupon":    {Ready: false, Reason: "coupon policy not prepared"},
		},
	}
	return nil
}

func (s *Store) MarkCouponPrepared(_ context.Context, dropID string, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	readiness := s.checks[dropID]
	readiness.Checks["coupon"] = model.Check{Ready: true}
	readiness.Ready = true
	s.checks[dropID] = readiness
	return nil
}

func (s *Store) Readiness(_ context.Context, dropID string) (model.Readiness, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	readiness, ok := s.checks[dropID]
	if !ok {
		return model.Readiness{
			DropID: dropID,
			Ready:  false,
			Checks: map[string]model.Check{
				"product":   {Ready: false, Reason: "product not prepared"},
				"drop":      {Ready: false, Reason: "drop not prepared"},
				"inventory": {Ready: false, Reason: "inventory not prepared"},
				"coupon":    {Ready: false, Reason: "coupon policy not prepared"},
			},
		}, nil
	}
	return readiness, nil
}
