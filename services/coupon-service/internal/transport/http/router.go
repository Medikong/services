package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

type Operation struct {
	ID               string
	CommandID        string
	Method           string
	Path             string
	Group            Group
	Boundary         httpcontract.Boundary
	Command          bool
	ApprovalRequired bool
	ApprovalOptional bool
	CaseRequired     bool
	BodySchema       string
	ResponseSchema   string
	QueryParameters  []string
	SuccessStatus    int
	LocationHeader   bool
	RetryAfterHeader bool
}

var operationCatalog = []Operation{
	{ID: "API.A.19-01", CommandID: "CMD.A.19-05", Method: http.MethodPost, Path: "/api/v1/coupon-campaigns/{campaignId}/claims", Group: GroupIssuance, Boundary: httpcontract.BoundaryPublic, Command: true, ResponseSchema: "IssueAcceptedResponse", SuccessStatus: http.StatusAccepted, LocationHeader: true, RetryAfterHeader: true},
	{ID: "API.A.19-02", CommandID: "CMD.A.19-06", Method: http.MethodPost, Path: "/api/v1/coupon-code-redemptions", Group: GroupIssuance, Boundary: httpcontract.BoundaryPublic, Command: true, BodySchema: "RedeemCouponCodeRequest", ResponseSchema: "IssueAcceptedResponse", SuccessStatus: http.StatusAccepted, LocationHeader: true, RetryAfterHeader: true},
	{ID: "API.A.19-03", Method: http.MethodGet, Path: "/api/v1/users/me/coupons", Group: GroupIssuance, Boundary: httpcontract.BoundaryPublic, QueryParameters: []string{"status", "cursor", "limit"}, ResponseSchema: "WalletCouponListResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-04", Method: http.MethodGet, Path: "/api/v1/users/me/coupons/{userCouponId}", Group: GroupIssuance, Boundary: httpcontract.BoundaryPublic, ResponseSchema: "CouponDetailResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-05", CommandID: "CMD.A.19-09", Method: http.MethodPost, Path: "/api/v1/internal/coupon-validations", Group: GroupRedemption, Boundary: httpcontract.BoundaryWorkload, Command: true, BodySchema: "ValidateCouponRequest", ResponseSchema: "CouponValidationResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-06", CommandID: "CMD.A.19-10", Method: http.MethodPost, Path: "/api/v1/internal/coupon-redemptions/{redemptionId}/reserve", Group: GroupRedemption, Boundary: httpcontract.BoundaryWorkload, Command: true, BodySchema: "ExpectedVersionRequest", ResponseSchema: "RedemptionResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-07", CommandID: "CMD.A.19-11", Method: http.MethodPost, Path: "/api/v1/internal/coupon-redemptions/{redemptionId}/commit", Group: GroupRedemption, Boundary: httpcontract.BoundaryWorkload, Command: true, BodySchema: "RedemptionTransitionRequest", ResponseSchema: "RedemptionResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-08", CommandID: "CMD.A.19-12", Method: http.MethodPost, Path: "/api/v1/internal/coupon-redemptions/{redemptionId}/release", Group: GroupRedemption, Boundary: httpcontract.BoundaryWorkload, Command: true, BodySchema: "RedemptionTransitionRequest", ResponseSchema: "RedemptionResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-09", CommandID: "CMD.A.19-15", Method: http.MethodPost, Path: "/api/v1/internal/coupon-redemptions/{redemptionId}/revoke", Group: GroupRedemption, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalOptional: true, BodySchema: "RedemptionTransitionRequest", ResponseSchema: "RedemptionResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-10", CommandID: "CMD.A.19-01", Method: http.MethodPost, Path: "/api/v1/internal/coupon-campaigns", Group: GroupCampaign, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalOptional: true, BodySchema: "CreateCouponCampaignRequest", ResponseSchema: "CampaignResponse", SuccessStatus: http.StatusCreated, LocationHeader: true},
	{ID: "API.A.19-11", CommandID: "CMD.A.19-02", Method: http.MethodPut, Path: "/api/v1/internal/coupon-campaigns/{campaignId}/issuance-policy", Group: GroupCampaign, Boundary: httpcontract.BoundaryWorkload, Command: true, BodySchema: "ConfigureIssuanceRequest", ResponseSchema: "CampaignResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-12", CommandID: "CMD.A.19-03", Method: http.MethodPost, Path: "/api/v1/internal/coupon-campaigns/{campaignId}/reviews", Group: GroupCampaign, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalRequired: true, BodySchema: "ReviewCampaignRequest", ResponseSchema: "CampaignResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-13", CommandID: "CMD.A.19-04", Method: http.MethodPost, Path: "/api/v1/internal/coupon-campaigns/{campaignId}/policy-versions", Group: GroupCampaign, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalRequired: true, BodySchema: "CreatePolicyVersionRequest", ResponseSchema: "CampaignResponse", SuccessStatus: http.StatusCreated},
	{ID: "API.A.19-14", CommandID: "CMD.A.19-08", Method: http.MethodPost, Path: "/api/v1/internal/bulk-coupon-issue-jobs", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalRequired: true, BodySchema: "CreateBulkIssueJobRequest", ResponseSchema: "BulkJobAcceptedResponse", SuccessStatus: http.StatusAccepted, LocationHeader: true, RetryAfterHeader: true},
	{ID: "API.A.19-15", Method: http.MethodGet, Path: "/api/v1/internal/bulk-coupon-issue-jobs/{bulkJobId}", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, ResponseSchema: "BulkJobResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-16", Method: http.MethodGet, Path: "/api/v1/internal/coupon-campaigns/{campaignId}/performance", Group: GroupCampaign, Boundary: httpcontract.BoundaryWorkload, QueryParameters: []string{"from", "to", "groupBy"}, ResponseSchema: "CampaignPerformanceResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-17", CommandID: "CMD.A.19-20", Method: http.MethodPost, Path: "/api/v1/internal/coupon-operational-controls", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalRequired: true, BodySchema: "ApplyOperationalControlRequest", ResponseSchema: "OperationalControlResponse", SuccessStatus: http.StatusCreated, LocationHeader: true},
	{ID: "API.A.19-18", CommandID: "CMD.A.19-31", Method: http.MethodPut, Path: "/api/v1/internal/coupon-operational-controls/{controlId}/read-only-notice", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalRequired: true, BodySchema: "ApplyReadOnlyNoticeRequest", ResponseSchema: "ReadOnlyNoticeResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-19", Method: http.MethodGet, Path: "/api/v1/internal/coupon-event-recoveries", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, QueryParameters: []string{"status", "originalOperationType", "cursor", "limit"}, ResponseSchema: "RecoveryListResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-20", CommandID: "CMD.A.19-21", Method: http.MethodPost, Path: "/api/v1/internal/coupon-event-recoveries/{recoveryId}/retry-attempts", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalRequired: true, BodySchema: "RetryRecoveryRequest", ResponseSchema: "RecoveryAttemptAcceptedResponse", SuccessStatus: http.StatusAccepted, LocationHeader: true, RetryAfterHeader: true},
	{ID: "API.A.19-21", CommandID: "CMD.A.19-25", Method: http.MethodPost, Path: "/api/v1/internal/coupon-event-recoveries/{recoveryId}/finalization", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalRequired: true, BodySchema: "FinalizeRecoveryRequest", ResponseSchema: "RecoveryResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-22", Method: http.MethodGet, Path: "/api/v1/internal/users/{userId}/coupon-timeline", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, CaseRequired: true, QueryParameters: []string{"cursor", "limit"}, ResponseSchema: "TimelineResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-23", CommandID: "CMD.A.19-13", Method: http.MethodPost, Path: "/api/v1/internal/compensation-coupon-issue-requests", Group: GroupIssuance, Boundary: httpcontract.BoundaryWorkload, Command: true, ApprovalOptional: true, CaseRequired: true, BodySchema: "CreateCompensationIssueRequest", ResponseSchema: "IssueAcceptedResponse", SuccessStatus: http.StatusAccepted, LocationHeader: true, RetryAfterHeader: true},
	{ID: "API.A.19-24", Method: http.MethodGet, Path: "/api/v1/internal/coupon-incidents/status", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, ResponseSchema: "IncidentStatusResponse", SuccessStatus: http.StatusOK},
	{ID: "API.A.19-25", Method: http.MethodGet, Path: "/api/v1/internal/coupon-cost-attributions", Group: GroupOperations, Boundary: httpcontract.BoundaryWorkload, QueryParameters: []string{"orderRef", "campaignId", "from", "to", "cursor", "limit"}, ResponseSchema: "CostAttributionListResponse", SuccessStatus: http.StatusOK},
}

type Options struct {
	AllowedOrigins []string
}

func NewRouter(backend Backend, options Options) (*chi.Mux, error) {
	if backend == nil {
		return nil, oops.New("coupon HTTP backend is required")
	}
	contract, err := httpcontract.New(options.AllowedOrigins)
	if err != nil {
		return nil, err
	}
	controller := Controller{backend: backend}
	router := chi.NewRouter()
	router.Use(httpcontract.RequestIDMiddleware)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpcontract.WriteProblem(w, r, httpcontract.NotFound())
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpcontract.WriteProblem(w, r, httpcontract.MethodNotAllowed())
	})
	for _, operation := range operationCatalog {
		operation := operation
		authenticate := contract.Authenticate(operation.Boundary, operation.Command && operation.Boundary == httpcontract.BoundaryPublic)
		router.With(authenticate).MethodFunc(operation.Method, operation.Path, controller.Handle(operation))
	}
	return router, nil
}

func Operations() []Operation {
	result := make([]Operation, len(operationCatalog))
	copy(result, operationCatalog)
	for index := range result {
		result[index].QueryParameters = append([]string(nil), result[index].QueryParameters...)
	}
	return result
}
