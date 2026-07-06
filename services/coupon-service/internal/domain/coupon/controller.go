package coupon

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/metrics"
)

const controllerServiceName = "coupon-service"

type Controller struct {
	service Service
	metrics *metrics.Registry
}

func NewController(service Service, registry *metrics.Registry) Controller {
	return Controller{service: service, metrics: registry}
}

func (c Controller) RegisterRoutes(r chi.Router) {
	r.Post("/v1/internal/coupon-policies", c.PreparePolicy)
	r.Get("/v1/internal/coupon-policies/{policyId}", c.GetPolicy)
	r.Route("/v1/coupons", func(r chi.Router) {
		r.Post("/issue", c.Issue)
		r.Get("/me", c.ListMine)
	})
}

func (c Controller) PreparePolicy(w http.ResponseWriter, r *http.Request) {
	var input PreparePolicyInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	policy, err := c.service.PreparePolicy(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, policy)
}

func (c Controller) GetPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := c.service.GetPolicy(r.Context(), chi.URLParam(r, "policyId"))
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, policy)
}

func (c Controller) Issue(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		c.incIssue("unauthorized")
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	var input IssueInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		c.incIssue("invalid_request")
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := c.service.Issue(r.Context(), p, input, r.Header.Get(headers.IdempotencyKey))
	if err != nil {
		outcome := controllerOutcome(err)
		c.incIssue(outcome)
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	c.incIssue(result.Result)
	logger.Info(r.Context(), "coupon.issue.completed", "result", result.Result, "policy_id", result.Coupon.PolicyID)
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (c Controller) ListMine(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	coupons, err := c.service.ListMine(r.Context(), p)
	if err != nil {
		httpapi.WriteError(w, r, mapControllerError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"coupons": coupons})
}

func (c Controller) incIssue(result string) {
	if c.metrics == nil {
		return
	}
	c.metrics.Inc("coupon_issue_total", map[string]string{"service": controllerServiceName, "result": result})
}

func controllerOutcome(err error) string {
	switch {
	case errors.Is(err, ErrSoldOut):
		return "sold_out"
	case errors.Is(err, ErrIssuePending):
		return "pending"
	case errors.Is(err, ErrPolicyNotReady):
		return "not_ready"
	case errors.Is(err, ErrPolicyNotFound):
		return "not_found"
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	default:
		return "failed"
	}
}

func mapControllerError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidPolicy):
		return httpapi.BadRequest("coupon.invalid_policy", "쿠폰 정책 값이 올바르지 않습니다.")
	case errors.Is(err, ErrInvalidIssue):
		return httpapi.BadRequest("coupon.invalid_issue", "쿠폰 발급 요청 값이 올바르지 않습니다.")
	case errors.Is(err, ErrUnauthorized):
		return httpapi.Unauthorized("auth.unauthorized", "인증이 필요합니다.")
	case errors.Is(err, ErrPolicyNotFound):
		return httpapi.Unprocessable("coupon.policy_not_found", "쿠폰 정책을 찾을 수 없습니다.")
	case errors.Is(err, ErrPolicyNotReady):
		return httpapi.Unprocessable("coupon.policy_not_ready", "쿠폰 정책이 아직 준비되지 않았습니다.")
	case errors.Is(err, ErrSoldOut):
		return httpapi.Conflict("coupon.sold_out", "준비된 쿠폰 수량이 모두 소진되었습니다.")
	case errors.Is(err, ErrIssuePending):
		return httpapi.Conflict("coupon.issue_pending", "쿠폰 발급 처리 중입니다. 잠시 후 다시 시도해주세요.")
	default:
		return err
	}
}
