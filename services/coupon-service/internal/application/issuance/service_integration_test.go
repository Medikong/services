//go:build integration

package issuanceapp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	issuanceapp "github.com/Medikong/services/services/coupon-service/internal/application/issuance"
	"github.com/Medikong/services/services/coupon-service/internal/application/ports"
	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/couponcode"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/operations"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
)

func TestCodeRedemptionRejectsBeforeReservationAndCompensatesIssueRejection(t *testing.T) {
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
	campaignValue := codeCampaign(now)
	_, err = campaignRepo.Create(ctx, campaignValue, campaignCommand(now, "create-code-campaign"))
	require.NoError(t, err)

	codeKey := []byte("01234567890123456789012345678901")
	rejectedHash, _, err := couponcode.Fingerprint("REJECT-0001", codeKey)
	require.NoError(t, err)
	releasedHash, _, err := couponcode.Fingerprint("RELEASE-0002", codeKey)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO coupon_code_batches (
		code_batch_id,campaign_id,status,format,quantity,created_count,distribution_channel,creator_ref,version
	) VALUES
		('batch_reject01',$1,'active','XXXX-XXXX',1,1,'partner','operator-1',0),
		('batch_release01',$1,'active','XXXX-XXXX',1,1,'partner','operator-1',0)`, campaignValue.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO coupon_codes (
		code_id,code_batch_id,campaign_id,code_hash,hash_version,normalization_version,code_suffix,status,version
	) VALUES
		('code-reject','batch_reject01',$1,$2,1,1,'0001','available',0),
		('code-release','batch_release01',$1,$3,1,1,'0002','available',0)`, campaignValue.ID, rejectedHash, releasedHash)
	require.NoError(t, err)

	issueRepo, err := issuerequest.NewPostgresRepository(pool)
	require.NoError(t, err)
	codeRepo, err := couponcode.NewPostgresRepository(pool)
	require.NoError(t, err)
	userRepo, err := usercoupon.NewPostgresRepository(pool)
	require.NoError(t, err)
	controlRepo := operations.NewPostgresRepository(pool)

	ineligible := newCodeIssuanceService(t, campaignRepo, issueRepo, codeRepo, userRepo, controlRepo, false, codeKey)
	rejected, err := ineligible.RedeemCode(ctx, issuanceapp.RedeemCodeInput{
		Metadata: issuanceMetadata(now, "redeem-ineligible"), UserID: "user-ineligible", Code: "REJECT-0001",
	})
	require.NoError(t, err)
	require.True(t, rejected.Rejected)
	require.Equal(t, "user_ineligible", rejected.ReasonCode)
	var rejectedStatus couponcode.CodeStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM coupon_codes WHERE code_id='code-reject'`).Scan(&rejectedStatus))
	require.Equal(t, couponcode.CodeAvailable, rejectedStatus)
	assertEventCount(t, ctx, pool, "EVT.A.19-15", "batch_reject01", 1)

	eligible := newCodeIssuanceService(t, campaignRepo, issueRepo, codeRepo, userRepo, controlRepo, true, codeKey)
	reserved, err := eligible.RedeemCode(ctx, issuanceapp.RedeemCodeInput{
		Metadata: issuanceMetadata(now.Add(time.Second), "redeem-valid"), UserID: "user-valid", Code: "RELEASE-0002",
	})
	require.NoError(t, err)
	require.False(t, reserved.Rejected)
	created, err := eligible.CreateIssueRequest(ctx, issuanceapp.CreateIssueRequestInput{
		Metadata:       issuanceMetadata(now.Add(2*time.Second), "create-code-issue"),
		IssueRequestID: reserved.IssueRequestID, CampaignID: campaignValue.ID, UserID: "user-valid",
		SourceType: issuerequest.SourceRedeemCode, SourceRef: "code-release",
	})
	require.NoError(t, err)
	require.Equal(t, issuerequest.StatusAccepted, created.Status)
	rejectedIssue, err := issueRepo.Reject(ctx, created.IssueRequestID, 0, "campaign_inactive", issueCommand(now.Add(3*time.Second), "reject-code-issue"))
	require.NoError(t, err)
	_, batchVersion, err := codeRepo.FindByIDWithBatchVersion(ctx, "code-release")
	require.NoError(t, err)
	released, err := eligible.ReleaseCode(ctx, issuanceapp.ReleaseCodeInput{
		Metadata: issuanceMetadata(now.Add(4*time.Second), "release-code-reservation"),
		CodeID:   "code-release", IssueRequestID: created.IssueRequestID,
		FailureResultRef: rejectedIssue.ResultRef, ExpectedBatchVersion: batchVersion,
	})
	require.NoError(t, err)
	require.Equal(t, couponcode.CodeAvailable, releasedMutationStatus(t, ctx, pool, released.CodeID))
	assertEventCount(t, ctx, pool, "EVT.A.19-08", created.IssueRequestID, 1)
	assertEventCount(t, ctx, pool, "EVT.A.19-14", "batch_release01", 1)
}

func codeCampaign(now time.Time) campaign.Campaign {
	claimStartsAt := now.Add(-time.Hour)
	claimEndsAt := now.Add(time.Hour)
	return campaign.Campaign{
		ID: "camp_codepolicy1", DisplayName: "Code Campaign", Status: campaign.StatusActive,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
		ClaimStartsAt: &claimStartsAt, ClaimEndsAt: &claimEndsAt,
		CurrentPolicyVersion: 1, TotalQuantity: 10, PerUserLimit: 1,
		IssuerAndFunding: shared.IssuerAndFunding{
			IssuerType: "platform", IssuerRef: shared.ExternalRef{Context: "coupon", Type: "platform", ID: "platform"},
			FunderType: "platform",
		},
		OwnerSnapshot: shared.SnapshotRef{
			SourceRef:     shared.ExternalRef{Context: "catalog", Type: "drop", ID: "drop_codepolicy1"},
			SourceVersion: "1", CapturedAt: now, PayloadHash: "sha256:" + strings.Repeat("a", 43),
		},
		Benefits: []campaign.Benefit{{
			ID: "benefit-code", PolicyVersion: 1, Type: campaign.BenefitFixedAmount,
			Amount: &shared.Money{Amount: "1000", Currency: "KRW"}, Currency: "KRW",
		}},
		Applicability: []campaign.ApplicabilityPolicy{{
			ID: "policy-code", PolicyVersion: 1, TargetType: "drop", TargetRef: "drop_codepolicy1",
			Inclusion: "include", ConditionType: "all", ConditionValue: []byte(`{"schemaVersion":1}`),
			EffectiveFrom: now.Add(-time.Hour), SnapshotLabel: "v1",
		}},
	}
}

func newCodeIssuanceService(t *testing.T, campaigns issuanceapp.CampaignReader, issues issuerequest.Repository, codes couponcode.Repository, users usercoupon.Repository, controls issuanceapp.OperationalControlReader, eligible bool, codeKey []byte) *issuanceapp.Service {
	t.Helper()
	service, err := issuanceapp.New(issuanceapp.Dependencies{
		Campaigns: campaigns, IssueRequests: issues, Codes: codes, UserCoupons: users,
		Approvals: codeApprovalPort{}, Cases: codeCasePort{}, UserEligibility: codeEligibilityPort{eligible: eligible},
		OperationalControl: controls, CodeHashKey: codeKey, CodeReservationTTL: 5 * time.Minute,
	})
	require.NoError(t, err)
	return service
}

type codeApprovalPort struct{}

func (codeApprovalPort) VerifyApproval(context.Context, string, string) error { return nil }

type codeCasePort struct{}

func (codeCasePort) VerifyCase(context.Context, string, ports.CSCaseBinding) error { return nil }

type codeEligibilityPort struct{ eligible bool }

func (p codeEligibilityPort) Snapshot(_ context.Context, userID string, at time.Time) (ports.UserEligibility, error) {
	return ports.UserEligibility{Eligible: p.eligible, Snapshot: shared.SnapshotRef{
		SourceRef:     shared.ExternalRef{Context: "user", Type: "eligibility", ID: userID},
		SourceVersion: "1", CapturedAt: at, PayloadHash: "sha256:" + strings.Repeat("b", 43),
	}}, nil
}

func campaignCommand(at time.Time, key string) campaign.Command {
	return campaign.Command{OperationType: "CMD.A.19-01", BusinessKey: key, RequestHash: "sha256:" + key,
		CorrelationID: "corr:" + key, OccurredAt: at, LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(time.Hour)}
}

func issueCommand(at time.Time, key string) issuerequest.Command {
	return issuerequest.Command{OperationType: "CMD.A.19-29", BusinessKey: key, RequestHash: "sha256:" + key,
		CorrelationID: "corr:" + key, OccurredAt: at, LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(time.Hour)}
}

func issuanceMetadata(at time.Time, key string) issuanceapp.CommandMetadata {
	return issuanceapp.CommandMetadata{CommandID: key, BusinessKey: key, CorrelationID: "corr:" + key,
		OccurredAt: at, LeaseUntil: at.Add(time.Minute), ExpiresAt: at.Add(time.Hour)}
}

func releasedMutationStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, codeID string) couponcode.CodeStatus {
	t.Helper()
	var status couponcode.CodeStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM coupon_codes WHERE code_id=$1`, codeID).Scan(&status))
	return status
}

func assertEventCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, documentID, aggregateID string, expected int) {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM domain_outbox WHERE event_document_id=$1 AND aggregate_id=$2`, documentID, aggregateID).Scan(&count))
	require.Equal(t, expected, count)
}
