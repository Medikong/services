package jobs

import (
	"context"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/bulk"
	"github.com/Medikong/services/services/coupon-service/internal/domain/issuerequest"
	"github.com/Medikong/services/services/coupon-service/internal/domain/recovery"
	"github.com/Medikong/services/services/coupon-service/internal/domain/usercoupon"
)

type BulkLease struct {
	Job                bulk.Job
	Cursor             string
	PageNumber         int64
	PlannedTargetCount int64
}

type BulkTarget struct {
	UserID         string
	BusinessKey    string
	IssueRequestID string
}

type BulkPageCommit struct {
	BulkJobID          string
	ExpectedVersion    int64
	WorkerID           string
	CurrentCursor      string
	NextCursor         string
	PageNumber         int64
	PlannedTargetCount int64
	Targets            []BulkTarget
	Finished           bool
	OccurredAt         time.Time
}

type BulkStore interface {
	ClaimBulkJobs(context.Context, string, time.Time, int, time.Duration) ([]BulkLease, error)
	CommitBulkPage(context.Context, BulkPageCommit) (int64, error)
	FailBulkJob(context.Context, string, string, time.Time, Failure, time.Time, bool) error
}

type IssueLease struct {
	Request              issuerequest.Request
	ExistingUserCouponID string
	ExistingResultRef    string
	WorkerAttempt        int
}

type IssueStore interface {
	ClaimIssueRequests(context.Context, string, time.Time, int, time.Duration) ([]IssueLease, error)
	EnqueueIssueCommand(context.Context, IssueLease, time.Time) error
	FailIssueRequest(context.Context, IssueLease, string, time.Time, Failure, time.Time, bool) error
}

type RecoveryLease struct {
	Recovery      recovery.Recovery
	WorkerAttempt int
}

type RecoveryStore interface {
	ClaimRecoveries(context.Context, string, time.Time, int, time.Duration) ([]RecoveryLease, error)
	FailRecovery(context.Context, RecoveryLease, string, time.Time, Failure, time.Time, bool) error
}

type ExpiryLease struct {
	Coupon  usercoupon.Coupon
	Attempt int
}

type ExpiryStore interface {
	ClaimExpirations(context.Context, string, time.Time, int, time.Duration) ([]ExpiryLease, error)
	CompleteExpiration(context.Context, string, string, time.Time) error
	FailExpiration(context.Context, ExpiryLease, string, time.Time, Failure, time.Time, bool) error
}
