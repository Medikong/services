package http

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-authz/principal"
	contractheaders "github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

func TestOperationCatalogMatchesOpenAPIBundle(t *testing.T) {
	operations := Operations()
	if len(operations) != 25 {
		t.Fatalf("operation count = %d, want 25", len(operations))
	}
	ids := make(map[string]struct{}, len(operations))
	methodsAndPaths := make(map[string]struct{}, len(operations))
	publicCount, workloadCount, commandCount := 0, 0, 0
	for _, operation := range operations {
		if _, duplicate := ids[operation.ID]; duplicate {
			t.Fatalf("duplicate API ID %s", operation.ID)
		}
		ids[operation.ID] = struct{}{}
		key := operation.Method + " " + operation.Path
		if _, duplicate := methodsAndPaths[key]; duplicate {
			t.Fatalf("duplicate route %s", key)
		}
		methodsAndPaths[key] = struct{}{}
		if operation.Boundary == httpcontract.BoundaryPublic {
			publicCount++
		} else if operation.Boundary == httpcontract.BoundaryWorkload {
			workloadCount++
		}
		if operation.Command {
			commandCount++
		}
	}
	if publicCount != 4 || workloadCount != 21 || commandCount != 17 {
		t.Fatalf("catalog counts public=%d workload=%d command=%d", publicCount, workloadCount, commandCount)
	}

	bundle, err := os.ReadFile("../../../api/openapi/openapi.bundle.yaml")
	if err != nil {
		t.Fatalf("read OpenAPI bundle: %v", err)
	}
	document := string(bundle)
	for _, operation := range operations {
		marker := "\n  " + operation.Path + ":\n"
		start := strings.Index(document, marker)
		if start < 0 {
			t.Errorf("OpenAPI path missing for %s: %s", operation.ID, operation.Path)
			continue
		}
		block := document[start+len(marker):]
		if next := strings.Index(block, "\n  /api/v1/"); next >= 0 {
			block = block[:next]
		}
		if components := strings.Index(block, "\ncomponents:"); components >= 0 {
			block = block[:components]
		}
		if !strings.Contains(block, "    "+strings.ToLower(operation.Method)+":\n") {
			t.Errorf("OpenAPI method missing for %s: %s", operation.ID, operation.Method)
		}
		if strings.Count(block, "x-api-id: "+operation.ID) != 1 {
			t.Errorf("OpenAPI API ID mismatch for %s", operation.ID)
		}
		if !strings.Contains(block, "        '"+strconv.Itoa(operation.SuccessStatus)+"':") {
			t.Errorf("OpenAPI success status mismatch for %s", operation.ID)
		}
		if operation.BodySchema == "" {
			if strings.Contains(block, "      requestBody:") {
				t.Errorf("unexpected OpenAPI request body for %s", operation.ID)
			}
		} else if !strings.Contains(block, "#/components/schemas/"+operation.BodySchema) {
			t.Errorf("OpenAPI request schema mismatch for %s", operation.ID)
		}
		if operation.ResponseSchema == "" || !strings.Contains(block, "#/components/schemas/"+operation.ResponseSchema) {
			t.Errorf("OpenAPI response schema mismatch for %s", operation.ID)
		}
		assertOpenAPIParameter(t, operation, block, "IdempotencyKey", operation.Command)
		assertOpenAPIParameter(t, operation, block, "XApprovalRef", operation.ApprovalRequired)
		assertOpenAPIParameter(t, operation, block, "XApprovalRefOptional", operation.ApprovalOptional)
		assertOpenAPIParameter(t, operation, block, "XCaseRef", operation.CaseRequired)
		assertOpenAPIHeader(t, operation, block, "Location", operation.LocationHeader)
		assertOpenAPIHeader(t, operation, block, "RetryAfter", operation.RetryAfterHeader)
		if operation.Boundary == httpcontract.BoundaryPublic {
			if !strings.Contains(block, "- MobileBearerAuth: []") {
				t.Errorf("mobile security missing for %s", operation.ID)
			}
			if operation.Command && (!strings.Contains(block, "WebCsrfToken: []") || !strings.Contains(block, "WebOrigin: []")) {
				t.Errorf("web mutation security mismatch for %s", operation.ID)
			}
		} else if !strings.Contains(block, "- WorkloadBearerAuth: []") {
			t.Errorf("workload security missing for %s", operation.ID)
		}
		for _, parameter := range operation.QueryParameters {
			marker := "- name: " + parameter
			if parameter == "cursor" {
				marker = "#/components/parameters/Cursor"
			} else if parameter == "limit" {
				marker = "#/components/parameters/Limit"
			}
			if !strings.Contains(block, marker) {
				t.Errorf("query parameter %s missing for %s", parameter, operation.ID)
			}
		}
	}
}

func assertOpenAPIParameter(t *testing.T, operation Operation, block, name string, expected bool) {
	t.Helper()
	present := strings.Contains(block, "#/components/parameters/"+name+"'")
	if present != expected {
		t.Errorf("OpenAPI parameter %s for %s: present=%v, want %v", name, operation.ID, present, expected)
	}
}

func assertOpenAPIHeader(t *testing.T, operation Operation, block, name string, expected bool) {
	t.Helper()
	present := strings.Contains(block, "#/components/headers/"+name+"'")
	if present != expected {
		t.Errorf("OpenAPI response header %s for %s: present=%v, want %v", name, operation.ID, present, expected)
	}
}

func TestRouterServesAllOpenAPIOperations(t *testing.T) {
	backend := &recordingBackend{}
	router, err := NewRouter(backend, Options{AllowedOrigins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	for _, operation := range Operations() {
		t.Run(operation.ID, func(t *testing.T) {
			request := requestForOperation(t, operation)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != operation.SuccessStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, operation.SuccessStatus, response.Body.String())
			}
			if response.Header().Get(httpcontract.RequestIDHeader) == "" || response.Header().Get(httpcontract.CacheControlHeader) != httpcontract.CacheControlValue {
				t.Fatalf("common headers = %#v", response.Header())
			}
			if operation.LocationHeader && response.Header().Get("Location") == "" {
				t.Fatal("Location header is missing")
			}
			if operation.RetryAfterHeader && response.Header().Get("Retry-After") != "1" {
				t.Fatalf("Retry-After = %q", response.Header().Get("Retry-After"))
			}
		})
	}
	if len(backend.calls) != 25 {
		t.Fatalf("backend call count = %d, want 25", len(backend.calls))
	}
}

func TestRouterEnforcesPrincipalBoundaryBeforeCredentials(t *testing.T) {
	backend := &recordingBackend{}
	router, err := NewRouter(backend, Options{AllowedOrigins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	raw := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/coupons", nil)
	raw.Header.Set("Authorization", "Bearer raw")
	raw.AddCookie(&http.Cookie{Name: "__Host-dm_session", Value: "raw"})
	rawResponse := httptest.NewRecorder()
	router.ServeHTTP(rawResponse, raw)
	if rawResponse.Code != http.StatusUnauthorized {
		t.Fatalf("raw credential status = %d", rawResponse.Code)
	}

	wrongInternal := httptest.NewRequest(http.MethodGet, "/api/v1/internal/coupon-incidents/status", nil)
	wrongInternal.Header.Set(contractheaders.Principal, encodedPrincipal(t, principal.Principal{Type: principal.TypeUser, UserID: "user-1", ClientType: "mobile"}))
	wrongResponse := httptest.NewRecorder()
	router.ServeHTTP(wrongResponse, wrongInternal)
	if wrongResponse.Code != http.StatusUnauthorized {
		t.Fatalf("user on workload status = %d", wrongResponse.Code)
	}

	service := httptest.NewRequest(http.MethodGet, "/api/v1/internal/coupon-incidents/status", nil)
	service.Header.Set(contractheaders.Principal, encodedPrincipal(t, principal.Principal{Type: principal.TypeService, ServiceID: "test-service"}))
	serviceResponse := httptest.NewRecorder()
	router.ServeHTTP(serviceResponse, service)
	if serviceResponse.Code != http.StatusOK {
		t.Fatalf("service without UserID status = %d; body=%s", serviceResponse.Code, serviceResponse.Body.String())
	}
}

func TestWebMutationRequiresOriginAndCSRF(t *testing.T) {
	backend := &recordingBackend{}
	router, err := NewRouter(backend, Options{AllowedOrigins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/coupon-campaigns/camp_12345678/claims", nil)
	request.Header.Set(contractheaders.Principal, encodedPrincipal(t, principal.Principal{Type: principal.TypeUser, UserID: "user-1", ClientType: "web"}))
	request.Header.Set(contractheaders.IdempotencyKey, "operation-key-1234")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/coupon-campaigns/camp_12345678/claims", nil)
	request.Header.Set(contractheaders.Principal, encodedPrincipal(t, principal.Principal{Type: principal.TypeUser, UserID: "user-1", ClientType: "web"}))
	request.Header.Set(contractheaders.IdempotencyKey, "operation-key-1234")
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set(httpcontract.CSRFHeader, "csrf-token")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("valid web mutation status = %d; body=%s", response.Code, response.Body.String())
	}
}

func TestRouterRejectsInvalidHeadersPathQueryAndJSON(t *testing.T) {
	backend := &recordingBackend{}
	router, err := NewRouter(backend, Options{AllowedOrigins: []string{"https://app.example.test"}})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	servicePrincipal := encodedPrincipal(t, principal.Principal{Type: principal.TypeService, ServiceID: "test-service"})
	tests := []struct {
		name    string
		method  string
		path    string
		body    string
		headers map[string]string
	}{
		{
			name:   "approval required",
			method: http.MethodPost,
			path:   "/api/v1/internal/coupon-campaigns",
			body:   bodyForSchema("CreateCouponCampaignRequest"),
			headers: map[string]string{
				contractheaders.Principal:      servicePrincipal,
				contractheaders.IdempotencyKey: "operation-key-1234",
				"Content-Type":                 "application/json",
			},
		},
		{
			name:   "case required",
			method: http.MethodGet,
			path:   "/api/v1/internal/users/user-1/coupon-timeline",
			headers: map[string]string{
				contractheaders.Principal: servicePrincipal,
			},
		},
		{
			name:   "invalid path id",
			method: http.MethodGet,
			path:   "/api/v1/internal/bulk-coupon-issue-jobs/not-a-job",
			headers: map[string]string{
				contractheaders.Principal: servicePrincipal,
			},
		},
		{
			name:   "unknown query",
			method: http.MethodGet,
			path:   "/api/v1/internal/coupon-incidents/status?extra=true",
			headers: map[string]string{
				contractheaders.Principal: servicePrincipal,
			},
		},
		{
			name:   "unknown JSON field",
			method: http.MethodPost,
			path:   "/api/v1/internal/coupon-validations",
			body:   `{"unknown":true}`,
			headers: map[string]string{
				contractheaders.Principal:      servicePrincipal,
				contractheaders.IdempotencyKey: "operation-key-1234",
				"Content-Type":                 "application/json",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Content-Type") != httpcontract.ProblemContentType {
				t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
		})
	}
	if len(backend.calls) != 0 {
		t.Fatalf("backend received invalid calls: %#v", backend.calls)
	}
}

type recordingBackend struct {
	calls []Call
}

func (b *recordingBackend) Campaign(_ context.Context, call Call) (Result, error) {
	return b.record(GroupCampaign, call)
}

func (b *recordingBackend) Issuance(_ context.Context, call Call) (Result, error) {
	return b.record(GroupIssuance, call)
}

func (b *recordingBackend) Redemption(_ context.Context, call Call) (Result, error) {
	return b.record(GroupRedemption, call)
}

func (b *recordingBackend) Operations(_ context.Context, call Call) (Result, error) {
	return b.record(GroupOperations, call)
}

func (b *recordingBackend) record(_ Group, call Call) (Result, error) {
	if call.OperationID == "" || call.Principal.Type == "" {
		return Result{}, oops.Errorf("invalid call: %#v", call)
	}
	b.calls = append(b.calls, call)
	return Result{
		Data:              responseData(call.OperationID),
		Location:          "/api/v1/status/" + call.OperationID,
		RetryAfterSeconds: 1,
	}, nil
}

func responseData(operationID string) any {
	ext := ExternalRef{Context: "test", Type: "resource", ID: "resource:123"}
	snapshot := SnapshotRef{SourceRef: ext, SourceVersion: "1", CapturedAt: "2026-07-11T00:00:00Z", PayloadHash: "sha256:" + strings.Repeat("A", 43)}
	money := Money{Amount: "100", Currency: "KRW"}
	benefit := Benefit{Type: "fixed_amount", Amount: &money}
	policyVersion := 1
	applicability := ApplicabilityPolicy{PolicySchemaVersion: &policyVersion, IncludeTargets: []ExternalRef{ext}, ExcludeTargets: []ExternalRef{}}
	issuer := IssuerAndFunding{IssuerType: "platform", IssuerRef: ext, FunderType: "platform"}
	switch operationID {
	case "API.A.19-01", "API.A.19-02", "API.A.19-23":
		return IssueAcceptedData{IssueRequestID: "ireq_12345678", Status: "accepted", StatusPath: "/api/v1/users/me/coupons?status=pending"}
	case "API.A.19-03":
		return WalletCouponListData{Items: []WalletCoupon{}, ActiveNotices: []ReadOnlyNotice{}}
	case "API.A.19-04":
		return CouponDetailData{UserCouponID: "ucpn_12345678", CampaignID: "camp_12345678", DisplayName: "Campaign", Benefit: benefit, Status: "available", UsableFrom: "2026-07-11T00:00:00Z", ExpiresAt: "2026-07-12T00:00:00Z", PolicyVersion: 1, Applicability: applicability, IssuerAndFunding: issuer}
	case "API.A.19-05":
		return CouponValidationData{RedemptionID: "redm_12345678", Eligible: true, Discount: money, FinalOrderAmount: money, PolicyVersion: 1, OrderSnapshotRef: snapshot, Version: 0}
	case "API.A.19-06", "API.A.19-07", "API.A.19-08", "API.A.19-09":
		return RedemptionData{RedemptionID: "redm_12345678", Status: "reserved", Version: 1, Discount: money}
	case "API.A.19-10", "API.A.19-11", "API.A.19-12", "API.A.19-13":
		return CampaignData{CampaignID: "camp_12345678", Status: "draft", PolicyVersion: 1, Version: 1}
	case "API.A.19-14":
		return BulkJobAcceptedData{BulkJobID: "bjob_12345678", Status: "registered", StatusPath: "/api/v1/internal/bulk-coupon-issue-jobs/bjob_12345678"}
	case "API.A.19-15":
		return BulkJobData{BulkJobID: "bjob_12345678", CampaignID: "camp_12345678", Status: "registered", Counts: PerformanceCounts{}, EvaluationAsOf: "2026-07-11T00:00:00Z"}
	case "API.A.19-16":
		return CampaignPerformanceData{CampaignID: "camp_12345678", Counts: PerformanceCounts{}}
	case "API.A.19-17":
		return OperationalControlData{ControlID: "ctrl_12345678", Scope: OperationalScope{Type: "campaign", Ref: ext}, BlockIssuance: true, EffectiveFrom: "2026-07-11T00:00:00Z", Active: true, Version: 1}
	case "API.A.19-18":
		return ReadOnlyNoticeData{ControlID: "ctrl_12345678", Message: "점검 중입니다.", EffectiveFrom: "2026-07-11T00:00:00Z", Active: true, Version: 1}
	case "API.A.19-19":
		return RecoveryListData{Items: []RecoveryItem{}}
	case "API.A.19-20":
		return RecoveryAttemptAcceptedData{RecoveryID: "rcvy_12345678", AttemptID: "att_12345678", Status: "retry_pending", StatusPath: "/api/v1/internal/coupon-event-recoveries/rcvy_12345678"}
	case "API.A.19-21":
		return RecoveryItem{RecoveryID: "rcvy_12345678", Status: "failed_final", OriginalOperationType: "commit", OriginalPayloadRef: ext, BusinessKeyRef: "business:123", AttemptCount: 1, UpdatedAt: "2026-07-11T00:00:00Z"}
	case "API.A.19-22":
		return TimelineData{Items: []TimelineEvent{}}
	case "API.A.19-24":
		signal := SignalStatus{Status: "normal", AsOf: "2026-07-11T00:00:00Z"}
		return IncidentStatusData{Issuance: signal, Redemption: signal, Recovery: signal, Postgres: signal, Redis: signal, MQ: signal, Workers: signal}
	case "API.A.19-25":
		return CostAttributionListData{Items: []CostAttributionItem{}}
	default:
		return map[string]any{}
	}
}

func requestForOperation(t *testing.T, operation Operation) *http.Request {
	t.Helper()
	path := operation.Path
	replacements := map[string]string{
		"{campaignId}":   "camp_12345678",
		"{userCouponId}": "ucpn_12345678",
		"{redemptionId}": "redm_12345678",
		"{bulkJobId}":    "bjob_12345678",
		"{controlId}":    "ctrl_12345678",
		"{recoveryId}":   "rcvy_12345678",
		"{userId}":       "user-1",
	}
	for placeholder, value := range replacements {
		path = strings.ReplaceAll(path, placeholder, value)
	}
	body := bodyForSchema(operation.BodySchema)
	request := httptest.NewRequest(operation.Method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if operation.Boundary == httpcontract.BoundaryPublic {
		request.Header.Set(contractheaders.Principal, encodedPrincipal(t, principal.Principal{Type: principal.TypeUser, UserID: "user-1", ClientType: "mobile"}))
	} else {
		request.Header.Set(contractheaders.Principal, encodedPrincipal(t, principal.Principal{Type: principal.TypeService, ServiceID: "test-service", Roles: []string{"coupon-workload"}}))
	}
	if operation.Command {
		request.Header.Set(contractheaders.IdempotencyKey, "operation-key-1234")
	}
	if operation.ApprovalRequired {
		request.Header.Set(httpcontract.ApprovalRefHeader, "approval:123")
	}
	if operation.CaseRequired {
		request.Header.Set(httpcontract.CaseRefHeader, "case:123")
	}
	return request
}

func bodyForSchema(schema string) string {
	ext := `{"context":"test","type":"resource","id":"resource:123"}`
	snapshot := fmt.Sprintf(`{"sourceRef":%s,"sourceVersion":"1","capturedAt":"2026-07-11T00:00:00Z","payloadHash":"sha256:%s"}`, ext, strings.Repeat("A", 43))
	money := `{"amount":"100","currency":"KRW"}`
	benefit := fmt.Sprintf(`{"type":"fixed_amount","amount":%s}`, money)
	applicability := fmt.Sprintf(`{"policySchemaVersion":1,"includeTargets":[%s],"excludeTargets":[]}`, ext)
	issuer := fmt.Sprintf(`{"issuerType":"platform","issuerRef":%s,"funderType":"platform"}`, ext)
	switch schema {
	case "":
		return ""
	case "RedeemCouponCodeRequest":
		return `{"code":"ABCD-1234"}`
	case "ValidateCouponRequest":
		return fmt.Sprintf(`{"userCouponId":"ucpn_12345678","orderSnapshot":{"snapshotRef":%s,"orderId":"order:123","userId":"user-1","items":[{"productRef":%s,"sellerRef":%s,"quantity":1,"unitPrice":%s}],"shippingFee":%s},"policyVersion":1}`, snapshot, ext, ext, money, money)
	case "ExpectedVersionRequest":
		return `{"expectedVersion":0}`
	case "RedemptionTransitionRequest":
		return fmt.Sprintf(`{"expectedVersion":0,"resultRef":%s,"reasonCode":"completed"}`, ext)
	case "CreateCouponCampaignRequest":
		return fmt.Sprintf(`{"displayName":"Campaign","benefit":%s,"applicability":%s,"issuerAndFunding":%s,"usableFrom":"2026-07-11T00:00:00Z","expiresAt":"2026-07-12T00:00:00Z","ownerSnapshot":%s}`, benefit, applicability, issuer, snapshot)
	case "ConfigureIssuanceRequest":
		return `{"expectedVersion":0,"totalQuantity":100,"perUserLimit":1,"claimStartsAt":"2026-07-11T00:00:00Z","claimEndsAt":"2026-07-12T00:00:00Z"}`
	case "ReviewCampaignRequest":
		return fmt.Sprintf(`{"expectedVersion":0,"decision":"approved","reasonCode":"approved","sellerOwnershipSnapshot":%s}`, snapshot)
	case "CreatePolicyVersionRequest":
		return fmt.Sprintf(`{"expectedVersion":0,"effectiveAt":"2026-07-12T00:00:00Z","benefit":%s}`, benefit)
	case "CreateBulkIssueJobRequest":
		return fmt.Sprintf(`{"campaignId":"camp_12345678","audienceSnapshot":%s,"evaluationAsOf":"2026-07-11T00:00:00Z","operationTaskRef":%s}`, snapshot, ext)
	case "ApplyOperationalControlRequest":
		return fmt.Sprintf(`{"scope":{"type":"campaign","ref":%s},"blockIssuance":true,"blockRedemption":false,"effectiveFrom":"2026-07-11T00:00:00Z","active":true,"operationTaskRef":%s}`, ext, ext)
	case "ApplyReadOnlyNoticeRequest":
		return `{"expectedVersion":0,"message":"점검 중입니다.","effectiveFrom":"2026-07-11T00:00:00Z","active":true}`
	case "RetryRecoveryRequest", "FinalizeRecoveryRequest":
		return fmt.Sprintf(`{"reasonCode":"retry","operationTaskRef":%s}`, ext)
	case "CreateCompensationIssueRequest":
		return fmt.Sprintf(`{"campaignId":"camp_12345678","userId":"user-1","sourceRef":%s,"reasonCode":"compensation"}`, ext)
	default:
		return `{}`
	}
}

func encodedPrincipal(t *testing.T, value principal.Principal) string {
	t.Helper()
	header, err := principal.EncodeHeader(value)
	if err != nil {
		t.Fatalf("EncodeHeader() error = %v", err)
	}
	return header
}
