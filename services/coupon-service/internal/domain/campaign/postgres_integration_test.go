//go:build integration

package campaign_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	campaignapp "github.com/Medikong/services/services/coupon-service/internal/application/campaign"
	issuanceapp "github.com/Medikong/services/services/coupon-service/internal/application/issuance"
	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/application/projection"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/couponcode"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/readmodel"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
	"github.com/google/uuid"
)

func TestIssuanceAggregatesPersistLedgerIdempotencyAndOutbox(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("coupon_service"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, migration.Migrate(ctx, pool))

	now := time.Now().UTC().Truncate(time.Microsecond)
	campaignRepo, err := campaign.NewPostgresRepository(pool)
	require.NoError(t, err)
	application, err := campaignapp.New(campaignapp.Dependencies{Repository: campaignRepo, SellerSnapshots: integrationSellerSnapshots{}, Approvals: integrationApprovals{}})
	require.NoError(t, err)
	applicationResult, err := application.RegisterPolicy(ctx, campaignapp.RegisterPolicyInput{
		Metadata: campaignapp.CommandMetadata{
			CommandID: "command-app-register", BusinessKey: "app-register", CorrelationID: "corr:app-register",
			ApprovalRef: "approval-app-register", OccurredAt: now,
			LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
		},
		DisplayName: "Application ID Contract", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
		Benefits: []campaign.Benefit{{Type: campaign.BenefitFixedAmount, Amount: &shared.Money{Amount: "1000", Currency: "KRW"}, Currency: "KRW"}},
		Applicability: []campaign.ApplicabilityPolicy{{
			TargetType: "drop", TargetRef: "drop_abcdefgh", Inclusion: "include", ConditionType: "all",
			ConditionValue: []byte(`{"schemaVersion":1}`),
		}},
		IssuerAndFunding: shared.IssuerAndFunding{IssuerType: "platform", IssuerRef: shared.ExternalRef{Context: "coupon", Type: "platform", ID: "platform"}, FunderType: "platform"},
		OwnerSnapshot:    shared.SnapshotRef{SourceRef: shared.ExternalRef{Context: "catalog", Type: "drop", ID: "drop_abcdefgh"}, SourceVersion: "1", CapturedAt: now, PayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		ApprovalPolicy:   shared.SnapshotRef{SourceRef: shared.ExternalRef{Context: "operations", Type: "approval_policy", ID: "coupon-campaign-v1"}, SourceVersion: "1", CapturedAt: now, PayloadHash: "sha256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"},
	})
	require.NoError(t, err)
	require.Regexp(t, `^camp_[A-Za-z0-9_-]{8,120}$`, applicationResult.CampaignID)
	var storedApplicationCampaignID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT campaign_id FROM coupon_campaigns WHERE campaign_id=$1`, applicationResult.CampaignID).Scan(&storedApplicationCampaignID))
	require.Equal(t, applicationResult.CampaignID, storedApplicationCampaignID)
	_, err = application.ChangePolicy(ctx, campaignapp.ChangePolicyInput{
		Metadata: campaignapp.CommandMetadata{
			CommandID: "command-app-change", BusinessKey: "app-change", CorrelationID: "corr:app-change",
			ApprovalRef: "approval-app-change", OccurredAt: now,
			LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
		},
		CampaignID: applicationResult.CampaignID, ExpectedVersion: 0, EffectiveAt: now.Add(30 * time.Minute),
		Benefits: []campaign.Benefit{{Type: campaign.BenefitFixedAmount, Amount: &shared.Money{Amount: "2000", Currency: "KRW"}, Currency: "KRW"}},
		Applicability: []campaign.ApplicabilityPolicy{{
			TargetType: "drop", TargetRef: "drop_abcdefgh", Inclusion: "include", ConditionType: "all",
			ConditionValue: []byte(`{"schemaVersion":1}`),
		}},
	})
	require.NoError(t, err)
	changedApplicationCampaign, err := campaignRepo.Get(ctx, applicationResult.CampaignID)
	require.NoError(t, err)
	require.NotNil(t, changedApplicationCampaign.IssuerAndFunding.ApprovalPolicy)
	require.Equal(t, "coupon-campaign-v1", changedApplicationCampaign.IssuerAndFunding.ApprovalPolicy.SourceRef.ID)
	require.Equal(t, int64(2), changedApplicationCampaign.CurrentPolicyVersion)
	require.Len(t, changedApplicationCampaign.Benefits, 1)
	require.Len(t, changedApplicationCampaign.Applicability, 1)
	require.Equal(t, int64(2), changedApplicationCampaign.Benefits[0].PolicyVersion)
	require.Equal(t, int64(2), changedApplicationCampaign.Applicability[0].PolicyVersion)
	effectiveBeforeChange, err := campaignRepo.GetEffective(ctx, applicationResult.CampaignID, now.Add(15*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(1), effectiveBeforeChange.CurrentPolicyVersion)
	require.Equal(t, "1000.0000", effectiveBeforeChange.Benefits[0].Amount.Amount)
	effectiveAfterChange, err := campaignRepo.GetEffective(ctx, applicationResult.CampaignID, now.Add(31*time.Minute))
	require.NoError(t, err)
	require.Equal(t, int64(2), effectiveAfterChange.CurrentPolicyVersion)
	require.Equal(t, "2000.0000", effectiveAfterChange.Benefits[0].Amount.Amount)

	c := campaign.Campaign{
		ID: "camp_abcdefgh", DisplayName: "Launch", Status: campaign.StatusActive,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), ClaimStartsAt: timePointer(now.Add(-time.Hour)), ClaimEndsAt: timePointer(now.Add(time.Hour)),
		CurrentPolicyVersion: 1, TotalQuantity: 2, PerUserLimit: 1, Version: 0,
		IssuerAndFunding: shared.IssuerAndFunding{IssuerType: "platform", IssuerRef: shared.ExternalRef{Context: "coupon", Type: "platform", ID: "platform"}, FunderType: "platform"},
		OwnerSnapshot:    shared.SnapshotRef{SourceRef: shared.ExternalRef{Context: "catalog", Type: "drop", ID: "drop_abcdefgh"}, SourceVersion: "1", CapturedAt: now, PayloadHash: "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		Benefits:         []campaign.Benefit{{ID: "benefit-1", PolicyVersion: 1, Type: campaign.BenefitFixedAmount, Amount: &shared.Money{Amount: "1000", Currency: "KRW"}, Currency: "KRW"}},
		Applicability:    []campaign.ApplicabilityPolicy{{ID: "policy-1", PolicyVersion: 1, TargetType: "drop", TargetRef: "drop_abcdefgh", Inclusion: "include", ConditionType: "all", ConditionValue: []byte(`{"schemaVersion":1}`), EffectiveFrom: now, SnapshotLabel: "v1"}},
	}
	_, err = campaignRepo.Create(ctx, c, campaignCommand("CMD.A.19-01", "create-campaign", now))
	require.NoError(t, err)
	reserveCommand := campaignCommand("CMD.A.19-26", "reserve-quantity", now)
	reserved, err := campaignRepo.ReserveQuantity(ctx, c.ID, "ireq_quantity1", 1, 0, now, reserveCommand)
	require.NoError(t, err)
	require.Equal(t, campaign.ReservationReserved, reserved.Reservation.State)
	reservedReplay, err := campaignRepo.ReserveQuantity(ctx, c.ID, "ireq_quantity1", 1, 0, now, reserveCommand)
	require.NoError(t, err)
	require.True(t, reservedReplay.Replayed)
	require.Equal(t, campaign.ReservationReserved, reservedReplay.Reservation.State)
	confirmed, err := campaignRepo.ConfirmQuantity(ctx, c.ID, "ireq_quantity1", 1, campaignCommand("CMD.A.19-27", "confirm-quantity", now.Add(time.Second)))
	require.NoError(t, err)
	require.Equal(t, campaign.ReservationConfirmed, confirmed.Reservation.State)
	_, err = pool.Exec(ctx, `INSERT INTO coupon_operational_controls (
		control_id,active,effective_from,block_issuance,block_redemption,operation_request_ref,approval_ref,version
	) VALUES ('ctrl_quantity01',true,$1,true,false,'operation:quantity-stop','approval:quantity-stop',0)`, now.Add(time.Second))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO coupon_operational_scopes (control_id,scope_type,scope_ref)
		VALUES ('ctrl_quantity01','campaign',$1)`, c.ID)
	require.NoError(t, err)
	blocked, err := campaignRepo.ReserveQuantity(ctx, c.ID, "ireq_blocked01", 1, confirmed.Version, now.Add(2*time.Second), campaignCommand("CMD.A.19-26", "blocked-quantity", now.Add(2*time.Second)))
	require.NoError(t, err)
	require.True(t, blocked.Rejected)
	require.Equal(t, "issuance_blocked", blocked.ReasonCode)
	_, found, err := campaignRepo.FindQuantityReservation(ctx, c.ID, "ireq_blocked01")
	require.NoError(t, err)
	require.False(t, found)
	quantityCampaign, err := campaignRepo.Get(ctx, c.ID)
	require.NoError(t, err)
	require.Zero(t, quantityCampaign.ReservedQuantity)
	require.Equal(t, int64(1), quantityCampaign.ConfirmedQuantity)
	_, err = campaignRepo.AddPolicyVersion(ctx, c.ID, confirmed.Version, campaign.PolicyVersion{
		Version: 2, EffectiveAt: now.Add(30 * time.Minute),
		Benefits: []campaign.Benefit{{ID: "benefit-future", Type: campaign.BenefitFixedAmount, Amount: &shared.Money{Amount: "2000", Currency: "KRW"}, Currency: "KRW"}},
		Applicability: []campaign.ApplicabilityPolicy{{
			ID: "policy-future", TargetType: "drop", TargetRef: "drop_abcdefgh", Inclusion: "include",
			ConditionType: "all", ConditionValue: []byte(`{"schemaVersion":1}`), SnapshotLabel: "v2",
		}},
		IssuerAndFunding: c.IssuerAndFunding,
	}, campaignCommand("CMD.A.19-04", "future-policy", now.Add(2*time.Second)))
	require.NoError(t, err)

	hash, _, err := couponcode.Fingerprint("ABCD-1234", []byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO coupon_code_batches (code_batch_id,campaign_id,status,format,quantity,created_count,distribution_channel,creator_ref,version) VALUES ('batch-1',$1,'active','XXXX-XXXX',1,1,'partner','operator-1',0)`, c.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO coupon_codes (code_id,code_batch_id,campaign_id,code_hash,hash_version,normalization_version,code_suffix,status,version) VALUES ('code-1','batch-1',$1,$2,1,1,'1234','available',0)`, c.ID, hash)
	require.NoError(t, err)
	codeRepo, err := couponcode.NewPostgresRepository(pool)
	require.NoError(t, err)
	activeLeaseCommand := codeCommand("CMD.A.19-06", "reserve-code-active-lease", now)
	activeLeaseHash := sha256.Sum256([]byte(activeLeaseCommand.RequestHash))
	_, err = pool.Exec(ctx, `INSERT INTO coupon_idempotency_records (operation_type,business_key,owner_type,owner_id,request_hash,status,locked_until,expires_at) VALUES ($1,$2,'CouponCodeBatch','batch-1',$3,'processing',$4,$5)`, activeLeaseCommand.OperationType, activeLeaseCommand.BusinessKey, activeLeaseHash[:], activeLeaseCommand.LeaseUntil, activeLeaseCommand.ExpiresAt)
	require.NoError(t, err)
	_, err = codeRepo.Reserve(ctx, hash, "user-1", "ireq_code0001", now.Add(time.Minute), activeLeaseCommand)
	require.ErrorIs(t, err, couponcode.ErrCommandInProgress)
	_, err = pool.Exec(ctx, `DELETE FROM coupon_idempotency_records WHERE operation_type=$1 AND business_key=$2`, activeLeaseCommand.OperationType, activeLeaseCommand.BusinessKey)
	require.NoError(t, err)
	reserveCodeCommand := codeCommand("CMD.A.19-06", "reserve-code", now)
	requestHash := sha256.Sum256([]byte(reserveCodeCommand.RequestHash))
	_, err = pool.Exec(ctx, `INSERT INTO coupon_idempotency_records (operation_type,business_key,owner_type,owner_id,request_hash,status,locked_until,expires_at) VALUES ($1,$2,'CouponCodeBatch','batch-1',$3,'processing',$4,$5)`, reserveCodeCommand.OperationType, reserveCodeCommand.BusinessKey, requestHash[:], now.Add(-time.Minute), reserveCodeCommand.ExpiresAt)
	require.NoError(t, err)
	codeReserved, err := codeRepo.Reserve(ctx, hash, "user-1", "ireq_code0001", now.Add(time.Minute), reserveCodeCommand)
	require.NoError(t, err)
	require.Equal(t, couponcode.CodeReserved, codeReserved.Code.Status)
	var resumedStatus string
	var resumedLockedUntil *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT status,locked_until FROM coupon_idempotency_records WHERE operation_type=$1 AND business_key=$2`, reserveCodeCommand.OperationType, reserveCodeCommand.BusinessKey).Scan(&resumedStatus, &resumedLockedUntil))
	require.Equal(t, "completed", resumedStatus)
	require.Nil(t, resumedLockedUntil)
	var codeReservationPayload []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT payload FROM domain_outbox WHERE event_document_id='EVT.A.19-12' AND aggregate_id='batch-1'`).Scan(&codeReservationPayload))
	var codeReservationEvent map[string]any
	require.NoError(t, json.Unmarshal(codeReservationPayload, &codeReservationEvent))
	require.Equal(t, "user-1", codeReservationEvent["userId"])
	require.Equal(t, "ireq_code0001", codeReservationEvent["issueRequestId"])
	codeReplay, err := codeRepo.Reserve(ctx, hash, "user-1", "ireq_code0001", now.Add(time.Minute), reserveCodeCommand)
	require.NoError(t, err)
	require.True(t, codeReplay.Replayed)
	require.Equal(t, couponcode.CodeReserved, codeReplay.Code.Status)
	codeConfirmed, err := codeRepo.Confirm(ctx, "code-1", "ireq_code0001", "ucpn_code0001", 1, codeCommand("CMD.A.19-16", "confirm-code", now.Add(time.Second)))
	require.NoError(t, err)
	require.Equal(t, couponcode.CodeRedeemed, codeConfirmed.Code.Status)

	issueRepo, err := issuerequest.NewPostgresRepository(pool)
	require.NoError(t, err)
	request := issuerequest.Request{
		ID: "ireq_abcdefgh", CampaignID: c.ID, UserID: "user-1", BusinessKey: "claim:user-1", SourceType: issuerequest.SourceClaim,
		SourceRef: "api:claim:1", Status: issuerequest.StatusAccepted, IssuerAndFundingSnapshot: []byte(`{"issuer":"platform"}`), PolicySnapshot: []byte(`{"version":1}`), Version: 0,
	}
	createIssueCommand := issueCommand("CMD.A.19-05", "create-issue", now)
	_, err = issueRepo.Create(ctx, request, issuerequest.Admission{PerUserLimit: 1}, createIssueCommand)
	require.NoError(t, err)
	issueReplay, err := issueRepo.Create(ctx, request, issuerequest.Admission{PerUserLimit: 1}, createIssueCommand)
	require.NoError(t, err)
	require.True(t, issueReplay.Replayed)
	require.Equal(t, issuerequest.StatusAccepted, issueReplay.Request.Status)
	pending, err := issueRepo.MarkPending(ctx, request.ID, 0, issueCommand("CMD.A.19-30", "pending-issue", now.Add(time.Second)))
	require.NoError(t, err)
	processing, err := issueRepo.MarkProcessing(ctx, request.ID, pending.Request.Version, issueCommand("CMD.A.19-07", "process-issue", now.Add(2*time.Second)))
	require.NoError(t, err)

	userCouponRepo, err := usercoupon.NewPostgresRepository(pool)
	require.NoError(t, err)
	grantedCoupon := usercoupon.Coupon{
		ID: "ucpn_abcdefgh", CampaignID: c.ID, PolicyVersion: 1, UserID: request.UserID, IssueRequestID: request.ID, Status: usercoupon.StatusGranted,
		UsableFrom: now, ExpiresAt: now.Add(time.Hour), GrantSnapshot: []byte(`{"benefit":"fixed"}`), ResultRef: "user_coupon:ucpn_abcdefgh:granted", Version: 0,
	}
	grantCouponCommand := couponCommand("CMD.A.19-07", "grant-user-coupon", now.Add(3*time.Second))
	granted, err := userCouponRepo.Grant(ctx, grantedCoupon, grantCouponCommand)
	require.NoError(t, err)
	require.Equal(t, usercoupon.StatusGranted, granted.Coupon.Status)
	grantReplay, err := userCouponRepo.Grant(ctx, grantedCoupon, grantCouponCommand)
	require.NoError(t, err)
	require.True(t, grantReplay.Replayed)
	require.Equal(t, usercoupon.StatusGranted, grantReplay.Coupon.Status)
	completed, err := issueRepo.Complete(ctx, request.ID, processing.Request.Version, grantedCoupon.ID, issueCommand("CMD.A.19-23", "complete-issue", now.Add(4*time.Second)))
	require.NoError(t, err)
	require.Equal(t, issuerequest.StatusCompleted, completed.Request.Status)
	expired, err := userCouponRepo.Expire(ctx, grantedCoupon.ID, 0, now.Add(2*time.Hour), couponCommand("CMD.A.19-24", "expire-user-coupon", now.Add(2*time.Hour)))
	require.NoError(t, err)
	require.Equal(t, usercoupon.StatusExpired, expired.Coupon.Status)
	require.Equal(t, "user_coupon:ucpn_abcdefgh:expired", expired.Coupon.ResultRef)
	loadedCoupon, err := userCouponRepo.Get(ctx, grantedCoupon.ID)
	require.NoError(t, err)
	require.Equal(t, expired.ResultRef, loadedCoupon.ResultRef)

	issuanceApplication, err := issuanceapp.New(issuanceapp.Dependencies{
		Campaigns: campaignRepo, IssueRequests: issueRepo, Codes: codeRepo, UserCoupons: userCouponRepo,
		Approvals: integrationApprovals{}, Cases: integrationCases{},
		UserEligibility: integrationEligibility{}, OperationalControl: integrationControls{},
		CodeHashKey: []byte("01234567890123456789012345678901"), CodeReservationTTL: time.Minute,
	})
	require.NoError(t, err)
	applicationClaim, err := issuanceApplication.Claim(ctx, issuanceapp.ClaimInput{
		Metadata: issuanceapp.CommandMetadata{
			CommandID: "command-app-claim", BusinessKey: "app-claim", CorrelationID: "corr:app-claim",
			OccurredAt: now, LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
		},
		CampaignID: c.ID, UserID: "user-app",
	})
	require.NoError(t, err)
	require.Regexp(t, `^ireq_[A-Za-z0-9_-]{8,120}$`, applicationClaim.IssueRequestID)
	applicationClaimRequest, err := issueRepo.Get(ctx, applicationClaim.IssueRequestID)
	require.NoError(t, err)
	var policyEnvelope struct {
		Payload json.RawMessage `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(applicationClaimRequest.PolicySnapshot, &policyEnvelope))
	var frozenPolicy struct {
		PolicyVersion int64              `json:"policyVersion"`
		Benefits      []campaign.Benefit `json:"benefits"`
	}
	require.NoError(t, json.Unmarshal(policyEnvelope.Payload, &frozenPolicy))
	require.Equal(t, int64(1), frozenPolicy.PolicyVersion)
	require.Equal(t, "1000.0000", frozenPolicy.Benefits[0].Amount.Amount)
	applicationPending, err := issueRepo.MarkPending(ctx, applicationClaim.IssueRequestID, 0, issueCommand("CMD.A.19-30", "app-pending", now.Add(5*time.Second)))
	require.NoError(t, err)
	applicationProcessing, err := issueRepo.MarkProcessing(ctx, applicationClaim.IssueRequestID, applicationPending.Request.Version, issueCommand("CMD.A.19-07", "app-processing", now.Add(6*time.Second)))
	require.NoError(t, err)
	applicationGrant, err := issuanceApplication.IssueUserCoupon(ctx, issuanceapp.IssueUserCouponInput{
		Metadata: issuanceapp.CommandMetadata{
			CommandID: "command-app-grant", BusinessKey: "app-grant", CorrelationID: "corr:app-grant",
			OccurredAt: now.Add(7 * time.Second), LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
		},
		IssueRequestID: applicationClaim.IssueRequestID, ExpectedIssueRequestVersion: applicationProcessing.Request.Version,
	})
	require.NoError(t, err)
	require.Regexp(t, `^ucpn_[A-Za-z0-9_-]{8,120}$`, applicationGrant.UserCouponID)
	var storedApplicationIssueID, storedApplicationCouponID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT issue_request_id FROM coupon_issue_requests WHERE issue_request_id=$1`, applicationClaim.IssueRequestID).Scan(&storedApplicationIssueID))
	require.NoError(t, pool.QueryRow(ctx, `SELECT user_coupon_id FROM user_coupons WHERE user_coupon_id=$1`, applicationGrant.UserCouponID).Scan(&storedApplicationCouponID))
	require.Equal(t, applicationClaim.IssueRequestID, storedApplicationIssueID)
	require.Equal(t, applicationGrant.UserCouponID, storedApplicationCouponID)
	var issuedEvent policy.Envelope
	var issuedPayload []byte
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT event_id,event_type,event_document_id,aggregate_type,aggregate_id,aggregate_version,
			occurred_at,correlation_id,COALESCE(causation_id,''),payload_schema_version,payload
		FROM domain_outbox WHERE event_document_id='EVT.A.19-09' AND aggregate_id=$1
	`, applicationGrant.UserCouponID).Scan(
		&issuedEvent.EventID, &issuedEvent.EventType, &issuedEvent.EventDocumentID,
		&issuedEvent.AggregateType, &issuedEvent.AggregateID, &issuedEvent.AggregateVersion,
		&issuedEvent.OccurredAt, &issuedEvent.CorrelationID, &issuedEvent.CausationID,
		&issuedEvent.PayloadSchemaVersion, &issuedPayload,
	))
	require.NoError(t, json.Unmarshal(issuedPayload, &issuedEvent.Data))
	require.NotEqual(t, uuid.Nil, issuedEvent.EventID)
	projector, err := projection.New(pool, "issuance-application-integration")
	require.NoError(t, err)
	require.NoError(t, projector.Handle(ctx, issuedEvent))
	readModels, err := readmodel.NewPostgresRepository(pool)
	require.NoError(t, err)
	wallet, err := readModels.ListWallet(ctx, readmodel.WalletQuery{UserID: "user-app", Limit: 10})
	require.NoError(t, err)
	require.Len(t, wallet.Items, 1)
	require.Equal(t, applicationGrant.UserCouponID, wallet.Items[0].UserCouponID)
	require.Equal(t, "Launch", wallet.Items[0].DisplayName)
	detail, err := readModels.GetCouponDetail(ctx, "user-app", applicationGrant.UserCouponID)
	require.NoError(t, err)
	require.Equal(t, "Launch", detail.Document.DisplayName)
	require.Equal(t, "fixed_amount", detail.Document.Benefit.Type)
	_, err = issuanceApplication.Claim(ctx, issuanceapp.ClaimInput{
		Metadata: issuanceapp.CommandMetadata{
			CommandID: "command-app-claim-limit", BusinessKey: "app-claim-limit", CorrelationID: "corr:app-claim-limit",
			OccurredAt: now.Add(8 * time.Second), LeaseUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
		},
		CampaignID: c.ID, UserID: "user-app",
	})
	require.ErrorIs(t, err, issuerequest.ErrPerUserLimitExceeded)
	var wait sync.WaitGroup
	claimErrors := make(chan error, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			at := now.Add(time.Duration(9+index) * time.Second)
			_, claimErr := issuanceApplication.Claim(ctx, issuanceapp.ClaimInput{
				Metadata: issuanceapp.CommandMetadata{
					CommandID: "command-race-" + string(rune('a'+index)), BusinessKey: "race-" + string(rune('a'+index)),
					CorrelationID: "corr:race", OccurredAt: at, LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(24 * time.Hour),
				},
				CampaignID: c.ID, UserID: "user-race",
			})
			claimErrors <- claimErr
		}(index)
	}
	wait.Wait()
	close(claimErrors)
	var admittedClaims, limitedClaims int
	for claimErr := range claimErrors {
		switch {
		case claimErr == nil:
			admittedClaims++
		case errors.Is(claimErr, issuerequest.ErrPerUserLimitExceeded):
			limitedClaims++
		default:
			require.NoError(t, claimErr)
		}
	}
	require.Equal(t, 1, admittedClaims)
	require.Equal(t, 1, limitedClaims)

	assertCount(t, ctx, pool, "coupon_quantity_ledger", 3)
	assertCount(t, ctx, pool, "coupon_issue_ledger", 8)
	assertCount(t, ctx, pool, "user_coupon_ledger", 3)
	assertCount(t, ctx, pool, "coupon_idempotency_records", 20)
	assertCount(t, ctx, pool, "domain_outbox", 19)
}

func campaignCommand(operation, key string, at time.Time) campaign.Command {
	return campaign.Command{OperationType: operation, BusinessKey: key, RequestHash: "sha256:" + key, CorrelationID: "corr:" + key, OccurredAt: at, LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(24 * time.Hour)}
}

func codeCommand(operation, key string, at time.Time) couponcode.Command {
	return couponcode.Command{
		OperationType: operation, BusinessKey: key, RequestHash: "sha256:" + key,
		CorrelationID: "corr:" + key, OccurredAt: at,
		LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(24 * time.Hour),
	}
}

func issueCommand(operation, key string, at time.Time) issuerequest.Command {
	return issuerequest.Command{OperationType: operation, BusinessKey: key, RequestHash: "sha256:" + key, CorrelationID: "corr:" + key, OccurredAt: at, LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(24 * time.Hour)}
}

func couponCommand(operation, key string, at time.Time) usercoupon.Command {
	return usercoupon.Command{OperationType: operation, BusinessKey: key, RequestHash: "sha256:" + key, CorrelationID: "corr:" + key, OccurredAt: at, LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(24 * time.Hour)}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, expected int) {
	t.Helper()
	var actual int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&actual))
	require.Equal(t, expected, actual, table)
}

type integrationApprovals struct{}

func (integrationApprovals) VerifyApproval(context.Context, string, string) error { return nil }

type integrationSellerSnapshots struct{}

func (integrationSellerSnapshots) VerifySellerOwnership(context.Context, shared.SnapshotRef) error {
	return nil
}

type integrationCases struct{}

func (integrationCases) VerifyCase(context.Context, string, ports.CSCaseBinding) error { return nil }

type integrationEligibility struct{}

func (integrationEligibility) Snapshot(_ context.Context, userID string, at time.Time) (ports.UserEligibility, error) {
	return ports.UserEligibility{Eligible: true, Snapshot: shared.SnapshotRef{
		SourceRef:     shared.ExternalRef{Context: "user", Type: "eligibility", ID: userID},
		SourceVersion: "1", CapturedAt: at, PayloadHash: "sha256:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
	}}, nil
}

type integrationControls struct{}

func (integrationControls) FindEffective(context.Context, operations.Scope, time.Time) ([]operations.Control, error) {
	return nil, nil
}
