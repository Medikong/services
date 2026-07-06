package coupon

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/metrics"
)

type Service struct {
	store           Repository
	gate            Gate
	gateFailureMode string
	metrics         *metrics.Registry
}

type Option func(*Service)

func NewService(store Repository, options ...Option) Service {
	s := Service{store: store, gateFailureMode: "db_fallback"}
	for _, option := range options {
		option(&s)
	}
	return s
}

func WithIssueGate(issueGate Gate) Option {
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

func (s Service) PreparePolicy(ctx context.Context, input PreparePolicyInput) (Policy, error) {
	if strings.TrimSpace(input.PolicyID) == "" || strings.TrimSpace(input.DropID) == "" || input.TotalQuantity <= 0 {
		return Policy{}, ErrInvalidPolicy
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "ready"
	}
	policy, err := s.store.UpsertPolicy(ctx, PolicyInput{
		PolicyID:      strings.TrimSpace(input.PolicyID),
		DropID:        strings.TrimSpace(input.DropID),
		Name:          strings.TrimSpace(input.Name),
		TotalQuantity: input.TotalQuantity,
		Status:        status,
	})
	if err != nil {
		return Policy{}, err
	}
	if s.gate != nil {
		if err := s.gate.PreparePolicy(ctx, policy); err != nil {
			s.inc("coupon_redis_gate_total", "prepare_failed")
			if s.gateFailureMode == "fail_closed" {
				return Policy{}, err
			}
			logger.Info(ctx, "coupon.redis_gate.prepare_failed", "policy_id", policy.PolicyID, logger.Err(err))
		}
	}
	return policy, nil
}

func (s Service) GetPolicy(ctx context.Context, policyID string) (Policy, error) {
	return s.store.GetPolicy(ctx, strings.TrimSpace(policyID))
}

func (s Service) Issue(ctx context.Context, p principal.Principal, input IssueInput, idempotencyKey string) (IssueResult, error) {
	if p.Type != principal.TypeUser || strings.TrimSpace(p.UserID) == "" {
		return IssueResult{}, ErrUnauthorized
	}
	if strings.TrimSpace(input.PolicyID) == "" {
		return IssueResult{}, ErrInvalidIssue
	}
	policyID := strings.TrimSpace(input.PolicyID)
	idemKey := strings.TrimSpace(idempotencyKey)
	if s.gate == nil {
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	}

	decision, err := s.gate.Admit(ctx, IssueRequest{PolicyID: policyID, UserID: p.UserID, IdempotencyKey: idemKey})
	if err != nil {
		s.inc("coupon_redis_gate_total", "redis_unavailable")
		if s.gateFailureMode == "fail_closed" {
			return IssueResult{}, err
		}
		logger.Info(ctx, "coupon.redis_gate.unavailable", "policy_id", policyID, logger.Err(err))
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	}
	s.inc("coupon_redis_gate_total", decision.Result)

	switch decision.Result {
	case ResultIssuedCandidate:
		result, err := s.issueInStore(ctx, policyID, p.UserID, idemKey)
		if err != nil {
			if compensateErr := s.gate.Compensate(ctx, decision); compensateErr != nil {
				logger.Info(ctx, "coupon.redis_gate.compensate_failed", "policy_id", policyID, logger.Err(compensateErr))
			}
			return IssueResult{}, err
		}
		if err := s.gate.Complete(ctx, decision, result); err != nil {
			logger.Info(ctx, "coupon.redis_gate.complete_failed", "policy_id", policyID, logger.Err(err))
		}
		return result, nil
	case ResultDuplicate:
		if decision.Coupon.CouponID != "" {
			return IssueResult{Result: "duplicate", Coupon: decision.Coupon}, nil
		}
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	case ResultSoldOut:
		return IssueResult{}, ErrSoldOut
	case ResultNotReady:
		if s.gateFailureMode == "fail_closed" {
			return IssueResult{}, ErrPolicyNotReady
		}
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	default:
		return IssueResult{}, errors.New("unknown redis gate result")
	}
}

func (s Service) ListMine(ctx context.Context, p principal.Principal) ([]Coupon, error) {
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

func (s Service) issueInStore(ctx context.Context, policyID string, userID string, idempotencyKey string) (IssueResult, error) {
	result, err := s.store.Issue(ctx, policyID, userID, idempotencyKey)
	if err != nil {
		s.inc("coupon_db_finalize_total", couponFinalizeOutcome(err))
		return IssueResult{}, err
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
	case errors.Is(err, ErrSoldOut):
		return "sold_out"
	case errors.Is(err, ErrPolicyNotReady):
		return "not_ready"
	case errors.Is(err, ErrPolicyNotFound):
		return "not_found"
	default:
		return "failed"
	}
}
