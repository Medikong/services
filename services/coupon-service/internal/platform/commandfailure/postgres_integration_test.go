//go:build integration

package commandfailure_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/commanding"
	"github.com/Medikong/services/services/coupon-service/internal/application/jobs"
	"github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/platform/commandfailure"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPostgresSinkSchedulesIdempotentIssueFailureCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon_command_failure"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migration.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO coupon_issue_requests (
		issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,
		issuer_and_funding_snapshot,policy_snapshot,lease_owner,lease_until,version
	) VALUES ('ireq_failure01','camp_failure01','user-1','issue:failure','claim','claim:1','processing','{}','{}','command_queue:CMD.A.19-07','infinity',3)`)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"issueRequestId": "ireq_failure01"})
	request := eventing.CommandRequest{
		ID: uuid.New(), CommandDocumentID: "CMD.A.19-07", AggregateID: "ireq_failure01",
		BusinessKey: "issue:failure", CorrelationID: "correlation-1", TraceID: "trace-1", Payload: payload,
	}
	sink, err := commandfailure.NewPostgresSink(pool, retryableClassifier{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, handled, err := sink.HandleCommandFailure(ctx, request, errors.New("business failure"), time.Now().Add(time.Minute), false); err != nil || !handled {
			t.Fatalf("HandleCommandFailure() handled=%v error=%v", handled, err)
		}
	}
	var count int
	var commandID, aggregateID string
	var recordedPayload []byte
	if err := pool.QueryRow(ctx, `SELECT count(*),max(command_document_id),max(aggregate_id),max(payload::text)
		FROM coupon_command_requests WHERE command_document_id='CMD.A.19-14'`).Scan(&count, &commandID, &aggregateID, &recordedPayload); err != nil {
		t.Fatal(err)
	}
	if count != 1 || commandID != "CMD.A.19-14" || aggregateID != "ireq_failure01" || !json.Valid(recordedPayload) {
		t.Fatalf("follow-up count=%d command=%s aggregate=%s payload=%s", count, commandID, aggregateID, recordedPayload)
	}

	_, err = pool.Exec(ctx, `INSERT INTO coupon_issue_requests (
		issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,retry_count,
		issuer_and_funding_snapshot,policy_snapshot,lease_owner,lease_until,version
	) VALUES ('ireq_exhausted1','camp_failure01','user-3','issue:exhausted','claim','claim:3','processing',2,'{}','{}','command_queue:CMD.A.19-07','infinity',7)`)
	if err != nil {
		t.Fatal(err)
	}
	exhaustedPayload, _ := json.Marshal(map[string]any{"issueRequestId": "ireq_exhausted1"})
	exhaustedRequest := eventing.CommandRequest{
		ID: uuid.New(), CommandDocumentID: "CMD.A.19-07", AggregateID: "ireq_exhausted1",
		BusinessKey: "issue:exhausted", CorrelationID: "correlation-3", Payload: exhaustedPayload,
	}
	if _, handled, err := sink.HandleCommandFailure(ctx, exhaustedRequest, errors.New("temporary outage"), time.Now().Add(time.Minute), false); err != nil || !handled {
		t.Fatalf("exhausted failure handled=%v error=%v", handled, err)
	}
	var retryable bool
	var hasNext bool
	if err := pool.QueryRow(ctx, `SELECT (payload->>'retryable')::boolean,payload ? 'nextAttemptAt'
		FROM coupon_command_requests WHERE command_document_id='CMD.A.19-14' AND aggregate_id='ireq_exhausted1'`).Scan(&retryable, &hasNext); err != nil {
		t.Fatal(err)
	}
	if retryable || hasNext {
		t.Fatalf("exhausted payload retryable=%v hasNext=%v", retryable, hasNext)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO coupon_issue_requests (
			issue_request_id,campaign_id,user_id,business_key,source_type,source_ref,status,
			issuer_and_funding_snapshot,policy_snapshot,lease_owner,lease_until,version
		) VALUES ('ireq_granted001','camp_failure01','user-2','issue:granted','claim','claim:2','processing','{}','{}','command_queue:CMD.A.19-07','infinity',2)
	`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO user_coupons (
			user_coupon_id,campaign_id,policy_version,user_id,issue_request_id,status,
			usable_from,expires_at,grant_snapshot,result_ref,version
		) VALUES ('ucpn_granted001','camp_failure01',1,'user-2','ireq_granted001','granted',now(),now()+interval '1 day','{}','user_coupon:ucpn_granted001:granted',1)
	`)
	if err != nil {
		t.Fatal(err)
	}
	grantedPayload, _ := json.Marshal(map[string]any{"issueRequestId": "ireq_granted001"})
	grantedRequest := eventing.CommandRequest{
		ID: uuid.New(), CommandDocumentID: "CMD.A.19-07", AggregateID: "ireq_granted001",
		BusinessKey: "issue:granted", CorrelationID: "correlation-2", Payload: grantedPayload,
	}
	resultRef, handled, err := sink.HandleCommandFailure(ctx, grantedRequest, errors.New("ambiguous commit"), time.Now().Add(time.Minute), false)
	if err != nil || !handled || resultRef != "user_coupon:ucpn_granted001:granted" {
		t.Fatalf("granted failure result=%q handled=%v error=%v", resultRef, handled, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM coupon_command_requests WHERE command_document_id='CMD.A.19-14' AND aggregate_id='ireq_granted001'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("granted issue failure command count=%d", count)
	}

	ingress, err := commanding.NewOperationsIngress(eventing.NewPostgresCommandQueue(pool))
	if err != nil {
		t.Fatal(err)
	}
	metadata := commanding.Metadata{BusinessKey: "operation:finalize:ireq_failure01", CorrelationID: "operation-request-1"}
	for range 2 {
		if _, err := ingress.SubmitFinalIssueFailure(ctx, commanding.FinalIssueFailureInput{
			IssueRequestID: "ireq_failure01", FailureCode: "retry_exhausted", ApprovalRef: "approval-1",
		}, metadata); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM coupon_command_requests WHERE command_document_id='CMD.A.19-22' AND aggregate_id='ireq_failure01'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idempotent CMD.A.19-22 count=%d", count)
	}
}

type retryableClassifier struct{}

func (retryableClassifier) Classify(error) jobs.Failure {
	return jobs.Failure{Code: "DEPENDENCY_UNAVAILABLE", Retryable: true}
}
