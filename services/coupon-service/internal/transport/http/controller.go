package http

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

type Controller struct {
	backend Backend
}

func (c Controller) Handle(operation Operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers, problem := httpcontract.ReadHeaders(r, httpcontract.HeaderRules{
			Idempotency: operation.Command,
			Approval:    operation.ApprovalRequired,
			Case:        operation.CaseRequired,
		})
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
		path, problem := readPath(operation, r)
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
		query, problem := readQuery(operation, r.URL.Query())
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
		body, problem := readBody(operation, w, r)
		if problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
		call := Call{
			OperationID: operation.ID,
			Principal:   httpcontract.Principal(r.Context()),
			Headers:     headers,
			Path:        path,
			Query:       query,
			Body:        body,
		}
		result, err := c.dispatch(r, operation.Group, call)
		if err != nil {
			httpcontract.WriteProblem(w, r, httpcontract.FromError(err))
			return
		}
		if problem := writeOperationHeaders(w, operation, result); problem != nil {
			httpcontract.WriteProblem(w, r, problem)
			return
		}
		httpcontract.WriteJSON(w, r, operation.SuccessStatus, result.Data, result.AsOf)
	}
}

func (c Controller) dispatch(r *http.Request, group Group, call Call) (Result, error) {
	switch group {
	case GroupCampaign:
		return c.backend.Campaign(r.Context(), call)
	case GroupIssuance:
		return c.backend.Issuance(r.Context(), call)
	case GroupRedemption:
		return c.backend.Redemption(r.Context(), call)
	case GroupOperations:
		return c.backend.Operations(r.Context(), call)
	default:
		return Result{}, httpcontract.Internal(nil)
	}
}

func readBody(operation Operation, w http.ResponseWriter, r *http.Request) (any, *httpcontract.Error) {
	if operation.BodySchema == "" {
		if r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
			return nil, httpcontract.InputInvalid("body", "unexpected_body")
		}
		return nil, nil
	}
	body := newRequestBody(operation.BodySchema)
	if body == nil {
		return nil, httpcontract.Internal(nil)
	}
	if problem := httpcontract.DecodeJSON(w, r, body); problem != nil {
		return nil, problem
	}
	if problem := validateBody(body); problem != nil {
		return nil, problem
	}
	return body, nil
}

func newRequestBody(schema string) any {
	switch schema {
	case "RedeemCouponCodeRequest":
		return &RedeemCouponCodeRequest{}
	case "ValidateCouponRequest":
		return &ValidateCouponRequest{}
	case "ExpectedVersionRequest":
		return &ExpectedVersionRequest{}
	case "RedemptionTransitionRequest":
		return &RedemptionTransitionRequest{}
	case "CreateCouponCampaignRequest":
		return &CreateCouponCampaignRequest{}
	case "ConfigureIssuanceRequest":
		return &ConfigureIssuanceRequest{}
	case "ReviewCampaignRequest":
		return &ReviewCampaignRequest{}
	case "CreatePolicyVersionRequest":
		return &CreatePolicyVersionRequest{}
	case "CreateBulkIssueJobRequest":
		return &CreateBulkIssueJobRequest{}
	case "ApplyOperationalControlRequest":
		return &ApplyOperationalControlRequest{}
	case "ApplyReadOnlyNoticeRequest":
		return &ApplyReadOnlyNoticeRequest{}
	case "RetryRecoveryRequest":
		return &RetryRecoveryRequest{}
	case "FinalizeRecoveryRequest":
		return &FinalizeRecoveryRequest{}
	case "CreateCompensationIssueRequest":
		return &CreateCompensationIssueRequest{}
	default:
		return nil
	}
}

func readPath(operation Operation, r *http.Request) (map[string]string, *httpcontract.Error) {
	result := make(map[string]string)
	for name, pattern := range map[string]*regexp.Regexp{
		"campaignId":   campaignIDPattern,
		"userCouponId": userCouponIDPattern,
		"redemptionId": redemptionIDPattern,
		"bulkJobId":    bulkJobIDPattern,
		"controlId":    controlIDPattern,
		"recoveryId":   recoveryIDPattern,
	} {
		if !strings.Contains(operation.Path, "{"+name+"}") {
			continue
		}
		value := chi.URLParam(r, name)
		if !pattern.MatchString(value) {
			return nil, httpcontract.InputInvalid(name, "invalid_format")
		}
		result[name] = value
	}
	if strings.Contains(operation.Path, "{userId}") {
		value := chi.URLParam(r, "userId")
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 128 || strings.TrimSpace(value) != value {
			return nil, httpcontract.InputInvalid("userId", "invalid_format")
		}
		result["userId"] = value
	}
	return result, nil
}

func readQuery(operation Operation, values url.Values) (url.Values, *httpcontract.Error) {
	allowed := make(map[string]struct{}, len(operation.QueryParameters))
	for _, name := range operation.QueryParameters {
		allowed[name] = struct{}{}
	}
	result := make(url.Values, len(values))
	for name, candidates := range values {
		if _, ok := allowed[name]; !ok {
			return nil, httpcontract.InputInvalid(name, "unexpected_query_parameter")
		}
		if len(candidates) != 1 || candidates[0] == "" {
			return nil, httpcontract.InputInvalid(name, "invalid_query_parameter")
		}
		value := candidates[0]
		if problem := validateQueryValue(operation.ID, name, value); problem != nil {
			return nil, problem
		}
		result.Set(name, value)
	}
	return result, nil
}

func validateQueryValue(operationID, name, value string) *httpcontract.Error {
	switch name {
	case "cursor":
		if len(value) > 512 {
			return httpcontract.InputInvalid(name, "too_long")
		}
	case "limit":
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			return httpcontract.InputInvalid(name, "out_of_range")
		}
	case "status":
		if operationID == "API.A.19-03" && !oneOf(value, "pending", "available", "reserved", "used", "reclaimed", "expired", "failed") {
			return httpcontract.InputInvalid(name, "invalid_enum")
		}
		if operationID == "API.A.19-19" && !oneOf(value, "recorded", "retry_pending", "retrying", "retry_failed", "completed", "failed_final") {
			return httpcontract.InputInvalid(name, "invalid_enum")
		}
	case "originalOperationType":
		if !oneOf(value, "reserve", "commit", "release", "revoke") {
			return httpcontract.InputInvalid(name, "invalid_enum")
		}
	case "from", "to":
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return httpcontract.InputInvalid(name, "invalid_date_time")
		}
	case "orderRef":
		if problem := opaqueRef(name, value); problem != nil {
			return problem
		}
	case "campaignId":
		if !campaignIDPattern.MatchString(value) {
			return httpcontract.InputInvalid(name, "invalid_format")
		}
	}
	return nil
}

func writeOperationHeaders(w http.ResponseWriter, operation Operation, result Result) *httpcontract.Error {
	if operation.LocationHeader {
		if !strings.HasPrefix(result.Location, "/") {
			return httpcontract.Internal(nil)
		}
	}
	if operation.RetryAfterHeader {
		if result.RetryAfterSeconds < 1 {
			return httpcontract.Internal(nil)
		}
	}
	if operation.LocationHeader {
		w.Header().Set("Location", result.Location)
	}
	if operation.RetryAfterHeader {
		w.Header().Set("Retry-After", strconv.Itoa(result.RetryAfterSeconds))
	}
	return nil
}
