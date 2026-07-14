package bulk

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	"github.com/google/uuid"
	"github.com/samber/oops"
)

type Status string

const (
	StatusRegistered            Status = "registered"
	StatusRunning               Status = "running"
	StatusCompleted             Status = "completed"
	StatusCompletedWithFailures Status = "completed_with_failures"
	StatusFailed                Status = "failed"
)

var bulkJobIDPattern = regexp.MustCompile(`^bjob_[A-Za-z0-9_-]{8,120}$`)

type Job struct {
	ID                    string             `json:"bulkJobId"`
	CampaignID            string             `json:"campaignId"`
	OwnerServiceID        string             `json:"ownerServiceId"`
	AudienceDefinitionRef string             `json:"audienceDefinitionRef"`
	AudienceSnapshot      shared.SnapshotRef `json:"audienceSnapshot"`
	EvaluationAsOf        time.Time          `json:"evaluationAsOf"`
	Status                Status             `json:"status"`
	PlanningComplete      bool               `json:"planningComplete"`
	TargetCount           int64              `json:"targetCount"`
	SucceededCount        int64              `json:"succeededCount"`
	RejectedCount         int64              `json:"rejectedCount"`
	FailedCount           int64              `json:"failedCount"`
	OperationRequestRef   string             `json:"operationRequestRef"`
	ApprovalRef           string             `json:"approvalRef"`
	LeaseOwner            string             `json:"leaseOwner,omitempty"`
	LeaseUntil            *time.Time         `json:"leaseUntil,omitempty"`
	NextAttemptAt         *time.Time         `json:"nextAttemptAt,omitempty"`
	AttemptCount          int                `json:"attemptCount"`
	Version               int64              `json:"version"`
	CreatedAt             time.Time          `json:"createdAt"`
	UpdatedAt             time.Time          `json:"updatedAt"`
}

type Registration struct {
	JobID               string
	CampaignID          string
	OwnerServiceID      string
	AudienceSnapshot    shared.SnapshotRef
	EvaluationAsOf      time.Time
	OperationRequestRef string
	ApprovalRef         string
	CreatedAt           time.Time
}

func Register(input Registration) (Job, reliability.Event, error) {
	job := Job{
		ID: input.JobID, CampaignID: input.CampaignID, OwnerServiceID: input.OwnerServiceID,
		AudienceDefinitionRef: input.AudienceSnapshot.SourceRef.ID,
		AudienceSnapshot:      input.AudienceSnapshot, EvaluationAsOf: input.EvaluationAsOf,
		Status: StatusRegistered, OperationRequestRef: input.OperationRequestRef,
		ApprovalRef: input.ApprovalRef, CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt,
	}
	if err := job.Validate(); err != nil {
		return Job{}, reliability.Event{}, err
	}
	return job, event(job, "EVT.A.19-16", "coupon.bulk_issue.registered", input.CreatedAt), nil
}

func (j Job) Validate() error {
	if !bulkJobIDPattern.MatchString(j.ID) || strings.TrimSpace(j.CampaignID) == "" || strings.TrimSpace(j.OwnerServiceID) == "" ||
		utf8.RuneCountInString(j.OwnerServiceID) > 200 ||
		strings.TrimSpace(j.AudienceDefinitionRef) == "" || j.EvaluationAsOf.IsZero() ||
		strings.TrimSpace(j.OperationRequestRef) == "" || strings.TrimSpace(j.ApprovalRef) == "" ||
		j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
		return oops.In("coupon_bulk_job").Code("coupon.bulk_job_invalid").New("bulk coupon issue job is incomplete")
	}
	if err := j.AudienceSnapshot.Validate(); err != nil {
		return err
	}
	if j.AudienceSnapshot.SourceRef.ID != j.AudienceDefinitionRef {
		return oops.In("coupon_bulk_job").Code("coupon.bulk_job_audience_ref_mismatch").New("audience definition and snapshot references do not match")
	}
	if j.TargetCount < 0 || j.SucceededCount < 0 || j.RejectedCount < 0 || j.FailedCount < 0 ||
		j.SucceededCount+j.RejectedCount+j.FailedCount > j.TargetCount {
		return oops.In("coupon_bulk_job").Code("coupon.bulk_job_counts_invalid").New("bulk coupon issue counters are invalid")
	}
	if (j.Status == StatusCompleted || j.Status == StatusCompletedWithFailures) && !j.PlanningComplete {
		return oops.In("coupon_bulk_job").Code("coupon.bulk_job_planning_incomplete").New("completed bulk job requires a closed audience plan")
	}
	if (j.Status == StatusCompleted || j.Status == StatusCompletedWithFailures) &&
		j.SucceededCount+j.RejectedCount+j.FailedCount != j.TargetCount {
		return oops.In("coupon_bulk_job").Code("coupon.bulk_job_counts_invalid").New("completed bulk job requires one terminal result per target")
	}
	switch j.Status {
	case StatusRegistered, StatusRunning, StatusCompleted, StatusCompletedWithFailures, StatusFailed:
		return nil
	default:
		return oops.In("coupon_bulk_job").Code("coupon.bulk_job_status_invalid").New("bulk coupon issue job status is invalid")
	}
}

func (j *Job) Lease(expectedVersion int64, owner string, until, now time.Time) error {
	if err := j.expectVersion(expectedVersion); err != nil {
		return err
	}
	if j.terminal() {
		return invalidTransition(j.Status, StatusRunning)
	}
	if strings.TrimSpace(owner) == "" || !until.After(now) {
		return oops.In("coupon_bulk_job").Code("coupon.bulk_job_lease_invalid").New("bulk job lease owner and future deadline are required")
	}
	if j.LeaseUntil != nil && j.LeaseUntil.After(now) && j.LeaseOwner != owner {
		return oops.In("coupon_bulk_job").Code("coupon.bulk_job_already_leased").New("bulk coupon issue job is leased by another worker")
	}
	j.Status = StatusRunning
	j.LeaseOwner = owner
	j.LeaseUntil = &until
	j.AttemptCount++
	j.Version++
	j.UpdatedAt = now
	return nil
}

type ResultDelta struct {
	TargetCount    *int64
	SucceededCount int64
	RejectedCount  int64
	FailedCount    int64
	ResultRef      string
	Final          bool
	RecordedAt     time.Time
}

func (j *Job) AggregateResult(expectedVersion int64, delta ResultDelta) (reliability.Event, error) {
	if err := j.expectVersion(expectedVersion); err != nil {
		return reliability.Event{}, err
	}
	if j.terminal() {
		return reliability.Event{}, invalidTransition(j.Status, StatusRunning)
	}
	if delta.SucceededCount < 0 || delta.RejectedCount < 0 || delta.FailedCount < 0 || strings.TrimSpace(delta.ResultRef) == "" || delta.RecordedAt.IsZero() {
		return reliability.Event{}, oops.In("coupon_bulk_job").Code("coupon.bulk_job_result_invalid").New("bulk result counts and result reference are invalid")
	}
	if delta.SucceededCount+delta.RejectedCount+delta.FailedCount == 0 && delta.TargetCount == nil && !delta.Final {
		return reliability.Event{}, oops.In("coupon_bulk_job").Code("coupon.bulk_job_result_empty").New("bulk result does not change the aggregate")
	}
	next := *j
	if delta.TargetCount != nil {
		if *delta.TargetCount < 0 || (next.TargetCount > 0 && next.TargetCount != *delta.TargetCount) {
			return reliability.Event{}, oops.In("coupon_bulk_job").Code("coupon.bulk_job_target_conflict").New("bulk target count conflicts with the registered audience")
		}
		next.TargetCount = *delta.TargetCount
	}
	next.Status = StatusRunning
	next.SucceededCount += delta.SucceededCount
	next.RejectedCount += delta.RejectedCount
	next.FailedCount += delta.FailedCount
	total := next.SucceededCount + next.RejectedCount + next.FailedCount
	if total > next.TargetCount {
		return reliability.Event{}, oops.In("coupon_bulk_job").Code("coupon.bulk_job_count_overflow").New("bulk terminal results exceed the target count")
	}
	if delta.Final && !next.PlanningComplete {
		return reliability.Event{}, oops.In("coupon_bulk_job").Code("coupon.bulk_job_planning_incomplete").New("bulk job cannot finish before audience planning is complete")
	}
	complete := next.PlanningComplete && (delta.Final || (next.TargetCount > 0 && total == next.TargetCount))
	if complete && total != next.TargetCount {
		return reliability.Event{}, oops.In("coupon_bulk_job").Code("coupon.bulk_job_incomplete").New("bulk job cannot complete before every target has a terminal result")
	}
	next.Version++
	next.UpdatedAt = delta.RecordedAt
	if !complete {
		*j = next
		return reliability.Event{}, nil
	}
	next.LeaseOwner = ""
	next.LeaseUntil = nil
	if next.RejectedCount == 0 && next.FailedCount == 0 {
		next.Status = StatusCompleted
		*j = next
		return event(next, "EVT.A.19-17", "coupon.bulk_issue.completed", delta.RecordedAt), nil
	}
	next.Status = StatusCompletedWithFailures
	*j = next
	return event(next, "EVT.A.19-18", "coupon.bulk_issue.completed_with_failures", delta.RecordedAt), nil
}

func (j Job) terminal() bool {
	return j.Status == StatusCompleted || j.Status == StatusCompletedWithFailures || j.Status == StatusFailed
}

func (j Job) expectVersion(expected int64) error {
	if j.Version == expected {
		return nil
	}
	return oops.In("coupon_bulk_job").Code("coupon.version_conflict").With("bulk_job_id", j.ID, "expected_version", expected, "actual_version", j.Version).New("bulk coupon issue job version does not match")
}

func invalidTransition(from, to Status) error {
	return oops.In("coupon_bulk_job").Code("coupon.bulk_job_transition_invalid").With("from", from, "to", to).New("bulk coupon issue job state transition is not allowed")
}

func event(j Job, documentID, eventType string, at time.Time) reliability.Event {
	return reliability.Event{
		ID: uuid.New(), DocumentID: documentID, Type: eventType,
		AggregateType: "BulkCouponIssueJob", AggregateID: j.ID, AggregateVersion: j.Version,
		PayloadSchemaVersion: 1, OccurredAt: at,
		Data: map[string]any{
			"bulk_job_id": j.ID, "campaign_id": j.CampaignID, "status": j.Status,
			"target_count": j.TargetCount, "succeeded_count": j.SucceededCount,
			"rejected_count": j.RejectedCount, "failed_count": j.FailedCount,
		},
	}
}
