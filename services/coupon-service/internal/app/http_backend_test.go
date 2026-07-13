package app

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/readmodel"
	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	couponhttp "github.com/Medikong/services/services/coupon-service/internal/transport/http"
	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
	"github.com/samber/oops"
)

func TestHTTPBackendCommandMappingsCoverEveryHTTPCommand(t *testing.T) {
	operations := couponhttp.Operations()
	if len(operations) != 25 {
		t.Fatalf("HTTP operation count = %d, want 25", len(operations))
	}
	commandCount := 0
	for _, operation := range operations {
		got := commandIDForAPI(operation.ID)
		if operation.CommandID == "" {
			if got != "" {
				t.Errorf("commandIDForAPI(%q) = %q for query operation", operation.ID, got)
			}
			continue
		}
		commandCount++
		if got != operation.CommandID {
			t.Errorf("commandIDForAPI(%q) = %q, want %q", operation.ID, got, operation.CommandID)
		}
	}
	if commandCount != 17 {
		t.Fatalf("HTTP command count = %d, want 17", commandCount)
	}
}

func TestHTTPBackendReadsRM04CampaignPerformance(t *testing.T) {
	from := time.Date(2026, 7, 11, 0, 0, 0, 0, time.FixedZone("KST", 9*60*60))
	to := from.Add(24 * time.Hour)
	asOf := to.Add(-time.Minute).UTC()
	repository := &campaignPerformanceRepositoryFake{value: readmodel.CampaignPerformance{
		CampaignID: "camp_12345678",
		Counts: readmodel.PerformanceCounts{
			Requested: 10, Issued: 8, Rejected: 1, FailedFinal: 1,
			Reserved: 7, Confirmed: 6, Released: 1, Reclaimed: 2,
		},
		ConfirmedDiscount: &readmodel.Money{Amount: "12000.0000", Currency: "KRW"},
		ReclaimedDiscount: &readmodel.Money{Amount: "3000.0000", Currency: "KRW"},
		AsOf:              asOf,
	}}
	backend := &httpBackend{components: components{
		readRepo:     repository,
		campaignRepo: &campaignRepositoryFake{value: campaign.Campaign{ID: "camp_12345678"}},
	}}
	result, err := backend.Campaign(context.Background(), couponhttp.Call{
		OperationID: "API.A.19-16", Path: map[string]string{"campaignId": "camp_12345678"},
		Query: url.Values{"from": []string{from.Format(time.RFC3339)}, "to": []string{to.Format(time.RFC3339)}},
	})
	if err != nil {
		t.Fatalf("Campaign() error = %v", err)
	}
	if repository.query.CampaignID != "camp_12345678" || repository.query.From == nil || repository.query.To == nil ||
		!repository.query.From.Equal(from.UTC()) || !repository.query.To.Equal(to.UTC()) {
		t.Fatalf("CampaignPerformance() query = %#v", repository.query)
	}
	data, ok := result.Data.(couponhttp.CampaignPerformanceData)
	if !ok {
		t.Fatalf("Campaign() data = %T, want CampaignPerformanceData", result.Data)
	}
	if data.CampaignID != "camp_12345678" || data.Counts.Issued != 8 || data.Counts.Reclaimed != 2 ||
		data.ConfirmedDiscount == nil || data.ConfirmedDiscount.Amount != "12000.0000" ||
		data.ReclaimedDiscount == nil || data.ReclaimedDiscount.Amount != "3000.0000" {
		t.Fatalf("Campaign() data = %#v", data)
	}
	if result.AsOf != asOf.Format(time.RFC3339Nano) {
		t.Fatalf("Campaign() asOf = %q, want %q", result.AsOf, asOf.Format(time.RFC3339Nano))
	}
}

func TestHTTPBackendRejectsMissingCampaignPerformanceProjection(t *testing.T) {
	repository := &campaignPerformanceRepositoryFake{value: readmodel.CampaignPerformance{CampaignID: "camp_12345678"}}
	backend := &httpBackend{components: components{
		readRepo:     repository,
		campaignRepo: &campaignRepositoryFake{value: campaign.Campaign{ID: "camp_12345678"}},
	}}
	_, err := backend.Campaign(context.Background(), couponhttp.Call{
		OperationID: "API.A.19-16", Path: map[string]string{"campaignId": "camp_12345678"}, Query: url.Values{},
	})
	problem, ok := err.(*httpcontract.Error)
	if !ok || problem.Status != 503 {
		t.Fatalf("Campaign() error = %#v", err)
	}
}

type campaignPerformanceRepositoryFake struct {
	readmodel.Repository
	query readmodel.PerformanceQuery
	value readmodel.CampaignPerformance
}

func (f *campaignPerformanceRepositoryFake) CampaignPerformance(_ context.Context, query readmodel.PerformanceQuery) (readmodel.CampaignPerformance, error) {
	f.query = query
	return f.value, nil
}

type campaignRepositoryFake struct {
	campaign.Repository
	value campaign.Campaign
	err   error
}

func (f *campaignRepositoryFake) Get(context.Context, string) (campaign.Campaign, error) {
	return f.value, f.err
}

func TestCostAttributionKeepsOpaqueOrderReference(t *testing.T) {
	repository := &costAttributionRepositoryFake{}
	backend := &httpBackend{components: components{readRepo: repository}}
	_, err := backend.Operations(context.Background(), couponhttp.Call{
		OperationID: "API.A.19-25", Query: url.Values{"orderRef": []string{"order:order_12345678"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.query.OrderID != "order:order_12345678" {
		t.Fatalf("order id = %q", repository.query.OrderID)
	}
}

type costAttributionRepositoryFake struct {
	readmodel.Repository
	query readmodel.CostAttributionQuery
}

func (f *costAttributionRepositoryFake) ListCostAttributions(_ context.Context, query readmodel.CostAttributionQuery) (readmodel.Page[readmodel.CostAttribution], error) {
	f.query = query
	return readmodel.Page[readmodel.CostAttribution]{Items: []readmodel.CostAttribution{}}, nil
}

func TestHTTPBackendResponseMappingsPreservePublicContracts(t *testing.T) {
	now := time.Date(2026, 7, 12, 3, 4, 5, 0, time.UTC)
	job := bulkJobData(bulk.Job{
		ID: "bjob_12345678", CampaignID: "camp_12345678", OwnerServiceID: "operations-service", Status: bulk.StatusCompletedWithFailures,
		TargetCount: 10, SucceededCount: 7, RejectedCount: 2, FailedCount: 1, EvaluationAsOf: now,
	})
	if job.Status != "completed" || job.Counts.Requested != 10 || job.Counts.Issued != 7 || job.Counts.FailedFinal != 1 {
		t.Fatalf("bulkJobData() = %#v", job)
	}

	recovery := recoveryReadItem(readmodel.Failure{
		FailureID: "rcvy_12345678", Kind: "recovery", Status: "retry_failed",
		BusinessKey: "API.A.19-20|workload|rcvy_12345678|secret-idempotency-key",
		SourceRef:   "payload:123", OriginalOperation: "confirm", CurrentAttemptID: "att_12345678",
		ResultKind: "failed", FailureCode: "payment_unavailable", AttemptCount: 2, UpdatedAt: now,
	})
	if recovery.OriginalOperationType != "commit" {
		t.Fatalf("original operation = %q, want commit", recovery.OriginalOperationType)
	}
	if recovery.BusinessKeyRef == "" || recovery.BusinessKeyRef == "API.A.19-20|workload|rcvy_12345678|secret-idempotency-key" {
		t.Fatalf("business key reference leaked or empty: %q", recovery.BusinessKeyRef)
	}
	if recovery.OriginalPayloadRef.Type != "replay_payload" || recovery.OriginalPayloadRef.ID != "payload:123" {
		t.Fatalf("original payload ref = %#v", recovery.OriginalPayloadRef)
	}

}

func TestHTTPBackendMetadataUsesScopedBusinessKeyAndDeadlines(t *testing.T) {
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	backend := &httpBackend{
		policy: configForBackendTest(),
		now:    func() time.Time { return now },
	}
	call := couponhttp.Call{
		OperationID: "API.A.19-01",
		Principal:   principal.Principal{Type: principal.TypeUser, UserID: "user-1"},
		Headers: httpcontract.Headers{
			RequestID: "request-1", IdempotencyKey: "idempotency-key-1234",
			Traceparent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		},
	}
	key := commandBusinessKey(call, "camp_12345678")
	metadata := backend.issuanceMetadata(call, "CMD.A.19-05", key)
	if key != "API.A.19-01|user:user-1|camp_12345678|idempotency-key-1234" {
		t.Fatalf("business key = %q", key)
	}
	if metadata.CorrelationID != "request-1" || metadata.CausationID != "CMD.A.19-05" ||
		metadata.TraceID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("metadata correlation = %#v", metadata)
	}
	if metadata.OccurredAt != now || metadata.LeaseUntil != now.Add(time.Minute) || metadata.ExpiresAt != now.Add(24*time.Hour) {
		t.Fatalf("metadata deadlines = %#v", metadata)
	}
}

func TestCommandBusinessKeySeparatesWorkloadIdentities(t *testing.T) {
	call := couponhttp.Call{
		OperationID: "API.A.19-23",
		Principal:   principal.Principal{Type: principal.TypeService, ServiceID: "cs-service"},
		Headers:     httpcontract.Headers{IdempotencyKey: "idempotency-key-1234"},
	}
	first := commandBusinessKey(call, "camp_12345678")
	call.Principal.ServiceID = "operations-service"
	second := commandBusinessKey(call, "camp_12345678")
	if first == second || first != "API.A.19-23|service:cs-service|camp_12345678|idempotency-key-1234" {
		t.Fatalf("business keys = %q, %q", first, second)
	}
}

func TestWorkloadAuthorizationReceivesOperationAndResourceScope(t *testing.T) {
	authorizer := &workloadAuthorizationFake{}
	backend := &httpBackend{components: components{external: externalPorts{authorization: authorizer}}}
	err := backend.authorizeWorkload(context.Background(), couponhttp.Call{
		OperationID: "API.A.19-15",
		Principal:   principal.Principal{Type: principal.TypeService, ServiceID: "operations-service", Roles: []string{"coupon-operator"}},
		Path:        map[string]string{"bulkJobId": "bjob_12345678"},
		Query:       url.Values{"campaignId": []string{"camp_12345678"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.access.ServiceID != "operations-service" || authorizer.access.OperationID != "API.A.19-15" ||
		authorizer.access.Resources["bulkJobId"] != "bjob_12345678" || authorizer.access.Resources["query.campaignId"] != "camp_12345678" {
		t.Fatalf("access = %#v", authorizer.access)
	}
}

func TestWorkloadAuthorizationIncludesBulkJobOwnership(t *testing.T) {
	authorizer := &workloadAuthorizationFake{}
	backend := &httpBackend{components: components{external: externalPorts{authorization: authorizer}}}
	err := backend.authorizeWorkloadResources(context.Background(), couponhttp.Call{
		OperationID: "API.A.19-15",
		Principal:   principal.Principal{Type: principal.TypeService, ServiceID: "operations-service"},
		Path:        map[string]string{"bulkJobId": "bjob_12345678"},
	}, map[string]string{"ownerServiceId": "operations-service", "operationRequestRef": "task-123"})
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.access.Resources["ownerServiceId"] != "operations-service" ||
		authorizer.access.Resources["operationRequestRef"] != "task-123" {
		t.Fatalf("access = %#v", authorizer.access)
	}
}

func TestBulkJobAuthorizationDenialUsesNotFoundPublicResponse(t *testing.T) {
	err := obscureBulkJobAuthorization(oops.Code("coupon.forbidden").New("not owner"))
	problem, ok := err.(*httpcontract.Error)
	if !ok || problem.Status != http.StatusNotFound || problem.Code != "COUPON_NOT_FOUND" {
		t.Fatalf("obscureBulkJobAuthorization() = %#v", err)
	}
}

type workloadAuthorizationFake struct{ access ports.WorkloadAccess }

func (f *workloadAuthorizationFake) AuthorizeWorkload(_ context.Context, access ports.WorkloadAccess) error {
	f.access = access
	return nil
}

func TestBusinessSignalDoesNotInventMissingHealth(t *testing.T) {
	now := time.Date(2026, 7, 12, 5, 0, 0, 0, time.UTC)
	missing := businessSignal(readmodel.IncidentStatus{Signals: map[readmodel.SignalName]readmodel.IncidentSignal{}}, readmodel.SignalRecovery, now)
	if missing.Status != "unavailable" || missing.LagSeconds != nil {
		t.Fatalf("missing signal = %#v", missing)
	}
	observedAt := now.Add(-7 * time.Second)
	present := businessSignal(readmodel.IncidentStatus{Signals: map[readmodel.SignalName]readmodel.IncidentSignal{
		readmodel.SignalRecovery: {Name: readmodel.SignalRecovery, Status: "degraded", ObservedAt: observedAt},
	}}, readmodel.SignalRecovery, now)
	if present.Status != "degraded" || present.LagSeconds == nil || *present.LagSeconds != 7 {
		t.Fatalf("present signal = %#v", present)
	}
}

func configForBackendTest() config.DomainPolicyConfig {
	return config.DomainPolicyConfig{CommandLease: time.Minute, IdempotencyTTL: 24 * time.Hour}
}
