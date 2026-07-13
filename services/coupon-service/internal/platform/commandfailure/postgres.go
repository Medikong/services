package commandfailure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/jobs"
	"github.com/Medikong/services/services/coupon-service/internal/domain/eventing"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

// PostgresSink turns a failed UserCoupon grant into CMD.A.19-14. The original
// command and the follow-up have separate aggregate boundaries; a stable UUID
// makes the hand-off idempotent if completing the original queue item fails.
type PostgresSink struct {
	pool        *pgxpool.Pool
	classifier  jobs.FailureClassifier
	maxAttempts int
}

func NewPostgresSink(pool *pgxpool.Pool, classifier jobs.FailureClassifier, maxAttempts int) (*PostgresSink, error) {
	if pool == nil || maxAttempts < 1 {
		return nil, oops.In("coupon_command_failure").Code("coupon.command_failure_config_invalid").New("postgres pool and positive maximum attempts are required")
	}
	if classifier == nil {
		classifier = jobs.DefaultFailureClassifier{}
	}
	return &PostgresSink{pool: pool, classifier: classifier, maxAttempts: maxAttempts}, nil
}

func (s *PostgresSink) HandleCommandFailure(ctx context.Context, request eventing.CommandRequest, cause error, next time.Time, terminal bool) (resultRef string, handled bool, err error) {
	if request.CommandDocumentID != "CMD.A.19-07" {
		return "", false, nil
	}
	if request.ID == uuid.Nil || !strings.HasPrefix(request.AggregateID, "ireq_") || strings.TrimSpace(request.CorrelationID) == "" {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_correlation_invalid").New("failed issue command correlation is incomplete")
	}
	var payload struct {
		IssueRequestID string `json:"issueRequestId"`
	}
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_payload_invalid").Wrap(err)
	}
	if payload.IssueRequestID != "" && payload.IssueRequestID != request.AggregateID {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_correlation_mismatch").New("failed issue command payload does not match its aggregate target")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_begin_failed").Wrap(err)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()

	var status issuerequest.Status
	var version int64
	var retryCount int
	var currentResultRef string
	if err = tx.QueryRow(ctx, `
		SELECT status,version,retry_count,COALESCE(result_ref,'')
		FROM coupon_issue_requests
		WHERE issue_request_id=$1
		FOR UPDATE
	`, request.AggregateID).Scan(&status, &version, &retryCount, &currentResultRef); err != nil {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_request_read_failed").Wrap(err)
	}
	if status != issuerequest.StatusProcessing {
		if err = tx.Commit(ctx); err != nil {
			return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_commit_failed").Wrap(err)
		}
		committed = true
		return firstResultRef(currentResultRef, request.AggregateID, status), true, nil
	}
	var grantedResultRef string
	err = tx.QueryRow(ctx, `
		SELECT result_ref FROM user_coupons WHERE issue_request_id=$1
	`, request.AggregateID).Scan(&grantedResultRef)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_commit_failed").Wrap(err)
		}
		committed = true
		// The UserCoupon transaction also persisted EVT.A.19-09. POLICY.A.19-15
		// will complete the issue request, so recording a contradictory failure
		// here would corrupt the cross-aggregate outcome.
		return grantedResultRef, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_grant_read_failed").Wrap(err)
	}

	failure := s.classifier.Classify(cause)
	retryable := failure.Retryable && !terminal && retryCount+1 < s.maxAttempts
	failureResultRef := "command:" + request.ID.String() + ":failed"
	commandID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("CMD.A.19-14:"+request.ID.String()))
	commandPayload := map[string]any{
		"issueRequestId": request.AggregateID, "expectedVersion": version,
		"failureCode": failure.Code, "failureResultRef": failureResultRef, "retryable": retryable,
	}
	if retryable {
		commandPayload["nextAttemptAt"] = next.UTC()
	}
	encoded, err := json.Marshal(commandPayload)
	if err != nil {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_payload_encode_failed").Wrap(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO coupon_command_requests (
			command_request_id,command_document_id,aggregate_type,aggregate_id,business_key,
			correlation_id,causation_id,trace_id,payload
		) VALUES ($1,'CMD.A.19-14','CouponIssueRequest',$2,$3,$4,$5,NULLIF($6,''),$7)
		ON CONFLICT (command_request_id) DO NOTHING
	`, commandID, request.AggregateID, "issue-failure:"+request.ID.String(), request.CorrelationID,
		request.ID.String(), request.TraceID, encoded); err != nil {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_enqueue_failed").Wrap(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return "", false, oops.In("coupon_command_failure").Code("coupon.issue_failure_commit_failed").Wrap(err)
	}
	committed = true
	return "command:" + commandID.String(), true, nil
}

func firstResultRef(resultRef, requestID string, status issuerequest.Status) string {
	if resultRef != "" {
		return resultRef
	}
	return fmt.Sprintf("issue_request:%s:%s", requestID, status)
}
