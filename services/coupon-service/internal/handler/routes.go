package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/packages/go-platform/metrics"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/coupon-service/internal/config"
	"github.com/Medikong/services/services/coupon-service/internal/repository"
	couponservice "github.com/Medikong/services/services/coupon-service/internal/service"
)

const serviceName = config.ServiceName

type Handler struct {
	service couponservice.Service
	metrics *metrics.Registry
}

func RegisterRoutes(mux *http.ServeMux, service couponservice.Service, registry *metrics.Registry) {
	h := Handler{service: service, metrics: registry}
	operational.NewWithMetrics(serviceName, nil, []func(io.Writer){registry.WritePrometheus}).Register(mux)
	mux.HandleFunc("POST /internal/coupon-policies", h.PreparePolicy)
	mux.HandleFunc("GET /internal/coupon-policies/{policyId}", h.GetPolicy)
	mux.HandleFunc("POST /coupons/issue", h.Issue)
	mux.HandleFunc("GET /coupons/me", h.ListMine)
}

func (h Handler) PreparePolicy(w http.ResponseWriter, r *http.Request) {
	var input couponservice.PreparePolicyInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		httpapi.WriteError(w, r, err)
		return
	}
	policy, err := h.service.PreparePolicy(r.Context(), input)
	if err != nil {
		httpapi.WriteError(w, r, mapCouponError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, policy)
}

func (h Handler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := h.service.GetPolicy(r.Context(), r.PathValue("policyId"))
	if err != nil {
		httpapi.WriteError(w, r, mapCouponError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, policy)
}

func (h Handler) Issue(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		h.incIssue("unauthorized")
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	var input couponservice.IssueInput
	if err := httpapi.DecodeJSON(r, &input); err != nil {
		h.incIssue("invalid_request")
		httpapi.WriteError(w, r, err)
		return
	}
	result, err := h.service.Issue(r.Context(), p, input, r.Header.Get(headers.IdempotencyKey))
	if err != nil {
		outcome := couponOutcome(err)
		h.incIssue(outcome)
		httpapi.WriteError(w, r, mapCouponError(err))
		return
	}
	h.incIssue(result.Result)
	logger.Info(r.Context(), "coupon.issue.completed", "result", result.Result, "policy_id", result.Coupon.PolicyID)
	httpapi.WriteJSON(w, http.StatusOK, result)
}

func (h Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	p, err := principal.DecodeHeader(r.Header.Get(headers.Principal))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.Unauthorized("auth.invalid_principal", "Principal 정보가 필요합니다."))
		return
	}
	coupons, err := h.service.ListMine(r.Context(), p)
	if err != nil {
		httpapi.WriteError(w, r, mapCouponError(err))
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"coupons": coupons})
}

func (h Handler) incIssue(result string) {
	h.metrics.Inc("coupon_issue_total", map[string]string{"service": serviceName, "result": result})
}

func couponOutcome(err error) string {
	switch {
	case errors.Is(err, repository.ErrSoldOut):
		return "sold_out"
	case errors.Is(err, repository.ErrPolicyNotReady):
		return "not_ready"
	case errors.Is(err, repository.ErrPolicyNotFound):
		return "not_found"
	case errors.Is(err, couponservice.ErrUnauthorized):
		return "unauthorized"
	default:
		return "failed"
	}
}

func mapCouponError(err error) error {
	switch {
	case errors.Is(err, couponservice.ErrInvalidPolicy):
		return httpapi.BadRequest("coupon.invalid_policy", "쿠폰 정책 값이 올바르지 않습니다.")
	case errors.Is(err, couponservice.ErrInvalidIssue):
		return httpapi.BadRequest("coupon.invalid_issue", "쿠폰 발급 요청 값이 올바르지 않습니다.")
	case errors.Is(err, couponservice.ErrUnauthorized):
		return httpapi.Unauthorized("auth.unauthorized", "인증이 필요합니다.")
	case errors.Is(err, repository.ErrPolicyNotFound):
		return httpapi.Unprocessable("coupon.policy_not_found", "쿠폰 정책을 찾을 수 없습니다.")
	case errors.Is(err, repository.ErrPolicyNotReady):
		return httpapi.Unprocessable("coupon.policy_not_ready", "쿠폰 정책이 아직 준비되지 않았습니다.")
	case errors.Is(err, repository.ErrSoldOut):
		return httpapi.Conflict("coupon.sold_out", "준비된 쿠폰 수량이 모두 소진되었습니다.")
	default:
		return err
	}
}
