package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/services/coupon-service/internal/gate"
	"github.com/Medikong/services/services/coupon-service/internal/model"
	"github.com/Medikong/services/services/coupon-service/internal/repository"
)

type Store = repository.Store

type Service struct {
	store           repository.Store
	gate            gate.Gate
	gateFailureMode string
	metrics         *metrics.Registry
}

type Option func(*Service)

func New(store repository.Store, options ...Option) Service {
	s := Service{store: store, gateFailureMode: "db_fallback"}
	for _, option := range options {
		option(&s)
	}
	return s
}

func WithIssueGate(issueGate gate.Gate) Option {
	return func(s *Service) {
		s.gate = issueGate
	}
}

func WithGateFailureMode(mode string) Option {
	return func(s *Service) {
		if mode != "" {
			s.gateFailureMode = mode
		}
	}
}

func WithMetrics(registry *metrics.Registry) Option {
	return func(s *Service) {
		s.metrics = registry
	}
}

type PreparePolicyInput struct {
	PolicyID      string `json:"policyId"`
	DropID        string `json:"dropId"`
	Name          string `json:"name"`
	TotalQuantity int    `json:"totalQuantity"`
	Status        string `json:"status"`
}

type IssueInput struct {
	PolicyID string `json:"policyId"`
}

func (s Service) PreparePolicy(ctx context.Context, input PreparePolicyInput) (model.Policy, error) {
	if strings.TrimSpace(input.PolicyID) == "" || strings.TrimSpace(input.DropID) == "" || input.TotalQuantity <= 0 {
		return model.Policy{}, ErrInvalidPolicy
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "ready"
	}
	policy, err := s.store.UpsertPolicy(ctx, repository.PolicyInput{
		PolicyID:      strings.TrimSpace(input.PolicyID),
		DropID:        strings.TrimSpace(input.DropID),
		Name:          strings.TrimSpace(input.Name),
		TotalQuantity: input.TotalQuantity,
		Status:        status,
	})
	if err != nil {
		return model.Policy{}, err
	}
	if s.gate != nil {
		if err := s.gate.PreparePolicy(ctx, policy); err != nil {
			s.inc("coupon_redis_gate_total", "prepare_failed")
			if s.gateFailureMode == "fail_closed" {
				return model.Policy{}, err
			}
			logger.Info(ctx, "coupon.redis_gate.prepare_failed", "policy_id", policy.PolicyID, logger.Err(err))
		}
	}
	return policy, nil
}

func (s Service) GetPolicy(ctx context.Context, policyID string) (model.Policy, error) {
	return s.store.GetPolicy(ctx, strings.TrimSpace(policyID))
}

func (s Service) Issue(ctx context.Context, p principal.Principal, input IssueInput, idempotencyKey string) (model.IssueResult, error) {
	if p.Type != principal.TypeUser || strings.TrimSpace(p.UserID) == "" {
		return model.IssueResult{}, ErrUnauthorized
	}
	if strings.TrimSpace(input.PolicyID) == "" {
		return model.IssueResult{}, ErrInvalidIssue
	}
	policyID := strings.TrimSpace(input.PolicyID)
	idemKey := strings.TrimSpace(idempotencyKey)
	if s.gate == nil {
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	}

	decision, err := s.gate.Admit(ctx, gate.IssueRequest{PolicyID: policyID, UserID: p.UserID, IdempotencyKey: idemKey})
	if err != nil {
		s.inc("coupon_redis_gate_total", "redis_unavailable")
		if s.gateFailureMode == "fail_closed" {
			return model.IssueResult{}, err
		}
		logger.Info(ctx, "coupon.redis_gate.unavailable", "policy_id", policyID, logger.Err(err))
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	}
	s.inc("coupon_redis_gate_total", decision.Result)

	switch decision.Result {
	case gate.ResultIssuedCandidate:
		result, err := s.issueInStore(ctx, policyID, p.UserID, idemKey)
		if err != nil {
			if compensateErr := s.gate.Compensate(ctx, decision); compensateErr != nil {
				logger.Info(ctx, "coupon.redis_gate.compensate_failed", "policy_id", policyID, logger.Err(compensateErr))
			}
			return model.IssueResult{}, err
		}
		if err := s.gate.Complete(ctx, decision, result); err != nil {
			logger.Info(ctx, "coupon.redis_gate.complete_failed", "policy_id", policyID, logger.Err(err))
		}
		return result, nil
	case gate.ResultDuplicate:
		if decision.Coupon.CouponID != "" {
			return model.IssueResult{Result: "duplicate", Coupon: decision.Coupon}, nil
		}
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	case gate.ResultSoldOut:
		return model.IssueResult{}, repository.ErrSoldOut
	case gate.ResultNotReady:
		if s.gateFailureMode == "fail_closed" {
			return model.IssueResult{}, repository.ErrPolicyNotReady
		}
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	default:
		return model.IssueResult{}, errors.New("unknown redis gate result")
	}
}

func (s Service) ListMine(ctx context.Context, p principal.Principal) ([]model.Coupon, error) {
	if p.Type != principal.TypeUser || strings.TrimSpace(p.UserID) == "" {
		return nil, ErrUnauthorized
	}
	return s.store.ListByUser(ctx, p.UserID)
}

var (
	ErrInvalidPolicy = errors.New("invalid coupon policy")
	ErrInvalidIssue  = errors.New("invalid coupon issue")
	ErrUnauthorized  = errors.New("unauthorized")
)

func (s Service) issueInStore(ctx context.Context, policyID string, userID string, idempotencyKey string) (model.IssueResult, error) {
	result, err := s.store.Issue(ctx, policyID, userID, idempotencyKey)
	if err != nil {
		s.inc("coupon_db_finalize_total", couponFinalizeOutcome(err))
		return model.IssueResult{}, err
	}
	s.inc("coupon_db_finalize_total", result.Result)
	return result, nil
}

func (s Service) inc(name string, result string) {
	if s.metrics == nil {
		return
	}
	s.metrics.Inc(name, map[string]string{"service": "coupon-service", "result": result})
}

func couponFinalizeOutcome(err error) string {
	switch {
	case errors.Is(err, repository.ErrSoldOut):
		return "sold_out"
	case errors.Is(err, repository.ErrPolicyNotReady):
		return "not_ready"
	case errors.Is(err, repository.ErrPolicyNotFound):
		return "not_found"
	default:
		return "failed"
	}
}
