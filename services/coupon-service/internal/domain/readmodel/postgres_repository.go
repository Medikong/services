package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) (*PostgresRepository, error) {
	if pool == nil {
		return nil, oops.In("coupon_read_model_repository").Code("coupon.pool_required").New("postgres pool is required")
	}
	return &PostgresRepository{pool: pool}, nil
}

func (r *PostgresRepository) ListWallet(ctx context.Context, input WalletQuery) (Page[WalletCoupon], error) {
	limit, err := normalizeLimit(input.Limit)
	if err != nil || !validUserID(input.UserID) || (input.Status != "" && !input.Status.Valid()) {
		return Page[WalletCoupon]{}, ErrInvalidQuery
	}
	cursorAt, cursorID, err := decodeCursor(input.Cursor, "wallet")
	if err != nil {
		return Page[WalletCoupon]{}, err
	}
	query := `SELECT user_id,user_coupon_id,campaign_id,display_name,benefit,display_status,
		usable_from,expires_at,last_event_id,projection_version,updated_at
		FROM rm_user_coupon_wallet WHERE user_id=$1`
	args := []any{input.UserID}
	if input.Status != "" {
		args = append(args, input.Status)
		query += fmt.Sprintf(" AND display_status=$%d", len(args))
	}
	if !cursorAt.IsZero() {
		args = append(args, cursorAt, cursorID)
		query += fmt.Sprintf(" AND (expires_at,user_coupon_id)>($%d,$%d)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY expires_at,user_coupon_id LIMIT $%d", len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return Page[WalletCoupon]{}, databaseError("list_wallet", err)
	}
	defer rows.Close()
	items := make([]WalletCoupon, 0, limit+1)
	for rows.Next() {
		var item WalletCoupon
		var benefit []byte
		if err := rows.Scan(&item.UserID, &item.UserCouponID, &item.CampaignID, &item.DisplayName, &benefit,
			&item.Status, &item.UsableFrom, &item.ExpiresAt, &item.LastEventID, &item.ProjectionVersion, &item.UpdatedAt); err != nil {
			return Page[WalletCoupon]{}, databaseError("scan_wallet", err)
		}
		if err := json.Unmarshal(benefit, &item.Benefit); err != nil {
			return Page[WalletCoupon]{}, databaseError("decode_wallet_benefit", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[WalletCoupon]{}, databaseError("list_wallet", err)
	}
	return walletPage(items, limit)
}

func (r *PostgresRepository) GetCouponDetail(ctx context.Context, userID, userCouponID string) (CouponDetail, error) {
	if !validUserID(userID) || strings.TrimSpace(userCouponID) == "" || len(userCouponID) > 200 {
		return CouponDetail{}, ErrInvalidQuery
	}
	var result CouponDetail
	var detail []byte
	err := r.pool.QueryRow(ctx, `SELECT user_id,detail,last_event_id,projection_version,updated_at
		FROM rm_coupon_details WHERE user_id=$1 AND user_coupon_id=$2`, userID, userCouponID).
		Scan(&result.UserID, &detail, &result.LastEventID, &result.ProjectionVersion, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return CouponDetail{}, ErrNotFound
	}
	if err != nil {
		return CouponDetail{}, databaseError("get_coupon_detail", err)
	}
	if err := json.Unmarshal(detail, &result.Document); err != nil {
		return CouponDetail{}, databaseError("decode_coupon_detail", err)
	}
	return result, nil
}

func (r *PostgresRepository) CampaignPerformance(ctx context.Context, input PerformanceQuery) (CampaignPerformance, error) {
	if strings.TrimSpace(input.CampaignID) == "" || len(input.CampaignID) > 200 || invalidRange(input.From, input.To) {
		return CampaignPerformance{}, ErrInvalidQuery
	}
	query := `SELECT COALESCE(sum(requested_count),0),COALESCE(sum(issued_count),0),
		COALESCE(sum(rejected_count),0),COALESCE(sum(failed_count),0),
		COALESCE(sum(reserved_count),0),COALESCE(sum(confirmed_count),0),
		COALESCE(sum(released_count),0),COALESCE(sum(reclaimed_count),0),
		COALESCE(sum(confirmed_discount_amount),0)::text,
		COALESCE(sum(reclaimed_discount_amount),0)::text,
		COALESCE(min(currency),''),count(DISTINCT currency),max(bucket_at)
		FROM rm_coupon_performance_minutely WHERE campaign_id=$1`
	args := []any{input.CampaignID}
	if input.From != nil {
		args = append(args, input.From.UTC())
		query += fmt.Sprintf(" AND bucket_at >= $%d", len(args))
	}
	if input.To != nil {
		args = append(args, input.To.UTC())
		query += fmt.Sprintf(" AND bucket_at < $%d", len(args))
	}
	result := CampaignPerformance{CampaignID: input.CampaignID}
	var confirmedAmount, reclaimedAmount, currency string
	var currencyCount int
	var asOf *time.Time
	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&result.Counts.Requested, &result.Counts.Issued, &result.Counts.Rejected, &result.Counts.FailedFinal,
		&result.Counts.Reserved, &result.Counts.Confirmed, &result.Counts.Released, &result.Counts.Reclaimed,
		&confirmedAmount, &reclaimedAmount, &currency, &currencyCount, &asOf,
	)
	if err != nil {
		return CampaignPerformance{}, databaseError("campaign_performance", err)
	}
	if currencyCount > 1 {
		return CampaignPerformance{}, oops.In("coupon_read_model_repository").Code("coupon.performance_currency_mixed").New("campaign performance spans more than one currency")
	}
	if asOf != nil {
		result.AsOf = asOf.UTC()
	}
	if currency != "" {
		result.ConfirmedDiscount = &Money{Amount: confirmedAmount, Currency: currency}
		result.ReclaimedDiscount = &Money{Amount: reclaimedAmount, Currency: currency}
	}
	return result, nil
}

func (r *PostgresRepository) BulkJobSummary(ctx context.Context, bulkJobID string) (BulkJobSummary, error) {
	if strings.TrimSpace(bulkJobID) == "" || len(bulkJobID) > 200 {
		return BulkJobSummary{}, ErrInvalidQuery
	}
	var result BulkJobSummary
	var asOf *time.Time
	err := r.pool.QueryRow(ctx, `
		WITH bulk_coupons AS (
			SELECT coupon.user_coupon_id
			FROM coupon_issue_requests AS request
			JOIN user_coupons AS coupon ON coupon.issue_request_id=request.issue_request_id
			WHERE request.source_type='bulk' AND split_part(request.source_ref, ':', 1)=$1
		)
		SELECT
			count(*) FILTER (WHERE ledger.event_type='coupon.redemption.reserved'),
			count(*) FILTER (WHERE ledger.event_type='coupon.redemption.confirmed'),
			count(*) FILTER (WHERE ledger.event_type='coupon.redemption.released'),
			count(*) FILTER (WHERE ledger.event_type='coupon.redemption.reclaimed'),
			max(ledger.occurred_at)
		FROM coupon_redemption_ledger AS ledger
		JOIN bulk_coupons AS coupon ON coupon.user_coupon_id=ledger.user_coupon_id
	`, bulkJobID).Scan(&result.Counts.Reserved, &result.Counts.Confirmed, &result.Counts.Released, &result.Counts.Reclaimed, &asOf)
	if err != nil {
		return BulkJobSummary{}, databaseError("bulk_job_usage_summary", err)
	}
	if asOf != nil {
		result.AsOf = asOf.UTC()
	}
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(payload->>'next_cursor',''),occurred_at
		FROM bulk_coupon_issue_ledger
		WHERE bulk_job_id=$1 AND event_type='coupon.bulk_issue.page_planned'
		ORDER BY occurred_at DESC,ledger_id DESC LIMIT 1
	`, bulkJobID).Scan(&result.NextCursorRef, &asOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return BulkJobSummary{}, databaseError("bulk_job_progress_summary", err)
	}
	if asOf != nil && asOf.After(result.AsOf) {
		result.AsOf = asOf.UTC()
	}
	return result, nil
}

func (r *PostgresRepository) ListFailures(ctx context.Context, input FailureQuery) (Page[Failure], error) {
	limit, err := normalizeLimit(input.Limit)
	if err != nil || len(input.Kind) > 24 || len(input.Status) > 24 || len(input.OriginalOperationType) > 24 {
		return Page[Failure]{}, ErrInvalidQuery
	}
	cursorAt, cursorID, err := decodeCursor(input.Cursor, "failure")
	if err != nil {
		return Page[Failure]{}, err
	}
	query := `SELECT failure_id,failure_kind,failure_status,business_key,source_ref,
		COALESCE(original_operation_type,''),COALESCE(current_attempt_id,''),COALESCE(result_kind,''),COALESCE(result_ref,''),
		failure_code,attempt_count,
		next_attempt_at,last_event_id,projection_version,updated_at FROM rm_coupon_failures WHERE true`
	args := make([]any, 0, 6)
	if input.Kind != "" {
		args = append(args, input.Kind)
		query += fmt.Sprintf(" AND failure_kind=$%d", len(args))
	}
	if input.Status != "" {
		args = append(args, input.Status)
		query += fmt.Sprintf(" AND failure_status=$%d", len(args))
	}
	if input.OriginalOperationType != "" {
		args = append(args, input.OriginalOperationType)
		query += fmt.Sprintf(" AND original_operation_type=$%d", len(args))
	}
	if !cursorAt.IsZero() {
		args = append(args, cursorAt, cursorID)
		query += fmt.Sprintf(" AND (updated_at,failure_id)<($%d,$%d)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY updated_at DESC,failure_id DESC LIMIT $%d", len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return Page[Failure]{}, databaseError("list_failures", err)
	}
	defer rows.Close()
	items := make([]Failure, 0, limit+1)
	for rows.Next() {
		var item Failure
		if err := rows.Scan(&item.FailureID, &item.Kind, &item.Status, &item.BusinessKey, &item.SourceRef,
			&item.OriginalOperation, &item.CurrentAttemptID, &item.ResultKind, &item.ResultRef, &item.FailureCode,
			&item.AttemptCount, &item.NextAttemptAt, &item.LastEventID, &item.ProjectionVersion, &item.UpdatedAt); err != nil {
			return Page[Failure]{}, databaseError("scan_failure", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[Failure]{}, databaseError("list_failures", err)
	}
	return failurePage(items, limit)
}

func (r *PostgresRepository) ListTimeline(ctx context.Context, input TimelineQuery) (Page[TimelineEvent], error) {
	limit, err := normalizeLimit(input.Limit)
	if err != nil || !validUserID(input.UserID) {
		return Page[TimelineEvent]{}, ErrInvalidQuery
	}
	cursorAt, cursorID, err := decodeCursor(input.Cursor, "timeline")
	if err != nil {
		return Page[TimelineEvent]{}, err
	}
	query := `SELECT timeline_id,user_id,COALESCE(user_coupon_id,''),event_type,result_ref,occurred_at,
		last_event_id,projection_version FROM rm_user_coupon_timeline WHERE user_id=$1`
	args := []any{input.UserID}
	if !cursorAt.IsZero() {
		args = append(args, cursorAt, cursorID)
		query += fmt.Sprintf(" AND (occurred_at,timeline_id)<($%d,$%d::uuid)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY occurred_at DESC,timeline_id DESC LIMIT $%d", len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return Page[TimelineEvent]{}, databaseError("list_timeline", err)
	}
	defer rows.Close()
	items := make([]TimelineEvent, 0, limit+1)
	for rows.Next() {
		var item TimelineEvent
		var resultRef []byte
		if err := rows.Scan(&item.TimelineID, &item.UserID, &item.UserCouponID, &item.EventType, &resultRef,
			&item.OccurredAt, &item.LastEventID, &item.ProjectionVersion); err != nil {
			return Page[TimelineEvent]{}, databaseError("scan_timeline", err)
		}
		if err := json.Unmarshal(resultRef, &item.ResultRef); err != nil {
			return Page[TimelineEvent]{}, databaseError("decode_timeline_result_ref", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[TimelineEvent]{}, databaseError("list_timeline", err)
	}
	return timelinePage(items, limit)
}

func validUserID(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 128
}

func (r *PostgresRepository) GetIncidentStatus(ctx context.Context) (IncidentStatus, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT ON (scope_type) incident_key,scope_type,scope_ref,status,
		business_metrics,COALESCE(observability_ref,''),observed_at,last_event_id,projection_version
		FROM rm_coupon_incident_status
		ORDER BY scope_type,observed_at DESC,incident_key DESC`)
	if err != nil {
		return IncidentStatus{}, databaseError("incident_status", err)
	}
	defer rows.Close()
	result := IncidentStatus{Signals: make(map[SignalName]IncidentSignal)}
	for rows.Next() {
		var signal IncidentSignal
		var name string
		var metrics []byte
		if err := rows.Scan(&signal.IncidentKey, &name, &signal.ScopeRef, &signal.Status, &metrics,
			&signal.ObservabilityRef, &signal.ObservedAt, &signal.LastEventID, &signal.ProjectionVersion); err != nil {
			return IncidentStatus{}, databaseError("scan_incident_status", err)
		}
		signal.Name = SignalName(name)
		if !signal.Name.valid() {
			return IncidentStatus{}, oops.In("coupon_read_model_repository").Code("coupon.incident_signal_invalid").With("signal", name).New("incident read model contains an unsupported signal")
		}
		if err := json.Unmarshal(metrics, &signal.BusinessMetrics); err != nil {
			return IncidentStatus{}, databaseError("decode_incident_metrics", err)
		}
		result.Signals[signal.Name] = signal
		if signal.ObservedAt.After(result.AsOf) {
			result.AsOf = signal.ObservedAt
		}
	}
	if err := rows.Err(); err != nil {
		return IncidentStatus{}, databaseError("incident_status", err)
	}
	return result, nil
}

func (r *PostgresRepository) ListCostAttributions(ctx context.Context, input CostAttributionQuery) (Page[CostAttribution], error) {
	limit, err := normalizeLimit(input.Limit)
	if err != nil || len(input.OrderID) > 200 || len(input.CampaignID) > 200 || invalidRange(input.From, input.To) {
		return Page[CostAttribution]{}, ErrInvalidQuery
	}
	cursorAt, cursorID, err := decodeCursor(input.Cursor, "cost")
	if err != nil {
		return Page[CostAttribution]{}, err
	}
	query := `SELECT attribution_id,order_id,redemption_id,campaign_id,kind,discount_amount::text,currency,
		cost_shares,COALESCE(settlement_ref,''),occurred_at,last_event_id,projection_version
		FROM rm_coupon_cost_attribution WHERE true`
	args := make([]any, 0, 8)
	if input.OrderID != "" {
		args = append(args, input.OrderID)
		query += fmt.Sprintf(" AND order_id=$%d", len(args))
	}
	if input.CampaignID != "" {
		args = append(args, input.CampaignID)
		query += fmt.Sprintf(" AND campaign_id=$%d", len(args))
	}
	if input.From != nil {
		args = append(args, input.From.UTC())
		query += fmt.Sprintf(" AND occurred_at >= $%d", len(args))
	}
	if input.To != nil {
		args = append(args, input.To.UTC())
		query += fmt.Sprintf(" AND occurred_at < $%d", len(args))
	}
	if !cursorAt.IsZero() {
		args = append(args, cursorAt, cursorID)
		query += fmt.Sprintf(" AND (occurred_at,attribution_id)<($%d,$%d::uuid)", len(args)-1, len(args))
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY occurred_at DESC,attribution_id DESC LIMIT $%d", len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return Page[CostAttribution]{}, databaseError("list_cost_attributions", err)
	}
	defer rows.Close()
	items := make([]CostAttribution, 0, limit+1)
	for rows.Next() {
		var item CostAttribution
		var orderID string
		var shares []byte
		if err := rows.Scan(&item.AttributionID, &orderID, &item.RedemptionID, &item.CampaignID, &item.Kind,
			&item.Discount.Amount, &item.Discount.Currency, &shares, &item.SettlementRef, &item.OccurredAt,
			&item.LastEventID, &item.ProjectionVersion); err != nil {
			return Page[CostAttribution]{}, databaseError("scan_cost_attribution", err)
		}
		item.OrderRef = ExternalRef{Context: "order", Type: "order", ID: orderID}
		if err := json.Unmarshal(shares, &item.Shares); err != nil {
			return Page[CostAttribution]{}, databaseError("decode_cost_shares", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[CostAttribution]{}, databaseError("list_cost_attributions", err)
	}
	return costPage(items, limit)
}

func (r *PostgresRepository) ListActiveNotices(ctx context.Context, input NoticeQuery) ([]ReadOnlyNotice, error) {
	if input.AsOf.IsZero() || len(input.Scopes) == 0 || len(input.Scopes) > 100 {
		return nil, ErrInvalidQuery
	}
	limit := input.Limit
	if limit == 0 {
		limit = 10
	}
	if limit < 1 || limit > 10 {
		return nil, ErrInvalidQuery
	}
	types := make([]string, 0, len(input.Scopes))
	refs := make([]string, 0, len(input.Scopes))
	seen := make(map[NoticeScope]struct{}, len(input.Scopes))
	for _, scope := range input.Scopes {
		if !validNoticeScope(scope) {
			return nil, ErrInvalidQuery
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}
		types = append(types, scope.Type)
		refs = append(refs, scope.Ref)
	}
	rows, err := r.pool.Query(ctx, `SELECT notice.control_id,notice.scope_type,notice.scope_ref,notice.message,
		notice.effective_from,notice.active,notice.last_event_id,notice.projection_version,notice.updated_at
		FROM rm_coupon_read_only_notice AS notice
		JOIN unnest($1::text[],$2::text[]) AS requested(scope_type,scope_ref)
		  ON requested.scope_type=notice.scope_type AND requested.scope_ref=notice.scope_ref
		WHERE notice.active AND notice.effective_from <= $3
		ORDER BY notice.effective_from DESC,notice.control_id LIMIT $4`, types, refs, input.AsOf.UTC(), limit)
	if err != nil {
		return nil, databaseError("list_active_notices", err)
	}
	defer rows.Close()
	items := make([]ReadOnlyNotice, 0, limit)
	for rows.Next() {
		var item ReadOnlyNotice
		if err := rows.Scan(&item.ControlID, &item.ScopeType, &item.ScopeRef, &item.Message, &item.EffectiveFrom,
			&item.Active, &item.LastEventID, &item.ProjectionVersion, &item.UpdatedAt); err != nil {
			return nil, databaseError("scan_active_notice", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("list_active_notices", err)
	}
	return items, nil
}

func walletPage(items []WalletCoupon, limit int) (Page[WalletCoupon], error) {
	result := Page[WalletCoupon]{Items: items}
	if len(items) <= limit {
		return result, nil
	}
	result.Items = items[:limit]
	last := result.Items[len(result.Items)-1]
	cursor, err := encodeCursor("wallet", last.ExpiresAt, last.UserCouponID)
	if err != nil {
		return Page[WalletCoupon]{}, err
	}
	result.NextCursor = cursor
	return result, nil
}

func failurePage(items []Failure, limit int) (Page[Failure], error) {
	result := Page[Failure]{Items: items}
	if len(items) <= limit {
		return result, nil
	}
	result.Items = items[:limit]
	last := result.Items[len(result.Items)-1]
	cursor, err := encodeCursor("failure", last.UpdatedAt, last.FailureID)
	if err != nil {
		return Page[Failure]{}, err
	}
	result.NextCursor = cursor
	return result, nil
}

func timelinePage(items []TimelineEvent, limit int) (Page[TimelineEvent], error) {
	result := Page[TimelineEvent]{Items: items}
	if len(items) <= limit {
		return result, nil
	}
	result.Items = items[:limit]
	last := result.Items[len(result.Items)-1]
	cursor, err := encodeCursor("timeline", last.OccurredAt, last.TimelineID.String())
	if err != nil {
		return Page[TimelineEvent]{}, err
	}
	result.NextCursor = cursor
	return result, nil
}

func costPage(items []CostAttribution, limit int) (Page[CostAttribution], error) {
	result := Page[CostAttribution]{Items: items}
	if len(items) <= limit {
		return result, nil
	}
	result.Items = items[:limit]
	last := result.Items[len(result.Items)-1]
	cursor, err := encodeCursor("cost", last.OccurredAt, last.AttributionID.String())
	if err != nil {
		return Page[CostAttribution]{}, err
	}
	result.NextCursor = cursor
	return result, nil
}

func invalidRange(from, to *time.Time) bool {
	return from != nil && to != nil && !from.Before(*to)
}

func validNoticeScope(scope NoticeScope) bool {
	if strings.TrimSpace(scope.Ref) == "" || len(scope.Ref) > 200 {
		return false
	}
	switch scope.Type {
	case "campaign", "drop", "user_group":
		return true
	default:
		return false
	}
}

func (n SignalName) valid() bool {
	switch n {
	case SignalIssuance, SignalRedemption, SignalRecovery, SignalPostgres, SignalRedis, SignalMQ, SignalWorkers:
		return true
	default:
		return false
	}
}

func databaseError(operation string, err error) error {
	return oops.In("coupon_read_model_repository").Code("coupon.read_model_database_failed").With("operation", operation).Wrap(err)
}

var _ Repository = (*PostgresRepository)(nil)
