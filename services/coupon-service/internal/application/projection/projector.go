package projection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"

	"github.com/Medikong/services/services/coupon-service/internal/application/policy"
	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type Projector struct {
	pool         *pgxpool.Pool
	consumerName string
}

func New(pool *pgxpool.Pool, consumerName string) (*Projector, error) {
	if pool == nil || strings.TrimSpace(consumerName) == "" || len(consumerName) > 160 {
		return nil, oops.In("coupon_read_model_projector").Code("coupon.projector_config_invalid").New("projector pool and consumer name are required")
	}
	return &Projector{pool: pool, consumerName: consumerName}, nil
}

func (p *Projector) Publish(ctx context.Context, event policy.Envelope) error {
	return p.Handle(ctx, event)
}

func (p *Projector) Handle(ctx context.Context, event policy.Envelope) (err error) {
	if p == nil || p.pool == nil {
		return oops.In("coupon_read_model_projector").Code("coupon.projector_required").New("read model projector is not configured")
	}
	if event.EventID == uuid.Nil || strings.TrimSpace(event.EventDocumentID) == "" ||
		strings.TrimSpace(event.EventType) == "" || strings.TrimSpace(event.AggregateType) == "" ||
		strings.TrimSpace(event.AggregateID) == "" || event.AggregateVersion < 0 || event.OccurredAt.IsZero() {
		return oops.In("coupon_read_model_projector").Code("coupon.projector_event_invalid").New("event envelope is incomplete")
	}
	if _, ok := coverage(event.EventDocumentID); !ok {
		return oops.In("coupon_read_model_projector").Code("coupon.projector_event_unsupported").With("event_document_id", event.EventDocumentID).New("event is outside BC.A.19")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return oops.In("coupon_read_model_projector").Code("coupon.projector_event_encode_failed").Wrap(err)
	}
	digest := sha256.Sum256(payload)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return projectorError("begin", err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_read_model_projector", err)
		}
	}()

	done, err := p.claimInbox(ctx, tx, event, digest[:])
	if err != nil {
		return err
	}
	if done {
		if err = tx.Commit(ctx); err != nil {
			return projectorError("commit_duplicate", err)
		}
		committed = true
		return nil
	}
	if event.PayloadSchemaVersion != 1 {
		if _, err = tx.Exec(ctx, `UPDATE consumer_inbox SET status='failed_final',
			failure_code='COUPON_EVENT_SCHEMA_UNSUPPORTED',attempt_count=attempt_count+1,updated_at=now()
			WHERE consumer_name=$1 AND event_id=$2`, p.consumerName, event.EventID); err != nil {
			return projectorError("reject_schema", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return projectorError("commit_schema_rejection", err)
		}
		committed = true
		return oops.In("coupon_read_model_projector").Code("coupon.projector_schema_unsupported").New("event payload schema version is not supported")
	}
	if err = p.apply(ctx, tx, event); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE consumer_inbox SET status='processed',result_ref=$3,
		failure_code=NULL,next_attempt_at=NULL,attempt_count=attempt_count+1,processed_at=now(),updated_at=now()
		WHERE consumer_name=$1 AND event_id=$2`, p.consumerName, event.EventID, "projection:"+event.EventDocumentID); err != nil {
		return projectorError("complete_inbox", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return projectorError("commit", err)
	}
	committed = true
	return nil
}

func (p *Projector) claimInbox(ctx context.Context, tx pgx.Tx, event policy.Envelope, digest []byte) (bool, error) {
	insert, err := tx.Exec(ctx, `INSERT INTO consumer_inbox (
		consumer_name,event_id,event_type,payload_schema_version,payload_hash,status
	) VALUES ($1,$2,$3,$4,$5,'received') ON CONFLICT (consumer_name,event_id) DO NOTHING`,
		p.consumerName, event.EventID, event.EventType, event.PayloadSchemaVersion, digest)
	if err != nil {
		return false, projectorError("claim_inbox", err)
	}
	if insert.RowsAffected() == 1 {
		return false, nil
	}
	var storedHash []byte
	var status string
	if err := tx.QueryRow(ctx, `SELECT payload_hash,status FROM consumer_inbox
		WHERE consumer_name=$1 AND event_id=$2 FOR UPDATE`, p.consumerName, event.EventID).Scan(&storedHash, &status); err != nil {
		return false, projectorError("read_inbox", err)
	}
	if !bytes.Equal(storedHash, digest) {
		return false, oops.In("coupon_read_model_projector").Code("coupon.projector_payload_conflict").New("duplicate event ID has a different payload")
	}
	switch status {
	case "processed":
		return true, nil
	case "failed_final":
		return false, oops.In("coupon_read_model_projector").Code("coupon.projector_event_failed_final").New("event projection was previously finalized as failed")
	default:
		return false, nil
	}
}

func (p *Projector) apply(ctx context.Context, tx pgx.Tx, event policy.Envelope) error {
	switch event.EventDocumentID {
	case "EVT.A.19-07", "EVT.A.19-08", "EVT.A.19-10", "EVT.A.19-11", "EVT.A.19-29", "EVT.A.19-37":
		return projectIssueEvent(ctx, tx, event)
	case "EVT.A.19-09", "EVT.A.19-31":
		return projectUserCouponEvent(ctx, tx, event)
	case "EVT.A.19-18":
		return projectBulkFailure(ctx, tx, event)
	case "EVT.A.19-19", "EVT.A.19-20", "EVT.A.19-21", "EVT.A.19-22", "EVT.A.19-23", "EVT.A.19-24", "EVT.A.19-28":
		return projectRedemptionEvent(ctx, tx, event)
	case "EVT.A.19-25", "EVT.A.19-32", "EVT.A.19-33", "EVT.A.19-34", "EVT.A.19-35", "EVT.A.19-36":
		return projectOperationalSignal(ctx, tx, event)
	case "EVT.A.19-26", "EVT.A.19-27", "EVT.A.19-30", "EVT.A.19-39", "EVT.A.19-40", "EVT.A.19-41":
		return projectRecoveryEvent(ctx, tx, event)
	case "EVT.A.19-38":
		return projectNoticeEvent(ctx, tx, event)
	default:
		return nil
	}
}

func decodeEventData[T any](event policy.Envelope) (T, error) {
	var value T
	payload, err := json.Marshal(event.Data)
	if err != nil {
		return value, projectorError("encode_event_data", err)
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, oops.In("coupon_read_model_projector").Code("coupon.projector_payload_invalid").With("event_document_id", event.EventDocumentID).Wrap(err)
	}
	return value, nil
}

func projectorError(operation string, err error) error {
	return oops.In("coupon_read_model_projector").Code("coupon.projector_database_failed").With("operation", operation).Wrap(err)
}
