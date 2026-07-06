package coupon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store           Repository
	redis           redis.Cmdable
	redisKeyPrefix  string
	redisPendingTTL time.Duration
	redisIdemTTL    time.Duration
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

func WithRedis(client redis.Cmdable, pendingTTL time.Duration, idempotencyTTL time.Duration) Option {
	return func(s *Service) {
		s.redis = client
		s.redisKeyPrefix = "coupon"
		if pendingTTL <= 0 {
			pendingTTL = 30 * time.Second
		}
		if idempotencyTTL <= 0 {
			idempotencyTTL = 24 * time.Hour
		}
		s.redisPendingTTL = pendingTTL
		s.redisIdemTTL = idempotencyTTL
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
	if s.redis != nil {
		if err := s.prepareRedisPolicy(ctx, policy); err != nil {
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
	if s.redis == nil {
		return s.issueInStore(ctx, policyID, p.UserID, idemKey)
	}
	return s.issueWithRedis(ctx, policyID, p.UserID, idemKey)
}

func (s Service) issueWithRedis(ctx context.Context, policyID string, userID string, idemKey string) (IssueResult, error) {
	decision, err := runRedisAdmitScript(ctx, s.redis, s.redisKeyPrefix, policyID, userID, idemKey, s.redisPendingTTL)
	if err != nil {
		s.inc("coupon_redis_gate_total", "redis_unavailable")
		if s.gateFailureMode == "fail_closed" {
			return IssueResult{}, err
		}
		logger.Info(ctx, "coupon.redis_gate.unavailable", "policy_id", policyID, logger.Err(err))
		return s.issueInStore(ctx, policyID, userID, idemKey)
	}
	s.inc("coupon_redis_gate_total", decision.Result)

	switch decision.Result {
	case resultIssuedCandidate:
		result, err := s.issueInStore(ctx, policyID, userID, idemKey)
		if err != nil {
			if compensateErr := runRedisCompensateScript(ctx, s.redis, s.redisKeyPrefix, decision); compensateErr != nil {
				logger.Info(ctx, "coupon.redis_gate.compensate_failed", "policy_id", policyID, logger.Err(compensateErr))
			}
			return IssueResult{}, err
		}
		if err := runRedisCompleteScript(ctx, s.redis, s.redisKeyPrefix, decision, result, s.redisIdemTTL); err != nil {
			logger.Info(ctx, "coupon.redis_gate.complete_failed", "policy_id", policyID, logger.Err(err))
		}
		return result, nil
	case resultDuplicate:
		if decision.Coupon.CouponID != "" {
			return IssueResult{Result: "duplicate", Coupon: decision.Coupon}, nil
		}
		return s.issueInStore(ctx, policyID, userID, idemKey)
	case resultPending:
		return IssueResult{}, ErrIssuePending
	case resultSoldOut:
		return IssueResult{}, ErrSoldOut
	case resultNotReady:
		if s.gateFailureMode == "fail_closed" {
			return IssueResult{}, ErrPolicyNotReady
		}
		return s.issueInStoreAndRefreshRedisPolicy(ctx, policyID, userID, idemKey)
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
	ErrIssuePending  = errors.New("coupon issue pending")
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

func (s Service) prepareRedisPolicy(ctx context.Context, policy Policy) error {
	if policy.Status != "ready" {
		return s.redis.Del(ctx, redisRemainingKey(s.redisKeyPrefix, policy.PolicyID)).Err()
	}
	remaining := policy.TotalQuantity - policy.IssuedCount
	if remaining < 0 {
		remaining = 0
	}
	return s.redis.Set(ctx, redisRemainingKey(s.redisKeyPrefix, policy.PolicyID), remaining, 0).Err()
}

func (s Service) issueInStoreAndRefreshRedisPolicy(ctx context.Context, policyID string, userID string, idempotencyKey string) (IssueResult, error) {
	result, err := s.issueInStore(ctx, policyID, userID, idempotencyKey)
	if refreshErr := s.refreshRedisPolicy(ctx, policyID); refreshErr != nil && !errors.Is(refreshErr, ErrPolicyNotFound) {
		logger.Info(ctx, "coupon.redis_gate.refresh_failed", "policy_id", policyID, logger.Err(refreshErr))
	}
	return result, err
}

func (s Service) refreshRedisPolicy(ctx context.Context, policyID string) error {
	policy, err := s.store.GetPolicy(ctx, policyID)
	if err != nil {
		return err
	}
	return s.prepareRedisPolicy(ctx, policy)
}
