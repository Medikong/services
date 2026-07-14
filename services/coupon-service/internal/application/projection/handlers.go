package projection

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/Medikong/services/services/coupon-service/internal/domain/readmodel"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"
)

type issuePayload struct {
	IssueRequestID string     `json:"issueRequestId"`
	CampaignID     string     `json:"campaignId"`
	UserID         string     `json:"userId"`
	BusinessKey    string     `json:"businessKey"`
	SourceRef      string     `json:"sourceRef"`
	Status         string     `json:"status"`
	UserCouponID   string     `json:"userCouponId"`
	FailureCode    string     `json:"failureCode"`
	RetryCount     int        `json:"retryCount"`
	NextAttemptAt  *time.Time `json:"nextAttemptAt"`
	ResultRef      string     `json:"resultRef"`
}

type userCouponPayload struct {
	UserCouponID   string          `json:"userCouponId"`
	CampaignID     string          `json:"campaignId"`
	PolicyVersion  int             `json:"policyVersion"`
	UserID         string          `json:"userId"`
	IssueRequestID string          `json:"issueRequestId"`
	Status         string          `json:"status"`
	UsableFrom     time.Time       `json:"usableFrom"`
	ExpiresAt      time.Time       `json:"expiresAt"`
	GrantSnapshot  json.RawMessage `json:"grantSnapshot"`
	ResultRef      string          `json:"resultRef"`
}

type grantSnapshot struct {
	DisplayName      string                        `json:"displayName"`
	Benefit          readmodel.Benefit             `json:"benefit"`
	Applicability    readmodel.ApplicabilityPolicy `json:"applicability"`
	IssuerAndFunding readmodel.IssuerAndFunding    `json:"issuerAndFunding"`
}

type redemptionPayload struct {
	RedemptionID     string                `json:"redemption_id"`
	UserCouponID     string                `json:"user_coupon_id"`
	CampaignID       string                `json:"campaign_id"`
	UserID           string                `json:"user_id"`
	OrderRef         readmodel.ExternalRef `json:"order_ref"`
	Status           string                `json:"status"`
	ResultRef        readmodel.ExternalRef `json:"result_ref"`
	PolicyVersion    int                   `json:"policy_version"`
	Discount         readmodel.Money       `json:"discount"`
	FinalOrderAmount readmodel.Money       `json:"final_order_amount"`
	CostShares       []readmodel.CostShare `json:"cost_shares"`
	EvaluatedAt      time.Time             `json:"evaluated_at"`
	ReservedUntil    *time.Time            `json:"reserved_until"`
}

type bulkPayload struct {
	BulkJobID      string `json:"bulk_job_id"`
	CampaignID     string `json:"campaign_id"`
	Status         string `json:"status"`
	TargetCount    int64  `json:"target_count"`
	SucceededCount int64  `json:"succeeded_count"`
	RejectedCount  int64  `json:"rejected_count"`
	FailedCount    int64  `json:"failed_count"`
}

type recoveryPayload struct {
	RecoveryID            string     `json:"recovery_id"`
	RedemptionID          string     `json:"redemption_id"`
	OriginalOperationType string     `json:"original_operation_type"`
	OriginalPayloadRef    string     `json:"original_payload_ref"`
	AttemptID             string     `json:"attempt_id"`
	BusinessKey           string     `json:"business_key"`
	Status                string     `json:"status"`
	ResultKind            string     `json:"result_kind"`
	ResultRef             string     `json:"result_ref"`
	FailureCode           string     `json:"failure_code"`
	AttemptCount          int        `json:"attempt_count"`
	NextAttemptAt         *time.Time `json:"next_attempt_at"`
}

type noticePayload struct {
	ControlID string `json:"control_id"`
	Scopes    []struct {
		Type string `json:"type"`
		Ref  string `json:"ref"`
	} `json:"scopes"`
	Notice struct {
		Message       string    `json:"message"`
		Active        bool      `json:"active"`
		EffectiveFrom time.Time `json:"effectiveFrom"`
	} `json:"notice"`
}

type performanceDelta struct {
	Requested       int64
	Issued          int64
	Rejected        int64
	Failed          int64
	Reserved        int64
	Confirmed       int64
	Released        int64
	Reclaimed       int64
	ConfirmedAmount string
	ReclaimedAmount string
	Currency        string
}

func projectIssueEvent(ctx context.Context, tx pgx.Tx, event policy.Envelope) error {
	value, err := decodeEventData[issuePayload](event)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value.IssueRequestID) == "" || strings.TrimSpace(value.CampaignID) == "" ||
		strings.TrimSpace(value.UserID) == "" || strings.TrimSpace(value.BusinessKey) == "" {
		return payloadError(event, "issue request identity is incomplete")
	}
	resultRef := stringResultRef(value.ResultRef, "issue_request", value.IssueRequestID)
	switch event.EventDocumentID {
	case "EVT.A.19-07":
		if err := addPerformance(ctx, tx, event, value.CampaignID, performanceDelta{Requested: 1}); err != nil {
			return err
		}
		if err := insertTimeline(ctx, tx, event, value.UserID, value.UserCouponID, resultRef); err != nil {
			return err
		}
		return upsertIncident(ctx, tx, event, readmodel.SignalIssuance, value.CampaignID, "normal", map[string]int64{"requested": 1})
	case "EVT.A.19-08":
		if err := addPerformance(ctx, tx, event, value.CampaignID, performanceDelta{Rejected: 1}); err != nil {
			return err
		}
		if err := insertTimeline(ctx, tx, event, value.UserID, value.UserCouponID, resultRef); err != nil {
			return err
		}
		return upsertIncident(ctx, tx, event, readmodel.SignalIssuance, value.CampaignID, "normal", map[string]int64{"rejected": 1})
	case "EVT.A.19-10", "EVT.A.19-11", "EVT.A.19-37":
		failureCode := value.FailureCode
		if failureCode == "" && event.EventDocumentID == "EVT.A.19-37" {
			failureCode = "COUPON_ISSUE_RETRY_PENDING"
		}
		if failureCode == "" {
			return payloadError(event, "issue failure code is required")
		}
		status := value.Status
		if status == "" {
			status = "failed_retryable"
		}
		sourceRef := value.SourceRef
		if sourceRef == "" {
			sourceRef = "issue_request:" + value.IssueRequestID
		}
		if err := upsertFailure(ctx, tx, event, "issue:"+value.IssueRequestID, status, value.BusinessKey, sourceRef, failureCode, value.RetryCount, value.NextAttemptAt); err != nil {
			return err
		}
		if event.EventDocumentID != "EVT.A.19-37" {
			if err := insertTimeline(ctx, tx, event, value.UserID, value.UserCouponID, resultRef); err != nil {
				return err
			}
		}
		incidentStatus := "degraded"
		metrics := map[string]int64{"retryable_failures": 1}
		if event.EventDocumentID == "EVT.A.19-11" {
			if err := addPerformance(ctx, tx, event, value.CampaignID, performanceDelta{Failed: 1}); err != nil {
				return err
			}
			incidentStatus = "critical"
			metrics = map[string]int64{"failed_final": 1}
		}
		return upsertIncident(ctx, tx, event, readmodel.SignalIssuance, value.CampaignID, incidentStatus, metrics)
	case "EVT.A.19-29":
		if _, err := tx.Exec(ctx, `UPDATE rm_coupon_failures SET failure_status='completed',last_event_id=$2,
			projection_version=GREATEST(projection_version,$3),updated_at=$4 WHERE failure_id=$1`,
			"issue:"+value.IssueRequestID, event.EventID, event.AggregateVersion, event.OccurredAt); err != nil {
			return projectorError("resolve_issue_failure", err)
		}
		if err := insertTimeline(ctx, tx, event, value.UserID, value.UserCouponID, resultRef); err != nil {
			return err
		}
		return upsertIncident(ctx, tx, event, readmodel.SignalIssuance, value.CampaignID, "normal", map[string]int64{"completed": 1})
	default:
		return nil
	}
}

func projectUserCouponEvent(ctx context.Context, tx pgx.Tx, event policy.Envelope) error {
	value, err := decodeEventData[userCouponPayload](event)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value.UserCouponID) == "" || strings.TrimSpace(value.CampaignID) == "" ||
		strings.TrimSpace(value.UserID) == "" || value.PolicyVersion < 1 || value.UsableFrom.IsZero() ||
		!value.UsableFrom.Before(value.ExpiresAt) {
		return payloadError(event, "user coupon identity and validity are incomplete")
	}
	if event.EventDocumentID == "EVT.A.19-31" {
		if err := updateCouponStatus(ctx, tx, event, value.UserID, value.UserCouponID, readmodel.WalletStatusExpired); err != nil {
			return err
		}
		if err := insertTimeline(ctx, tx, event, value.UserID, value.UserCouponID,
			stringResultRef(value.ResultRef, "user_coupon", value.UserCouponID)); err != nil {
			return err
		}
		return upsertIncident(ctx, tx, event, readmodel.SignalIssuance, value.CampaignID, "normal", map[string]int64{"expired": 1})
	}
	var snapshot grantSnapshot
	if len(value.GrantSnapshot) == 0 || json.Unmarshal(value.GrantSnapshot, &snapshot) != nil ||
		strings.TrimSpace(snapshot.DisplayName) == "" || strings.TrimSpace(snapshot.Benefit.Type) == "" ||
		snapshot.Applicability.PolicySchemaVersion != 1 || strings.TrimSpace(snapshot.IssuerAndFunding.IssuerType) == "" ||
		strings.TrimSpace(snapshot.IssuerAndFunding.IssuerRef.ID) == "" || strings.TrimSpace(snapshot.IssuerAndFunding.FunderType) == "" {
		return payloadError(event, "grant snapshot cannot build the public coupon detail")
	}
	benefit, err := json.Marshal(snapshot.Benefit)
	if err != nil {
		return payloadError(event, "benefit snapshot cannot be encoded")
	}
	detailDocument := readmodel.CouponDetailDocument{
		UserCouponID: value.UserCouponID, CampaignID: value.CampaignID, DisplayName: snapshot.DisplayName,
		Benefit: snapshot.Benefit, Status: readmodel.WalletStatusAvailable, UsableFrom: value.UsableFrom,
		ExpiresAt: value.ExpiresAt, PolicyVersion: value.PolicyVersion, Applicability: snapshot.Applicability,
		IssuerAndFunding: snapshot.IssuerAndFunding,
	}
	detail, err := json.Marshal(detailDocument)
	if err != nil {
		return payloadError(event, "coupon detail cannot be encoded")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rm_user_coupon_wallet (
		user_id,user_coupon_id,campaign_id,display_name,benefit,display_status,usable_from,expires_at,
		last_event_id,projection_version,updated_at
	) VALUES ($1,$2,$3,$4,$5,'available',$6,$7,$8,$9,$10)
	ON CONFLICT (user_id,user_coupon_id) DO UPDATE SET campaign_id=EXCLUDED.campaign_id,
		display_name=EXCLUDED.display_name,benefit=EXCLUDED.benefit,display_status=EXCLUDED.display_status,
		usable_from=EXCLUDED.usable_from,expires_at=EXCLUDED.expires_at,last_event_id=EXCLUDED.last_event_id,
		projection_version=GREATEST(rm_user_coupon_wallet.projection_version+1,EXCLUDED.projection_version),
		updated_at=EXCLUDED.updated_at WHERE rm_user_coupon_wallet.updated_at <= EXCLUDED.updated_at`,
		value.UserID, value.UserCouponID, value.CampaignID, snapshot.DisplayName, benefit,
		value.UsableFrom, value.ExpiresAt, event.EventID, event.AggregateVersion, event.OccurredAt); err != nil {
		return projectorError("upsert_wallet", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rm_coupon_details (
		user_coupon_id,user_id,campaign_id,policy_version,detail,last_event_id,projection_version,updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	ON CONFLICT (user_coupon_id) DO UPDATE SET user_id=EXCLUDED.user_id,campaign_id=EXCLUDED.campaign_id,
		policy_version=EXCLUDED.policy_version,detail=EXCLUDED.detail,last_event_id=EXCLUDED.last_event_id,
		projection_version=GREATEST(rm_coupon_details.projection_version+1,EXCLUDED.projection_version),
		updated_at=EXCLUDED.updated_at WHERE rm_coupon_details.updated_at <= EXCLUDED.updated_at`,
		value.UserCouponID, value.UserID, value.CampaignID, value.PolicyVersion, detail,
		event.EventID, event.AggregateVersion, event.OccurredAt); err != nil {
		return projectorError("upsert_coupon_detail", err)
	}
	if err := addPerformance(ctx, tx, event, value.CampaignID, performanceDelta{Issued: 1}); err != nil {
		return err
	}
	if err := insertTimeline(ctx, tx, event, value.UserID, value.UserCouponID,
		stringResultRef(value.ResultRef, "user_coupon", value.UserCouponID)); err != nil {
		return err
	}
	return upsertIncident(ctx, tx, event, readmodel.SignalIssuance, value.CampaignID, "normal", map[string]int64{"issued": 1})
}

func projectRedemptionEvent(ctx context.Context, tx pgx.Tx, event policy.Envelope) error {
	value, err := decodeEventData[redemptionPayload](event)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value.RedemptionID) == "" || strings.TrimSpace(value.UserCouponID) == "" ||
		strings.TrimSpace(value.CampaignID) == "" || strings.TrimSpace(value.UserID) == "" ||
		!validExternalRef(value.OrderRef) || !validExternalRef(value.ResultRef) || value.PolicyVersion < 1 {
		return payloadError(event, "redemption projection fields are incomplete")
	}
	if event.EventDocumentID == "EVT.A.19-28" {
		return insertCostAttribution(ctx, tx, event, value)
	}
	if err := insertTimeline(ctx, tx, event, value.UserID, value.UserCouponID, value.ResultRef); err != nil {
		return err
	}
	delta := performanceDelta{}
	var nextStatus readmodel.WalletStatus
	switch event.EventDocumentID {
	case "EVT.A.19-21":
		nextStatus = readmodel.WalletStatusReserved
		delta.Reserved = 1
	case "EVT.A.19-22":
		nextStatus = readmodel.WalletStatusUsed
		delta.Confirmed = 1
		delta.ConfirmedAmount = value.Discount.Amount
		delta.Currency = value.Discount.Currency
	case "EVT.A.19-23":
		nextStatus = readmodel.WalletStatusAvailable
		delta.Released = 1
	case "EVT.A.19-24":
		nextStatus = readmodel.WalletStatusReclaimed
		delta.Reclaimed = 1
		delta.ReclaimedAmount = value.Discount.Amount
		delta.Currency = value.Discount.Currency
	}
	if nextStatus != "" {
		if err := updateCouponStatus(ctx, tx, event, value.UserID, value.UserCouponID, nextStatus); err != nil {
			return err
		}
		if err := addPerformance(ctx, tx, event, value.CampaignID, delta); err != nil {
			return err
		}
	}
	return upsertIncident(ctx, tx, event, readmodel.SignalRedemption, value.CampaignID, "normal",
		map[string]int64{strings.TrimPrefix(event.EventDocumentID, "EVT.A.19-"): 1})
}

func projectBulkFailure(ctx context.Context, tx pgx.Tx, event policy.Envelope) error {
	value, err := decodeEventData[bulkPayload](event)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value.BulkJobID) == "" || strings.TrimSpace(value.CampaignID) == "" {
		return payloadError(event, "bulk job identity is incomplete")
	}
	if err := upsertFailure(ctx, tx, event, "bulk:"+value.BulkJobID, value.Status,
		"bulk_job:"+value.BulkJobID, "bulk_job:"+value.BulkJobID, "COUPON_BULK_COMPLETED_WITH_FAILURES", 1, nil); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE rm_coupon_failures SET failure_kind='bulk' WHERE failure_id=$1`, "bulk:"+value.BulkJobID); err != nil {
		return projectorError("classify_bulk_failure", err)
	}
	return upsertIncident(ctx, tx, event, readmodel.SignalIssuance, value.CampaignID, "degraded",
		map[string]int64{"bulk_rejected": value.RejectedCount, "bulk_failed": value.FailedCount})
}

func projectRecoveryEvent(ctx context.Context, tx pgx.Tx, event policy.Envelope) error {
	value, err := decodeEventData[recoveryPayload](event)
	if err != nil {
		return err
	}
	if event.EventDocumentID == "EVT.A.19-41" {
		return projectReplayDecision(ctx, tx, event, value)
	}
	if strings.TrimSpace(value.RecoveryID) == "" || strings.TrimSpace(value.RedemptionID) == "" ||
		strings.TrimSpace(value.OriginalOperationType) == "" || strings.TrimSpace(value.OriginalPayloadRef) == "" ||
		strings.TrimSpace(value.BusinessKey) == "" {
		return payloadError(event, "recovery correlation is incomplete")
	}
	failureID := "recovery:" + value.RecoveryID
	if event.EventDocumentID == "EVT.A.19-26" || (event.EventDocumentID == "EVT.A.19-41" && value.ResultKind != "failed") {
		if _, err := tx.Exec(ctx, `UPDATE rm_coupon_failures SET failure_status='completed',last_event_id=$2,
			projection_version=GREATEST(projection_version,$3),updated_at=$4 WHERE failure_id=$1`,
			failureID, event.EventID, event.AggregateVersion, event.OccurredAt); err != nil {
			return projectorError("resolve_recovery_failure", err)
		}
		return upsertIncident(ctx, tx, event, readmodel.SignalRecovery, value.RecoveryID, "normal", map[string]int64{"completed": 1})
	}
	failureCode := value.FailureCode
	if failureCode == "" {
		failureCode = "COUPON_RECOVERY_PENDING"
	}
	status := value.Status
	if status == "" {
		status = "recorded"
	}
	if err := upsertFailure(ctx, tx, event, failureID, status, value.BusinessKey,
		value.OriginalPayloadRef, failureCode, value.AttemptCount, value.NextAttemptAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE rm_coupon_failures SET failure_kind='recovery',
		original_operation_type=$2,current_attempt_id=NULLIF($3,''),result_kind=NULLIF($4,''),result_ref=NULLIF($5,'')
		WHERE failure_id=$1`, failureID, value.OriginalOperationType, value.AttemptID, value.ResultKind, value.ResultRef); err != nil {
		return projectorError("enrich_recovery_failure", err)
	}
	incidentStatus := "degraded"
	if event.EventDocumentID == "EVT.A.19-30" {
		incidentStatus = "critical"
	}
	return upsertIncident(ctx, tx, event, readmodel.SignalRecovery, value.RecoveryID, incidentStatus,
		map[string]int64{"attempts": int64(value.AttemptCount)})
}

func projectReplayDecision(ctx context.Context, tx pgx.Tx, event policy.Envelope, value recoveryPayload) error {
	if strings.TrimSpace(value.RecoveryID) == "" || strings.TrimSpace(value.RedemptionID) == "" ||
		strings.TrimSpace(value.AttemptID) == "" || strings.TrimSpace(value.BusinessKey) == "" {
		return payloadError(event, "replay decision correlation is incomplete")
	}
	status := "completed"
	incidentStatus := "normal"
	metrics := map[string]int64{"replay_completed": 1}
	switch value.ResultKind {
	case "transitioned", "already_applied":
		if strings.TrimSpace(value.ResultRef) == "" {
			return payloadError(event, "successful replay decision requires a result reference")
		}
	case "failed":
		if strings.TrimSpace(value.FailureCode) == "" {
			return payloadError(event, "failed replay decision requires a failure code")
		}
		status = "retry_failed"
		incidentStatus = "degraded"
		metrics = map[string]int64{"replay_failed": 1}
	default:
		return payloadError(event, "replay decision result kind is invalid")
	}
	updated, err := tx.Exec(ctx, `UPDATE rm_coupon_failures SET
		failure_status=$2::varchar,current_attempt_id=$3::text,result_kind=$4::varchar,
		result_ref=NULLIF($5::varchar,''),
		failure_code=CASE WHEN $4::varchar='failed' THEN $6::varchar ELSE failure_code END,next_attempt_at=NULL,
		last_event_id=$7,projection_version=projection_version+1,updated_at=$8
		WHERE failure_id=$1::text AND failure_kind='recovery' AND business_key=$9::varchar`,
		"recovery:"+value.RecoveryID, status, value.AttemptID, value.ResultKind, value.ResultRef,
		value.FailureCode, event.EventID, event.OccurredAt, value.BusinessKey)
	if err != nil {
		return projectorError("apply_replay_decision", err)
	}
	if updated.RowsAffected() != 1 {
		return payloadError(event, "replay decision recovery projection is missing or has different correlation")
	}
	return upsertIncident(ctx, tx, event, readmodel.SignalRecovery, value.RecoveryID, incidentStatus, metrics)
}

func projectNoticeEvent(ctx context.Context, tx pgx.Tx, event policy.Envelope) error {
	value, err := decodeEventData[noticePayload](event)
	if err != nil {
		return err
	}
	if strings.TrimSpace(value.ControlID) == "" || len(value.Scopes) == 0 || strings.TrimSpace(value.Notice.Message) == "" ||
		value.Notice.EffectiveFrom.IsZero() {
		return payloadError(event, "read-only notice is incomplete")
	}
	for _, scope := range value.Scopes {
		if !validNoticeScope(scope.Type, scope.Ref) {
			return payloadError(event, "read-only notice scope is invalid")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO rm_coupon_read_only_notice (
			control_id,scope_type,scope_ref,message,effective_from,active,last_event_id,projection_version,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (control_id,scope_type,scope_ref) DO UPDATE SET message=EXCLUDED.message,
			effective_from=EXCLUDED.effective_from,active=EXCLUDED.active,last_event_id=EXCLUDED.last_event_id,
			projection_version=EXCLUDED.projection_version,updated_at=EXCLUDED.updated_at
			WHERE rm_coupon_read_only_notice.projection_version <= EXCLUDED.projection_version`,
			value.ControlID, scope.Type, scope.Ref, value.Notice.Message, value.Notice.EffectiveFrom,
			value.Notice.Active, event.EventID, event.AggregateVersion, event.OccurredAt); err != nil {
			return projectorError("upsert_read_only_notice", err)
		}
	}
	return nil
}

func projectOperationalSignal(ctx context.Context, tx pgx.Tx, event policy.Envelope) error {
	if event.EventDocumentID == "EVT.A.19-25" {
		value, err := decodeEventData[struct {
			ControlID       string `json:"control_id"`
			BlockIssuance   bool   `json:"block_issuance"`
			BlockRedemption bool   `json:"block_redemption"`
		}](event)
		if err != nil {
			return err
		}
		if strings.TrimSpace(value.ControlID) == "" {
			return payloadError(event, "operational control ID is required")
		}
		if value.BlockIssuance {
			if err := upsertIncident(ctx, tx, event, readmodel.SignalIssuance, value.ControlID, "degraded", map[string]int64{"blocked": 1}); err != nil {
				return err
			}
		}
		if value.BlockRedemption {
			return upsertIncident(ctx, tx, event, readmodel.SignalRedemption, value.ControlID, "degraded", map[string]int64{"blocked": 1})
		}
		return nil
	}
	campaignID := eventString(event, "campaign_id", "campaignId")
	if strings.TrimSpace(campaignID) == "" {
		return payloadError(event, "campaign ID is required for issuance signal")
	}
	return upsertIncident(ctx, tx, event, readmodel.SignalIssuance, campaignID, "normal",
		map[string]int64{strings.TrimPrefix(event.EventDocumentID, "EVT.A.19-"): 1})
}

func addPerformance(ctx context.Context, tx pgx.Tx, event policy.Envelope, campaignID string, delta performanceDelta) error {
	if strings.TrimSpace(campaignID) == "" {
		return payloadError(event, "campaign ID is required for performance projection")
	}
	confirmedAmount := delta.ConfirmedAmount
	if confirmedAmount == "" {
		confirmedAmount = "0"
	}
	reclaimedAmount := delta.ReclaimedAmount
	if reclaimedAmount == "" {
		reclaimedAmount = "0"
	}
	if (delta.ConfirmedAmount != "" || delta.ReclaimedAmount != "") && len(delta.Currency) != 3 {
		return payloadError(event, "discount currency is invalid")
	}
	result, err := tx.Exec(ctx, `INSERT INTO rm_coupon_performance_minutely (
		campaign_id,bucket_at,requested_count,issued_count,rejected_count,failed_count,reserved_count,
		confirmed_count,released_count,reclaimed_count,confirmed_discount_amount,reclaimed_discount_amount,
		currency,last_event_id,projection_version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::numeric,$12::numeric,NULLIF($13,''),$14,$15)
	ON CONFLICT (campaign_id,bucket_at) DO UPDATE SET
		requested_count=rm_coupon_performance_minutely.requested_count+EXCLUDED.requested_count,
		issued_count=rm_coupon_performance_minutely.issued_count+EXCLUDED.issued_count,
		rejected_count=rm_coupon_performance_minutely.rejected_count+EXCLUDED.rejected_count,
		failed_count=rm_coupon_performance_minutely.failed_count+EXCLUDED.failed_count,
		reserved_count=rm_coupon_performance_minutely.reserved_count+EXCLUDED.reserved_count,
		confirmed_count=rm_coupon_performance_minutely.confirmed_count+EXCLUDED.confirmed_count,
		released_count=rm_coupon_performance_minutely.released_count+EXCLUDED.released_count,
		reclaimed_count=rm_coupon_performance_minutely.reclaimed_count+EXCLUDED.reclaimed_count,
		confirmed_discount_amount=rm_coupon_performance_minutely.confirmed_discount_amount+EXCLUDED.confirmed_discount_amount,
		reclaimed_discount_amount=rm_coupon_performance_minutely.reclaimed_discount_amount+EXCLUDED.reclaimed_discount_amount,
		currency=COALESCE(rm_coupon_performance_minutely.currency,EXCLUDED.currency),
		last_event_id=EXCLUDED.last_event_id,
		projection_version=rm_coupon_performance_minutely.projection_version+1
	WHERE rm_coupon_performance_minutely.currency IS NULL OR EXCLUDED.currency IS NULL OR
		rm_coupon_performance_minutely.currency=EXCLUDED.currency`,
		campaignID, event.OccurredAt.UTC().Truncate(time.Minute), delta.Requested, delta.Issued, delta.Rejected,
		delta.Failed, delta.Reserved, delta.Confirmed, delta.Released, delta.Reclaimed, confirmedAmount,
		reclaimedAmount, delta.Currency, event.EventID, event.AggregateVersion)
	if err != nil {
		return projectorError("upsert_performance", err)
	}
	if result.RowsAffected() != 1 {
		return oops.In("coupon_read_model_projector").Code("coupon.performance_currency_conflict").New("performance bucket contains a different currency")
	}
	return nil
}

func updateCouponStatus(ctx context.Context, tx pgx.Tx, event policy.Envelope, userID, userCouponID string, status readmodel.WalletStatus) error {
	if !status.Valid() {
		return payloadError(event, "wallet status is invalid")
	}
	updated, err := tx.Exec(ctx, `UPDATE rm_user_coupon_wallet SET display_status=$3,last_event_id=$4,
		projection_version=projection_version+1,updated_at=$5
		WHERE user_id=$1 AND user_coupon_id=$2 AND updated_at <= $5`, userID, userCouponID, status, event.EventID, event.OccurredAt)
	if err != nil {
		return projectorError("update_wallet_status", err)
	}
	if updated.RowsAffected() == 0 {
		exists, err := walletProjectionExists(ctx, tx, userID, userCouponID)
		if err != nil {
			return err
		}
		if !exists {
			return oops.In("coupon_read_model_projector").Code("coupon.wallet_projection_missing").New("wallet projection must exist before a redemption event")
		}
	}
	detail, err := tx.Exec(ctx, `UPDATE rm_coupon_details SET detail=jsonb_set(detail,'{status}',to_jsonb($3::text)),
		last_event_id=$4,projection_version=projection_version+1,updated_at=$5
		WHERE user_id=$1 AND user_coupon_id=$2 AND updated_at <= $5`, userID, userCouponID, status, event.EventID, event.OccurredAt)
	if err != nil {
		return projectorError("update_detail_status", err)
	}
	if detail.RowsAffected() == 0 {
		exists, err := detailProjectionExists(ctx, tx, userID, userCouponID)
		if err != nil {
			return err
		}
		if !exists {
			return oops.In("coupon_read_model_projector").Code("coupon.detail_projection_missing").New("coupon detail projection must exist before a status event")
		}
	}
	return nil
}

func insertTimeline(ctx context.Context, tx pgx.Tx, event policy.Envelope, userID, userCouponID string, resultRef readmodel.ExternalRef) error {
	if strings.TrimSpace(userID) == "" || !validExternalRef(resultRef) {
		return payloadError(event, "timeline user and result reference are required")
	}
	encoded, err := json.Marshal(resultRef)
	if err != nil {
		return payloadError(event, "timeline result reference cannot be encoded")
	}
	_, err = tx.Exec(ctx, `INSERT INTO rm_user_coupon_timeline (
		timeline_id,user_id,user_coupon_id,event_type,result_ref,occurred_at,last_event_id,projection_version
	) VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$1,$7) ON CONFLICT (last_event_id) DO NOTHING`,
		event.EventID, userID, userCouponID, event.EventDocumentID, encoded, event.OccurredAt, event.AggregateVersion)
	if err != nil {
		return projectorError("insert_timeline", err)
	}
	return nil
}

func upsertFailure(ctx context.Context, tx pgx.Tx, event policy.Envelope, failureID, status, businessKey,
	sourceRef, failureCode string, attemptCount int, nextAttemptAt *time.Time) error {
	if strings.TrimSpace(failureID) == "" || strings.TrimSpace(status) == "" || strings.TrimSpace(businessKey) == "" ||
		strings.TrimSpace(sourceRef) == "" || strings.TrimSpace(failureCode) == "" || attemptCount < 0 {
		return payloadError(event, "failure projection fields are incomplete")
	}
	_, err := tx.Exec(ctx, `INSERT INTO rm_coupon_failures (
		failure_id,failure_status,business_key,source_ref,failure_code,attempt_count,next_attempt_at,
		last_event_id,projection_version,updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	ON CONFLICT (failure_id) DO UPDATE SET failure_status=EXCLUDED.failure_status,
		business_key=EXCLUDED.business_key,source_ref=EXCLUDED.source_ref,failure_code=EXCLUDED.failure_code,
		attempt_count=EXCLUDED.attempt_count,next_attempt_at=EXCLUDED.next_attempt_at,last_event_id=EXCLUDED.last_event_id,
		projection_version=EXCLUDED.projection_version,updated_at=EXCLUDED.updated_at
		WHERE rm_coupon_failures.projection_version <= EXCLUDED.projection_version`,
		failureID, status, businessKey, sourceRef, failureCode, attemptCount, nextAttemptAt,
		event.EventID, event.AggregateVersion, event.OccurredAt)
	if err != nil {
		return projectorError("upsert_failure", err)
	}
	return nil
}

func upsertIncident(ctx context.Context, tx pgx.Tx, event policy.Envelope, signal readmodel.SignalName,
	scopeRef, status string, metrics map[string]int64) error {
	if strings.TrimSpace(scopeRef) == "" || len(scopeRef) > 200 ||
		(status != "normal" && status != "degraded" && status != "critical") {
		return payloadError(event, "incident projection fields are invalid")
	}
	encoded, err := json.Marshal(metrics)
	if err != nil {
		return payloadError(event, "incident metrics cannot be encoded")
	}
	incidentKey := string(signal) + ":" + scopeRef
	_, err = tx.Exec(ctx, `INSERT INTO rm_coupon_incident_status (
		incident_key,scope_type,scope_ref,status,business_metrics,observed_at,last_event_id,projection_version
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	ON CONFLICT (incident_key) DO UPDATE SET status=EXCLUDED.status,
		business_metrics=rm_coupon_incident_status.business_metrics || EXCLUDED.business_metrics,
		observed_at=EXCLUDED.observed_at,last_event_id=EXCLUDED.last_event_id,
		projection_version=rm_coupon_incident_status.projection_version+1
		WHERE rm_coupon_incident_status.observed_at <= EXCLUDED.observed_at`,
		incidentKey, signal, scopeRef, status, encoded, event.OccurredAt, event.EventID, event.AggregateVersion)
	if err != nil {
		return projectorError("upsert_incident", err)
	}
	return nil
}

func insertCostAttribution(ctx context.Context, tx pgx.Tx, event policy.Envelope, value redemptionPayload) error {
	if value.Discount.Amount == "" || len(value.Discount.Currency) != 3 || len(value.CostShares) == 0 {
		return payloadError(event, "cost attribution amount and shares are required")
	}
	kind := readmodel.CostAttributionKind(value.Status)
	if kind != readmodel.CostAttributionConfirmed && kind != readmodel.CostAttributionReclaimed {
		return payloadError(event, "cost attribution kind is invalid")
	}
	shares, err := json.Marshal(value.CostShares)
	if err != nil {
		return payloadError(event, "cost shares cannot be encoded")
	}
	_, err = tx.Exec(ctx, `INSERT INTO rm_coupon_cost_attribution (
		attribution_id,order_id,redemption_id,campaign_id,kind,discount_amount,currency,cost_shares,
		settlement_ref,occurred_at,last_event_id,projection_version
	) VALUES ($1,$2,$3,$4,$5,$6::numeric,$7,$8,NULL,$9,$1,$10)
	ON CONFLICT (attribution_id) DO NOTHING`, event.EventID, value.OrderRef.ID, value.RedemptionID,
		value.CampaignID, kind, value.Discount.Amount, value.Discount.Currency, shares, event.OccurredAt, event.AggregateVersion)
	if err != nil {
		return projectorError("insert_cost_attribution", err)
	}
	return nil
}

func walletProjectionExists(ctx context.Context, tx pgx.Tx, userID, userCouponID string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rm_user_coupon_wallet WHERE user_id=$1 AND user_coupon_id=$2)`, userID, userCouponID).Scan(&exists); err != nil {
		return false, projectorError("check_wallet_projection", err)
	}
	return exists, nil
}

func detailProjectionExists(ctx context.Context, tx pgx.Tx, userID, userCouponID string) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rm_coupon_details WHERE user_id=$1 AND user_coupon_id=$2)`, userID, userCouponID).Scan(&exists); err != nil {
		return false, projectorError("check_detail_projection", err)
	}
	return exists, nil
}

func stringResultRef(value, kind, fallbackID string) readmodel.ExternalRef {
	if strings.TrimSpace(value) == "" {
		value = fallbackID
	}
	return readmodel.ExternalRef{Context: "coupon", Type: kind, ID: value}
}

func validExternalRef(value readmodel.ExternalRef) bool {
	return strings.TrimSpace(value.Context) != "" && len(value.Context) <= 64 &&
		strings.TrimSpace(value.Type) != "" && len(value.Type) <= 64 &&
		strings.TrimSpace(value.ID) != "" && len(value.ID) <= 200
}

func validNoticeScope(scopeType, scopeRef string) bool {
	if strings.TrimSpace(scopeRef) == "" || len(scopeRef) > 200 {
		return false
	}
	switch scopeType {
	case "campaign", "drop", "user_group":
		return true
	default:
		return false
	}
}

func eventString(event policy.Envelope, keys ...string) string {
	for _, key := range keys {
		if value, ok := event.Data[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func payloadError(event policy.Envelope, message string) error {
	return oops.In("coupon_read_model_projector").Code("coupon.projector_payload_invalid").
		With("event_document_id", event.EventDocumentID, "aggregate_id", event.AggregateID).
		New(message)
}
