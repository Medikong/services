package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type Envelope struct {
	EventID              uuid.UUID      `json:"event_id"`
	EventDocumentID      string         `json:"event_document_id"`
	EventType            string         `json:"event_type"`
	AggregateType        string         `json:"aggregate_type"`
	AggregateID          string         `json:"aggregate_id"`
	AggregateVersion     int64          `json:"aggregate_version"`
	OccurredAt           time.Time      `json:"occurred_at"`
	CorrelationID        string         `json:"correlation_id"`
	CausationID          string         `json:"causation_id,omitempty"`
	TraceID              string         `json:"trace_id,omitempty"`
	PayloadSchemaVersion int16          `json:"payload_schema_version"`
	Data                 map[string]any `json:"data"`
}

func EnvelopeFromEvent(event reliability.Event) Envelope {
	data, _ := event.Data.(map[string]any)
	return Envelope{
		EventID: event.ID, EventDocumentID: event.DocumentID, EventType: event.Type,
		AggregateType: event.AggregateType, AggregateID: event.AggregateID, AggregateVersion: event.AggregateVersion,
		OccurredAt: event.OccurredAt, CorrelationID: event.CorrelationID, CausationID: event.CausationID, TraceID: event.TraceID,
		PayloadSchemaVersion: event.PayloadSchemaVersion, Data: data,
	}
}

type Processor struct {
	pool *pgxpool.Pool
}

func NewProcessor(pool *pgxpool.Pool) *Processor {
	return &Processor{pool: pool}
}

func (p *Processor) Handle(ctx context.Context, consumerName string, envelope Envelope) (err error) {
	if p == nil || p.pool == nil {
		return oops.In("coupon_policy_processor").Code("coupon.pool_required").New("postgres pool is required")
	}
	if strings.TrimSpace(consumerName) == "" || envelope.EventID == uuid.Nil || strings.TrimSpace(envelope.EventDocumentID) == "" {
		return oops.In("coupon_policy_processor").Code("coupon.event_invalid").New("event envelope is incomplete")
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return oops.In("coupon_policy_processor").Code("coupon.event_encode_failed").Wrap(err)
	}
	payloadHash := sha256.Sum256(payload)
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return oops.In("coupon_policy_processor").Code("coupon.transaction_begin_failed").Wrap(err)
	}
	committed := false
	defer func() {
		if !committed {
			err = reliability.Rollback(ctx, tx, "coupon_policy_processor", err)
		}
	}()

	insert, err := tx.Exec(ctx, `
		INSERT INTO consumer_inbox (
			consumer_name, event_id, event_type, payload_schema_version, payload_hash, status
		) VALUES ($1,$2,$3,$4,$5,'received')
		ON CONFLICT (consumer_name, event_id) DO NOTHING
	`, consumerName, envelope.EventID, envelope.EventType, envelope.PayloadSchemaVersion, payloadHash[:])
	if err != nil {
		return oops.In("coupon_policy_processor").Code("coupon.inbox_insert_failed").Wrap(err)
	}
	if insert.RowsAffected() == 0 {
		var storedHash []byte
		var status string
		if err := tx.QueryRow(ctx, `
			SELECT payload_hash, status FROM consumer_inbox
			WHERE consumer_name=$1 AND event_id=$2 FOR UPDATE
		`, consumerName, envelope.EventID).Scan(&storedHash, &status); err != nil {
			return oops.In("coupon_policy_processor").Code("coupon.inbox_read_failed").Wrap(err)
		}
		if !bytes.Equal(storedHash, payloadHash[:]) {
			return oops.In("coupon_policy_processor").Code("coupon.inbox_payload_conflict").New("duplicate event ID has a different payload")
		}
		if status == "processed" {
			if err = tx.Commit(ctx); err != nil {
				return oops.In("coupon_policy_processor").Code("coupon.transaction_commit_failed").Wrap(err)
			}
			committed = true
			return nil
		}
		if status == "failed_final" {
			if err = tx.Commit(ctx); err != nil {
				return oops.In("coupon_policy_processor").Code("coupon.transaction_commit_failed").Wrap(err)
			}
			committed = true
			return oops.In("coupon_policy_processor").Code("coupon.event_failed_final").New("event processing was previously finalized as failed")
		}
	}

	if envelope.PayloadSchemaVersion != 1 {
		if _, err = tx.Exec(ctx, `
			UPDATE consumer_inbox SET status='failed_final', failure_code='COUPON_EVENT_SCHEMA_UNSUPPORTED',
				attempt_count=attempt_count+1, updated_at=now()
			WHERE consumer_name=$1 AND event_id=$2
		`, consumerName, envelope.EventID); err != nil {
			return oops.In("coupon_policy_processor").Code("coupon.inbox_finalize_failed").Wrap(err)
		}
		if err = tx.Commit(ctx); err != nil {
			return oops.In("coupon_policy_processor").Code("coupon.transaction_commit_failed").Wrap(err)
		}
		committed = true
		return oops.In("coupon_policy_processor").Code("coupon.event_schema_unsupported").New("event payload schema version is not supported")
	}

	for _, match := range EventRoutes(envelope.EventDocumentID) {
		if match.Route.CommandID == "CMD.A.19-19" && !eventHasRetryTime(envelope.Data) {
			continue
		}
		targetID := envelope.AggregateID
		if match.Route.TargetIDField != "" {
			value, ok := eventString(envelope.Data, match.Route.TargetIDField)
			if !ok {
				return oops.In("coupon_policy_processor").
					Code("coupon.policy_target_missing").
					With("event_document_id", envelope.EventDocumentID, "target_field", match.Route.TargetIDField).
					New("policy event does not contain its target aggregate reference")
			}
			targetID = value
		}
		businessKey := match.Policy.ID + ":" + envelope.EventID.String() + ":" + match.Route.CommandID
		if _, err = tx.Exec(ctx, `
			INSERT INTO coupon_command_requests (
				command_request_id, command_document_id, policy_document_id, source_event_id,
				aggregate_type, aggregate_id, business_key, correlation_id, causation_id, trace_id, payload
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),NULLIF($10,''),$11)
			ON CONFLICT (policy_document_id, source_event_id, command_document_id, business_key) DO NOTHING
		`, uuid.New(), match.Route.CommandID, match.Policy.ID, envelope.EventID,
			match.Route.AggregateType, targetID, businessKey, envelope.CorrelationID, envelope.CausationID, envelope.TraceID, payload); err != nil {
			return oops.In("coupon_policy_processor").Code("coupon.policy_command_enqueue_failed").Wrap(err)
		}
	}
	if _, err = tx.Exec(ctx, `
		UPDATE consumer_inbox SET status='processed', result_ref=$3, processed_at=now(), updated_at=now()
		WHERE consumer_name=$1 AND event_id=$2
	`, consumerName, envelope.EventID, "policy:"+envelope.EventDocumentID); err != nil {
		return oops.In("coupon_policy_processor").Code("coupon.inbox_complete_failed").Wrap(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return oops.In("coupon_policy_processor").Code("coupon.transaction_commit_failed").Wrap(err)
	}
	committed = true
	return nil
}

func eventHasRetryTime(data map[string]any) bool {
	for _, name := range []string{"next_attempt_at", "nextAttemptAt"} {
		if value, ok := data[name].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func eventString(data map[string]any, snakeName string) (string, bool) {
	for _, name := range []string{snakeName, lowerCamel(snakeName)} {
		value, ok := data[name].(string)
		if ok && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func lowerCamel(value string) string {
	parts := strings.Split(value, "_")
	for index := 1; index < len(parts); index++ {
		if parts[index] != "" {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, "")
}
