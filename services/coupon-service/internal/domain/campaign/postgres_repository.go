package campaign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
)

const aggregateType = "CouponCampaign"

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) (*PostgresRepository, error) {
	if db == nil {
		return nil, oops.In("coupon_campaign_repository").Code("campaign.database_required").New("postgres pool is required")
	}
	return &PostgresRepository{db: db}, nil
}

func (r *PostgresRepository) Create(ctx context.Context, campaign Campaign, command Command) (Mutation, error) {
	if err := campaign.Validate(); err != nil {
		return Mutation{}, err
	}
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, aggregateType, campaign.ID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		issuerRef, funderRef, ownerSnapshot, err := campaignJSON(campaign)
		if err != nil {
			return Mutation{}, err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO coupon_campaigns (
				campaign_id, display_name, description, status, starts_at, ends_at,
				claim_starts_at, claim_ends_at, current_policy_version, total_quantity,
				per_user_limit, reserved_quantity, confirmed_quantity, issuer_type,
				issuer_ref, funder_type, funder_ref, platform_share_percentage,
				approval_ref, owner_snapshot, external_business_ref, version
			) VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,NULLIF($18,'')::numeric,NULLIF($19,''),$20,NULLIF($21,''),$22)`,
			campaign.ID, campaign.DisplayName, campaign.Description, campaign.Status, campaign.StartsAt, campaign.EndsAt,
			campaign.ClaimStartsAt, campaign.ClaimEndsAt, campaign.CurrentPolicyVersion, campaign.TotalQuantity,
			campaign.PerUserLimit, campaign.ReservedQuantity, campaign.ConfirmedQuantity, campaign.IssuerAndFunding.IssuerType,
			issuerRef, campaign.IssuerAndFunding.FunderType, funderRef, campaign.IssuerAndFunding.PlatformSharePercentage,
			campaign.ApprovalRef, ownerSnapshot, campaign.ExternalBusinessRef, campaign.Version)
		if err != nil {
			return Mutation{}, dbError("create_campaign", err)
		}
		if err := insertPolicyVersion(ctx, tx, campaign.ID, PolicyVersion{
			Version: campaign.CurrentPolicyVersion, EffectiveAt: campaign.StartsAt,
			Benefits: campaign.Benefits, Applicability: campaign.Applicability,
			IssuerAndFunding: campaign.IssuerAndFunding,
		}); err != nil {
			return Mutation{}, err
		}
		resultRef := strings.Join([]string{"campaign", campaign.ID, "version", intString(campaign.Version)}, ":")
		snapshot, err := json.Marshal(campaign)
		if err != nil {
			return Mutation{}, oops.In("coupon_campaign_repository").Code("campaign.snapshot_encode_failed").Wrap(err)
		}
		if err := insertOutbox(ctx, tx, command, "coupon.policy.registered", "EVT.A.19-01", campaign.ID, campaign.Version, snapshot); err != nil {
			return Mutation{}, err
		}
		if campaign.Status == StatusUnderReview {
			if err := insertOutbox(ctx, tx, command, "coupon.review.requested", "EVT.A.19-02", campaign.ID, campaign.Version, snapshot); err != nil {
				return Mutation{}, err
			}
		}
		mutation := Mutation{ResultRef: resultRef, ResponseSnapshot: snapshot}
		if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

func (r *PostgresRepository) Get(ctx context.Context, campaignID string) (Campaign, error) {
	return loadCampaign(ctx, r.db, campaignID, false, nil)
}

func (r *PostgresRepository) GetEffective(ctx context.Context, campaignID string, at time.Time) (Campaign, error) {
	if at.IsZero() {
		return Campaign{}, ErrCampaignInactive
	}
	return loadCampaign(ctx, r.db, campaignID, false, &at)
}

// FindQuantityReservation exposes durable issue_request_id correlation without
// granting the policy dispatcher mutation access to the campaign Aggregate.
func (r *PostgresRepository) FindQuantityReservation(ctx context.Context, campaignID, issueRequestID string) (QuantityReservation, bool, error) {
	query := `SELECT campaign_id,issue_request_id,quantity,state,result_ref,reserved_at,decided_at FROM coupon_quantity_reservations WHERE campaign_id=$1 AND issue_request_id=$2`
	var reservation QuantityReservation
	var decided sql.NullTime
	err := r.db.QueryRow(ctx, query, campaignID, issueRequestID).Scan(
		&reservation.CampaignID, &reservation.IssueRequestID, &reservation.Quantity,
		&reservation.State, &reservation.ResultRef, &reservation.ReservedAt, &decided,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuantityReservation{}, false, nil
	}
	if err != nil {
		return QuantityReservation{}, false, dbError("get_quantity_reservation", err)
	}
	if decided.Valid {
		reservation.DecidedAt = &decided.Time
	}
	return reservation, true, nil
}

func (r *PostgresRepository) ConfigureIssuance(ctx context.Context, campaignID string, expectedVersion int64, limit QuantityLimit, command Command) (Mutation, error) {
	if err := limit.Validate(); err != nil {
		return Mutation{}, err
	}
	return r.updateCampaign(ctx, campaignID, expectedVersion, command, "coupon.policy.registered", "EVT.A.19-01", func(current Campaign) (Campaign, error) {
		if limit.TotalQuantity < current.ReservedQuantity+current.ConfirmedQuantity {
			return Campaign{}, ErrQuantityUnavailable
		}
		current.TotalQuantity = limit.TotalQuantity
		current.PerUserLimit = limit.PerUserLimit
		current.ClaimStartsAt = &limit.ClaimStartsAt
		current.ClaimEndsAt = &limit.ClaimEndsAt
		return current, nil
	})
}

func (r *PostgresRepository) Review(ctx context.Context, campaignID string, expectedVersion int64, decision Status, reasonCode string, command Command) (Mutation, error) {
	return r.updateCampaign(ctx, campaignID, expectedVersion, command, reviewEvent(decision), reviewDocument(decision), func(current Campaign) (Campaign, error) {
		if strings.TrimSpace(reasonCode) == "" || strings.TrimSpace(command.ApprovalRef) == "" {
			return Campaign{}, oops.In("coupon_campaign").Code("campaign.review_evidence_required").New("campaign review reason and approval reference are required")
		}
		updated, err := current.Review(decision)
		if err != nil {
			return Campaign{}, err
		}
		updated.ApprovalRef = command.ApprovalRef
		updated.IssuerAndFunding.ApprovalRef = command.ApprovalRef
		return updated, nil
	})
}

func (r *PostgresRepository) AddPolicyVersion(ctx context.Context, campaignID string, expectedVersion int64, policy PolicyVersion, command Command) (Mutation, error) {
	if policy.Version < 2 || policy.EffectiveAt.IsZero() || !policy.EffectiveAt.After(command.OccurredAt) || len(policy.Benefits) == 0 || len(policy.Applicability) == 0 {
		return Mutation{}, oops.In("coupon_campaign").Code("campaign.policy_version_invalid").New("new policy version and future effective time are required")
	}
	if err := policy.IssuerAndFunding.Validate(); err != nil {
		return Mutation{}, err
	}
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, aggregateType, campaignID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		current, err := loadCampaign(ctx, tx, campaignID, true, nil)
		if err != nil {
			return Mutation{}, err
		}
		if current.Version != expectedVersion {
			return Mutation{}, ErrVersionConflict
		}
		if policy.Version != current.CurrentPolicyVersion+1 {
			return Mutation{}, oops.In("coupon_campaign").Code("campaign.policy_version_invalid").New("new policy version must follow the current version")
		}
		for i := range policy.Benefits {
			policy.Benefits[i].PolicyVersion = policy.Version
		}
		for i := range policy.Applicability {
			policy.Applicability[i].PolicyVersion = policy.Version
			policy.Applicability[i].EffectiveFrom = policy.EffectiveAt
		}
		issuerRef, err := json.Marshal(policy.IssuerAndFunding.IssuerRef)
		if err != nil {
			return Mutation{}, oops.In("coupon_campaign_repository").Code("campaign.issuer_encode_failed").Wrap(err)
		}
		var funderRef []byte
		if policy.IssuerAndFunding.FunderRef != nil {
			funderRef, err = json.Marshal(policy.IssuerAndFunding.FunderRef)
			if err != nil {
				return Mutation{}, oops.In("coupon_campaign_repository").Code("campaign.funder_encode_failed").Wrap(err)
			}
		}
		newVersion := current.Version + 1
		tag, err := tx.Exec(ctx, `UPDATE coupon_campaigns SET current_policy_version=$1, issuer_type=$2, issuer_ref=$3, funder_type=$4, funder_ref=$5, platform_share_percentage=NULLIF($6,'')::numeric, approval_ref=NULLIF($7,''), version=$8, updated_at=$9 WHERE campaign_id=$10 AND version=$11`,
			policy.Version, policy.IssuerAndFunding.IssuerType, issuerRef, policy.IssuerAndFunding.FunderType, funderRef, policy.IssuerAndFunding.PlatformSharePercentage, policy.IssuerAndFunding.ApprovalRef, newVersion, command.OccurredAt, campaignID, expectedVersion)
		if err != nil {
			return Mutation{}, dbError("change_policy", err)
		}
		if tag.RowsAffected() != 1 {
			return Mutation{}, ErrVersionConflict
		}
		if err := insertPolicyVersion(ctx, tx, campaignID, policy); err != nil {
			return Mutation{}, err
		}
		payload, _ := json.Marshal(map[string]any{"campaignId": campaignID, "policyVersion": policy.Version, "effectiveAt": policy.EffectiveAt})
		resultRef := strings.Join([]string{"campaign", campaignID, "policy", intString(policy.Version)}, ":")
		if err := insertOutbox(ctx, tx, command, "coupon.policy.changed", "EVT.A.19-06", campaignID, newVersion, payload); err != nil {
			return Mutation{}, err
		}
		mutation := Mutation{ResultRef: resultRef, ResponseSnapshot: payload}
		if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

func (r *PostgresRepository) ReserveQuantity(ctx context.Context, campaignID, issueRequestID string, quantity, expectedVersion int64, at time.Time, command Command) (QuantityMutation, error) {
	if quantity <= 0 || issueRequestID == "" {
		return QuantityMutation{}, ErrInvalidQuantity
	}
	return inTx(ctx, r.db, func(tx pgx.Tx) (QuantityMutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, aggregateType, campaignID)
		if err != nil {
			return QuantityMutation{}, err
		}
		if idem.replayed {
			return quantityIdempotencyReplay(idem.mutation)
		}
		if existing, ok, err := loadReservation(ctx, tx, campaignID, issueRequestID, true); err != nil {
			return QuantityMutation{}, err
		} else if ok {
			if existing.Quantity != quantity {
				return QuantityMutation{}, ErrIdempotencyConflict
			}
			mutation := quantityReplay(existing)
			if err := finishIdempotency(ctx, tx, command, "completed", mutation.Mutation); err != nil {
				return QuantityMutation{}, err
			}
			return mutation, nil
		}
		current, err := loadCampaign(ctx, tx, campaignID, true, nil)
		if err != nil {
			return QuantityMutation{}, err
		}
		if current.Version != expectedVersion {
			return QuantityMutation{}, ErrVersionConflict
		}
		blocked, err := issuanceBlocked(ctx, tx, campaignID, at)
		if err != nil {
			return QuantityMutation{}, err
		}
		var ruleErr error
		if blocked {
			ruleErr = ErrIssuanceBlocked
		} else {
			ruleErr = current.CanReserve(quantity, at)
		}
		if ruleErr != nil {
			resultRef := strings.Join([]string{"quantity", campaignID, issueRequestID, "rejected"}, ":")
			payload, _ := json.Marshal(map[string]any{"campaignId": campaignID, "issueRequestId": issueRequestID, "quantity": quantity, "reasonCode": errorCode(ruleErr), "resultRef": resultRef})
			if err := insertQuantityLedger(ctx, tx, campaignID, issueRequestID, "reject", quantity, "unreserved", "rejected", resultRef, at); err != nil {
				return QuantityMutation{}, err
			}
			if err := insertOutbox(ctx, tx, command, "coupon.quantity.rejected", "EVT.A.19-33", campaignID, current.Version, payload); err != nil {
				return QuantityMutation{}, err
			}
			mutation := QuantityMutation{Mutation: Mutation{ResultRef: resultRef, ResponseSnapshot: payload}, Reservation: QuantityReservation{CampaignID: campaignID, IssueRequestID: issueRequestID, Quantity: quantity, State: ReservationRejected, ResultRef: resultRef}, Version: current.Version, Rejected: true, ReasonCode: errorCode(ruleErr)}
			if err := finishIdempotency(ctx, tx, command, "failed_final", mutation.Mutation); err != nil {
				return QuantityMutation{}, err
			}
			return mutation, nil
		}
		resultRef := strings.Join([]string{"quantity", campaignID, issueRequestID, "reserved"}, ":")
		_, err = tx.Exec(ctx, `INSERT INTO coupon_quantity_reservations (campaign_id,issue_request_id,quantity,state,result_ref,reserved_at) VALUES ($1,$2,$3,'reserved',$4,$5)`, campaignID, issueRequestID, quantity, resultRef, at)
		if err != nil {
			return QuantityMutation{}, dbError("reserve_quantity", err)
		}
		newVersion := current.Version + 1
		tag, err := tx.Exec(ctx, `UPDATE coupon_campaigns SET reserved_quantity=reserved_quantity+$1, version=$2, updated_at=$3 WHERE campaign_id=$4 AND version=$5 AND reserved_quantity+confirmed_quantity+$1<=total_quantity`, quantity, newVersion, at, campaignID, expectedVersion)
		if err != nil {
			return QuantityMutation{}, dbError("reserve_quantity", err)
		}
		if tag.RowsAffected() != 1 {
			return QuantityMutation{}, ErrVersionConflict
		}
		reservation := QuantityReservation{CampaignID: campaignID, IssueRequestID: issueRequestID, Quantity: quantity, State: ReservationReserved, ResultRef: resultRef, ReservedAt: at}
		payload, _ := json.Marshal(reservation)
		if err := insertQuantityLedger(ctx, tx, campaignID, issueRequestID, "reserve", quantity, "unreserved", "reserved", resultRef, at); err != nil {
			return QuantityMutation{}, err
		}
		if err := insertOutbox(ctx, tx, command, "coupon.quantity.reserved", "EVT.A.19-32", campaignID, newVersion, payload); err != nil {
			return QuantityMutation{}, err
		}
		mutation := QuantityMutation{Mutation: Mutation{ResultRef: resultRef, ResponseSnapshot: payload}, Reservation: reservation, Version: newVersion}
		if err := finishIdempotency(ctx, tx, command, "completed", mutation.Mutation); err != nil {
			return QuantityMutation{}, err
		}
		return mutation, nil
	})
}

func issuanceBlocked(ctx context.Context, db queryer, campaignID string, at time.Time) (bool, error) {
	var blocked bool
	err := db.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM coupon_operational_controls AS control
		JOIN coupon_operational_scopes AS scope ON scope.control_id=control.control_id
		WHERE scope.scope_type='campaign' AND scope.scope_ref=$1
		  AND control.active AND control.block_issuance AND control.effective_from<=$2
	)`, campaignID, at).Scan(&blocked)
	if err != nil {
		return false, dbError("check_issuance_stop", err)
	}
	return blocked, nil
}

func (r *PostgresRepository) ConfirmQuantity(ctx context.Context, campaignID, issueRequestID string, expectedVersion int64, command Command) (QuantityMutation, error) {
	return r.decideQuantity(ctx, campaignID, issueRequestID, expectedVersion, ReservationConfirmed, "confirm", "coupon.quantity.confirmed", "EVT.A.19-34", command)
}

func (r *PostgresRepository) ReleaseQuantity(ctx context.Context, campaignID, issueRequestID string, expectedVersion int64, command Command) (QuantityMutation, error) {
	return r.decideQuantity(ctx, campaignID, issueRequestID, expectedVersion, ReservationReleased, "release", "coupon.quantity.released", "EVT.A.19-35", command)
}

func (r *PostgresRepository) decideQuantity(ctx context.Context, campaignID, issueRequestID string, expectedVersion int64, target ReservationState, transition, eventType, documentID string, command Command) (QuantityMutation, error) {
	return inTx(ctx, r.db, func(tx pgx.Tx) (QuantityMutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, aggregateType, campaignID)
		if err != nil {
			return QuantityMutation{}, err
		}
		if idem.replayed {
			return quantityIdempotencyReplay(idem.mutation)
		}
		reservation, ok, err := loadReservation(ctx, tx, campaignID, issueRequestID, true)
		if err != nil {
			return QuantityMutation{}, err
		}
		if !ok {
			return QuantityMutation{}, ErrNotFound
		}
		if reservation.State == target {
			mutation := quantityReplay(reservation)
			if err := finishIdempotency(ctx, tx, command, "completed", mutation.Mutation); err != nil {
				return QuantityMutation{}, err
			}
			return mutation, nil
		}
		if reservation.State != ReservationReserved {
			return QuantityMutation{}, ErrInvalidTransition
		}
		current, err := loadCampaign(ctx, tx, campaignID, true, nil)
		if err != nil {
			return QuantityMutation{}, err
		}
		if current.Version != expectedVersion {
			return QuantityMutation{}, ErrVersionConflict
		}
		now := command.OccurredAt
		resultRef := strings.Join([]string{"quantity", campaignID, issueRequestID, string(target)}, ":")
		tag, err := tx.Exec(ctx, `UPDATE coupon_quantity_reservations SET state=$1,result_ref=$2,decided_at=$3 WHERE campaign_id=$4 AND issue_request_id=$5 AND state='reserved'`, target, resultRef, now, campaignID, issueRequestID)
		if err != nil {
			return QuantityMutation{}, dbError(transition+"_quantity", err)
		}
		if tag.RowsAffected() != 1 {
			return QuantityMutation{}, ErrInvalidTransition
		}
		newVersion := current.Version + 1
		if target == ReservationConfirmed {
			tag, err = tx.Exec(ctx, `UPDATE coupon_campaigns SET reserved_quantity=reserved_quantity-$1,confirmed_quantity=confirmed_quantity+$1,version=$2,updated_at=$3 WHERE campaign_id=$4 AND version=$5 AND reserved_quantity>=$1`, reservation.Quantity, newVersion, now, campaignID, expectedVersion)
		} else {
			tag, err = tx.Exec(ctx, `UPDATE coupon_campaigns SET reserved_quantity=reserved_quantity-$1,version=$2,updated_at=$3 WHERE campaign_id=$4 AND version=$5 AND reserved_quantity>=$1`, reservation.Quantity, newVersion, now, campaignID, expectedVersion)
		}
		if err != nil {
			return QuantityMutation{}, dbError(transition+"_quantity", err)
		}
		if tag.RowsAffected() != 1 {
			return QuantityMutation{}, ErrVersionConflict
		}
		reservation.State = target
		reservation.ResultRef = resultRef
		reservation.DecidedAt = &now
		payload, _ := json.Marshal(reservation)
		if err := insertQuantityLedger(ctx, tx, campaignID, issueRequestID, transition, reservation.Quantity, "reserved", string(target), resultRef, now); err != nil {
			return QuantityMutation{}, err
		}
		if err := insertOutbox(ctx, tx, command, eventType, documentID, campaignID, newVersion, payload); err != nil {
			return QuantityMutation{}, err
		}
		mutation := QuantityMutation{Mutation: Mutation{ResultRef: resultRef, ResponseSnapshot: payload}, Reservation: reservation, Version: newVersion}
		if err := finishIdempotency(ctx, tx, command, "completed", mutation.Mutation); err != nil {
			return QuantityMutation{}, err
		}
		return mutation, nil
	})
}

func (r *PostgresRepository) updateCampaign(ctx context.Context, campaignID string, expectedVersion int64, command Command, eventType, documentID string, mutate func(Campaign) (Campaign, error)) (Mutation, error) {
	return inTx(ctx, r.db, func(tx pgx.Tx) (Mutation, error) {
		idem, err := acquireIdempotency(ctx, tx, command, aggregateType, campaignID)
		if err != nil || idem.replayed {
			return idem.mutation, err
		}
		current, err := loadCampaign(ctx, tx, campaignID, true, nil)
		if err != nil {
			return Mutation{}, err
		}
		if current.Version != expectedVersion {
			return Mutation{}, ErrVersionConflict
		}
		updated, err := mutate(current)
		if err != nil {
			return Mutation{}, err
		}
		newVersion := current.Version + 1
		updated.Version = newVersion
		tag, err := tx.Exec(ctx, `UPDATE coupon_campaigns SET status=$1,total_quantity=$2,per_user_limit=$3,claim_starts_at=$4,claim_ends_at=$5,approval_ref=NULLIF($6,''),version=$7,updated_at=$8 WHERE campaign_id=$9 AND version=$10`, updated.Status, updated.TotalQuantity, updated.PerUserLimit, updated.ClaimStartsAt, updated.ClaimEndsAt, updated.ApprovalRef, newVersion, command.OccurredAt, campaignID, expectedVersion)
		if err != nil {
			return Mutation{}, dbError("update_campaign", err)
		}
		if tag.RowsAffected() != 1 {
			return Mutation{}, ErrVersionConflict
		}
		payload, _ := json.Marshal(map[string]any{"campaignId": campaignID, "status": updated.Status, "approvalRef": updated.ApprovalRef, "version": newVersion})
		resultRef := strings.Join([]string{"campaign", campaignID, "version", intString(newVersion)}, ":")
		if err := insertOutbox(ctx, tx, command, eventType, documentID, campaignID, newVersion, payload); err != nil {
			return Mutation{}, err
		}
		mutation := Mutation{ResultRef: resultRef, ResponseSnapshot: payload}
		if err := finishIdempotency(ctx, tx, command, "completed", mutation); err != nil {
			return Mutation{}, err
		}
		return mutation, nil
	})
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadCampaign(ctx context.Context, db queryer, campaignID string, lock bool, effectiveAt *time.Time) (Campaign, error) {
	query := `SELECT campaign_id,display_name,COALESCE(description,''),status,starts_at,ends_at,claim_starts_at,claim_ends_at,current_policy_version,total_quantity,per_user_limit,reserved_quantity,confirmed_quantity,issuer_type,issuer_ref,funder_type,funder_ref,COALESCE(platform_share_percentage::text,''),COALESCE(approval_ref,''),owner_snapshot,COALESCE(external_business_ref,''),version,created_at,updated_at FROM coupon_campaigns WHERE campaign_id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	var c Campaign
	var claimStart, claimEnd sql.NullTime
	var issuerRef, funderRef, ownerSnapshot []byte
	var issuerType, funderType, platformShare string
	err := db.QueryRow(ctx, query, campaignID).Scan(&c.ID, &c.DisplayName, &c.Description, &c.Status, &c.StartsAt, &c.EndsAt, &claimStart, &claimEnd, &c.CurrentPolicyVersion, &c.TotalQuantity, &c.PerUserLimit, &c.ReservedQuantity, &c.ConfirmedQuantity, &issuerType, &issuerRef, &funderType, &funderRef, &platformShare, &c.ApprovalRef, &ownerSnapshot, &c.ExternalBusinessRef, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Campaign{}, ErrNotFound
		}
		return Campaign{}, dbError("get_campaign", err)
	}
	if claimStart.Valid {
		c.ClaimStartsAt = &claimStart.Time
	}
	if claimEnd.Valid {
		c.ClaimEndsAt = &claimEnd.Time
	}
	c.IssuerAndFunding = shared.IssuerAndFunding{IssuerType: issuerType, FunderType: funderType, PlatformSharePercentage: platformShare, ApprovalRef: c.ApprovalRef}
	if err := json.Unmarshal(issuerRef, &c.IssuerAndFunding.IssuerRef); err != nil {
		return Campaign{}, oops.In("coupon_campaign_repository").Code("campaign.issuer_decode_failed").Wrap(err)
	}
	if len(funderRef) > 0 {
		var ref shared.ExternalRef
		if err := json.Unmarshal(funderRef, &ref); err != nil {
			return Campaign{}, oops.In("coupon_campaign_repository").Code("campaign.funder_decode_failed").Wrap(err)
		}
		c.IssuerAndFunding.FunderRef = &ref
	}
	if err := json.Unmarshal(ownerSnapshot, &c.OwnerSnapshot); err != nil {
		return Campaign{}, oops.In("coupon_campaign_repository").Code("campaign.owner_snapshot_decode_failed").Wrap(err)
	}
	if effectiveAt != nil {
		var funding []byte
		err := db.QueryRow(ctx, `SELECT policy_version,issuer_and_funding
			FROM coupon_campaign_policy_versions
			WHERE campaign_id=$1 AND effective_at<=$2
			ORDER BY effective_at DESC,policy_version DESC LIMIT 1`, campaignID, effectiveAt.UTC()).Scan(&c.CurrentPolicyVersion, &funding)
		if errors.Is(err, pgx.ErrNoRows) {
			return Campaign{}, ErrCampaignInactive
		}
		if err != nil {
			return Campaign{}, dbError("get_effective_policy_version", err)
		}
		if err := json.Unmarshal(funding, &c.IssuerAndFunding); err != nil {
			return Campaign{}, oops.In("coupon_campaign_repository").Code("campaign.funding_decode_failed").Wrap(err)
		}
	}
	rows, err := db.Query(ctx, `SELECT benefit_id,policy_version,benefit_type,COALESCE(amount::text,''),COALESCE(percentage::text,''),COALESCE(max_discount_amount::text,''),COALESCE(currency,'') FROM coupon_benefits WHERE campaign_id=$1 AND policy_version=$2 ORDER BY benefit_id`, campaignID, c.CurrentPolicyVersion)
	if err != nil {
		return Campaign{}, dbError("list_campaign_benefits", err)
	}
	for rows.Next() {
		var b Benefit
		var amount, max string
		if err := rows.Scan(&b.ID, &b.PolicyVersion, &b.Type, &amount, &b.Percentage, &max, &b.Currency); err != nil {
			rows.Close()
			return Campaign{}, dbError("scan_campaign_benefit", err)
		}
		if amount != "" {
			b.Amount = &shared.Money{Amount: amount, Currency: b.Currency}
		}
		if max != "" {
			b.MaxDiscountAmount = &shared.Money{Amount: max, Currency: b.Currency}
		}
		c.Benefits = append(c.Benefits, b)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Campaign{}, dbError("list_campaign_benefits", err)
	}
	rows.Close()
	rows, err = db.Query(ctx, `SELECT policy_id,policy_version,target_type,target_ref,inclusion,COALESCE(condition_type,''),COALESCE(condition_value,'{}'::jsonb),effective_from,COALESCE(snapshot_label,'') FROM coupon_applicability_policies WHERE campaign_id=$1 AND policy_version=$2 ORDER BY policy_id`, campaignID, c.CurrentPolicyVersion)
	if err != nil {
		return Campaign{}, dbError("list_campaign_applicability", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p ApplicabilityPolicy
		if err := rows.Scan(&p.ID, &p.PolicyVersion, &p.TargetType, &p.TargetRef, &p.Inclusion, &p.ConditionType, &p.ConditionValue, &p.EffectiveFrom, &p.SnapshotLabel); err != nil {
			return Campaign{}, dbError("scan_campaign_applicability", err)
		}
		c.Applicability = append(c.Applicability, p)
	}
	if err := rows.Err(); err != nil {
		return Campaign{}, dbError("list_campaign_applicability", err)
	}
	return c, nil
}

func insertPolicyVersion(ctx context.Context, tx pgx.Tx, campaignID string, policy PolicyVersion) error {
	funding, err := json.Marshal(policy.IssuerAndFunding)
	if err != nil {
		return oops.In("coupon_campaign_repository").Code("campaign.funding_encode_failed").Wrap(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO coupon_campaign_policy_versions (
		campaign_id,policy_version,effective_at,issuer_and_funding
	) VALUES ($1,$2,$3,$4)`, campaignID, policy.Version, policy.EffectiveAt, funding); err != nil {
		return dbError("insert_campaign_policy_version", err)
	}
	for _, b := range policy.Benefits {
		if err := b.Validate(); err != nil {
			return err
		}
		var amount, max any
		if b.Amount != nil {
			amount = b.Amount.Amount
		}
		if b.MaxDiscountAmount != nil {
			max = b.MaxDiscountAmount.Amount
		}
		if _, err := tx.Exec(ctx, `INSERT INTO coupon_benefits (benefit_id,campaign_id,policy_version,benefit_type,amount,percentage,max_discount_amount,currency) VALUES ($1,$2,$3,$4,NULLIF($5,'')::numeric,NULLIF($6,'')::numeric,NULLIF($7,'')::numeric,NULLIF($8,''))`, b.ID, campaignID, b.PolicyVersion, b.Type, stringValue(amount), b.Percentage, stringValue(max), b.Currency); err != nil {
			return dbError("insert_campaign_benefit", err)
		}
	}
	for _, p := range policy.Applicability {
		if err := p.Validate(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO coupon_applicability_policies (policy_id,campaign_id,policy_version,target_type,target_ref,inclusion,condition_type,condition_value,effective_from,snapshot_label) VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,NULLIF($10,''))`, p.ID, campaignID, p.PolicyVersion, p.TargetType, p.TargetRef, p.Inclusion, p.ConditionType, p.ConditionValue, p.EffectiveFrom, p.SnapshotLabel); err != nil {
			return dbError("insert_campaign_applicability", err)
		}
	}
	return nil
}

func loadReservation(ctx context.Context, tx pgx.Tx, campaignID, issueRequestID string, lock bool) (QuantityReservation, bool, error) {
	query := `SELECT campaign_id,issue_request_id,quantity,state,result_ref,reserved_at,decided_at FROM coupon_quantity_reservations WHERE campaign_id=$1 AND issue_request_id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var reservation QuantityReservation
	var decided sql.NullTime
	err := tx.QueryRow(ctx, query, campaignID, issueRequestID).Scan(&reservation.CampaignID, &reservation.IssueRequestID, &reservation.Quantity, &reservation.State, &reservation.ResultRef, &reservation.ReservedAt, &decided)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuantityReservation{}, false, nil
	}
	if err != nil {
		return QuantityReservation{}, false, dbError("get_quantity_reservation", err)
	}
	if decided.Valid {
		reservation.DecidedAt = &decided.Time
	}
	return reservation, true, nil
}

func insertQuantityLedger(ctx context.Context, tx pgx.Tx, campaignID, issueRequestID, transition string, quantity int64, before, after, resultRef string, occurredAt time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO coupon_quantity_ledger (ledger_id,campaign_id,issue_request_id,transition,quantity,before_state,after_state,result_ref,occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, uuid.New(), campaignID, issueRequestID, transition, quantity, before, after, resultRef, occurredAt)
	if err != nil {
		return dbError("insert_quantity_ledger", err)
	}
	return nil
}

type idempotencyResult struct {
	mutation Mutation
	replayed bool
}

func acquireIdempotency(ctx context.Context, tx pgx.Tx, command Command, ownerType, ownerID string) (idempotencyResult, error) {
	if strings.TrimSpace(command.OperationType) == "" || strings.TrimSpace(command.BusinessKey) == "" || strings.TrimSpace(command.RequestHash) == "" || strings.TrimSpace(command.CorrelationID) == "" || command.OccurredAt.IsZero() ||
		!command.LeaseUntil.After(command.OccurredAt) || !command.ExpiresAt.After(command.LeaseUntil) {
		return idempotencyResult{}, oops.In("coupon_campaign_repository").Code("campaign.command_invalid").New("command idempotency, correlation, lease, and expiry fields are required")
	}
	digest := sha256.Sum256([]byte(command.RequestHash))
	tag, err := tx.Exec(ctx, `INSERT INTO coupon_idempotency_records (operation_type,business_key,owner_type,owner_id,request_hash,status,locked_until,expires_at) VALUES ($1,$2,$3,$4,$5,'processing',$6,$7) ON CONFLICT DO NOTHING`, command.OperationType, command.BusinessKey, ownerType, ownerID, digest[:], command.LeaseUntil, command.ExpiresAt)
	if err != nil {
		return idempotencyResult{}, dbError("claim_idempotency", err)
	}
	if tag.RowsAffected() == 1 {
		return idempotencyResult{}, nil
	}
	var storedHash, snapshot []byte
	var status string
	var resultRef sql.NullString
	var lockedUntil sql.NullTime
	err = tx.QueryRow(ctx, `SELECT request_hash,status,result_ref,response_snapshot,locked_until FROM coupon_idempotency_records WHERE operation_type=$1 AND business_key=$2 FOR UPDATE`, command.OperationType, command.BusinessKey).Scan(&storedHash, &status, &resultRef, &snapshot, &lockedUntil)
	if err != nil {
		return idempotencyResult{}, dbError("read_idempotency", err)
	}
	if !bytes.Equal(storedHash, digest[:]) {
		return idempotencyResult{}, ErrIdempotencyConflict
	}
	if status == "processing" {
		if lockedUntil.Valid && lockedUntil.Time.After(command.OccurredAt) {
			return idempotencyResult{}, ErrCommandInProgress
		}
		tag, err = tx.Exec(ctx, `UPDATE coupon_idempotency_records SET owner_type=$3,owner_id=$4,locked_until=$5,expires_at=$6,updated_at=$7 WHERE operation_type=$1 AND business_key=$2 AND status='processing'`, command.OperationType, command.BusinessKey, ownerType, ownerID, command.LeaseUntil, command.ExpiresAt, command.OccurredAt)
		if err != nil {
			return idempotencyResult{}, dbError("resume_idempotency", err)
		}
		if tag.RowsAffected() != 1 {
			return idempotencyResult{}, ErrCommandInProgress
		}
		return idempotencyResult{}, nil
	}
	return idempotencyResult{mutation: Mutation{ResultRef: resultRef.String, ResponseSnapshot: snapshot, Replayed: true}, replayed: true}, nil
}

func finishIdempotency(ctx context.Context, tx pgx.Tx, command Command, status string, mutation Mutation) error {
	digest := sha256.Sum256([]byte(command.RequestHash))
	tag, err := tx.Exec(ctx, `UPDATE coupon_idempotency_records SET status=$1,result_ref=$2,response_snapshot=$3,locked_until=NULL,completed_at=$4,updated_at=$4 WHERE operation_type=$5 AND business_key=$6 AND request_hash=$7 AND status='processing'`, status, mutation.ResultRef, mutation.ResponseSnapshot, command.OccurredAt, command.OperationType, command.BusinessKey, digest[:])
	if err != nil {
		return dbError("finish_idempotency", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrCommandInProgress
	}
	return nil
}

func insertOutbox(ctx context.Context, tx pgx.Tx, command Command, eventType, documentID, aggregateID string, aggregateVersion int64, payload []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO domain_outbox (event_id,event_type,event_document_id,payload_schema_version,aggregate_type,aggregate_id,aggregate_version,correlation_id,causation_id,trace_id,payload,occurred_at) VALUES ($1,$2,$3,1,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11)`, uuid.New(), eventType, documentID, aggregateType, aggregateID, aggregateVersion, command.CorrelationID, command.CausationID, command.TraceID, payload, command.OccurredAt)
	if err != nil {
		return dbError("insert_outbox", err)
	}
	return nil
}

func campaignJSON(c Campaign) ([]byte, []byte, []byte, error) {
	issuer, err := json.Marshal(c.IssuerAndFunding.IssuerRef)
	if err != nil {
		return nil, nil, nil, oops.In("coupon_campaign_repository").Code("campaign.issuer_encode_failed").Wrap(err)
	}
	var funder []byte
	if c.IssuerAndFunding.FunderRef != nil {
		funder, err = json.Marshal(c.IssuerAndFunding.FunderRef)
		if err != nil {
			return nil, nil, nil, oops.In("coupon_campaign_repository").Code("campaign.funder_encode_failed").Wrap(err)
		}
	}
	owner, err := json.Marshal(c.OwnerSnapshot)
	if err != nil {
		return nil, nil, nil, oops.In("coupon_campaign_repository").Code("campaign.owner_snapshot_encode_failed").Wrap(err)
	}
	return issuer, funder, owner, nil
}

func quantityReplay(reservation QuantityReservation) QuantityMutation {
	payload, _ := json.Marshal(reservation)
	return QuantityMutation{Mutation: Mutation{ResultRef: reservation.ResultRef, ResponseSnapshot: payload, Replayed: true}, Reservation: reservation}
}

func quantityIdempotencyReplay(mutation Mutation) (QuantityMutation, error) {
	var reservation QuantityReservation
	if err := json.Unmarshal(mutation.ResponseSnapshot, &reservation); err != nil {
		return QuantityMutation{}, oops.In("coupon_campaign_repository").Code("campaign.snapshot_decode_failed").Wrap(err)
	}
	result := QuantityMutation{Mutation: mutation, Reservation: reservation}
	if reservation.CampaignID != "" {
		return result, nil
	}
	var rejected struct {
		CampaignID     string `json:"campaignId"`
		IssueRequestID string `json:"issueRequestId"`
		Quantity       int64  `json:"quantity"`
		ReasonCode     string `json:"reasonCode"`
		ResultRef      string `json:"resultRef"`
	}
	if err := json.Unmarshal(mutation.ResponseSnapshot, &rejected); err != nil {
		return QuantityMutation{}, oops.In("coupon_campaign_repository").Code("campaign.snapshot_decode_failed").Wrap(err)
	}
	if rejected.CampaignID == "" || rejected.IssueRequestID == "" {
		return QuantityMutation{}, oops.In("coupon_campaign_repository").Code("campaign.snapshot_decode_failed").New("quantity result snapshot is incomplete")
	}
	result.Reservation = QuantityReservation{CampaignID: rejected.CampaignID, IssueRequestID: rejected.IssueRequestID, Quantity: rejected.Quantity, State: ReservationRejected, ResultRef: rejected.ResultRef}
	result.Rejected = true
	result.ReasonCode = rejected.ReasonCode
	return result, nil
}

func reviewEvent(decision Status) string {
	switch decision {
	case StatusApproved:
		return "coupon.approved"
	case StatusRejected:
		return "coupon.rejected"
	default:
		return "coupon.review.held"
	}
}

func reviewDocument(decision Status) string {
	switch decision {
	case StatusApproved:
		return "EVT.A.19-03"
	case StatusRejected:
		return "EVT.A.19-04"
	default:
		return "EVT.A.19-05"
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, ErrCampaignInactive):
		return "campaign_inactive"
	case errors.Is(err, ErrIssuanceBlocked):
		return "issuance_blocked"
	case errors.Is(err, ErrQuantityUnavailable):
		return "quantity_unavailable"
	default:
		return "quantity_invalid"
	}
}

func intString(value int64) string {
	return strconv.FormatInt(value, 10)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func dbError(operation string, err error) error {
	return oops.In("coupon_campaign_repository").Code("campaign.database_failed").With("operation", operation).Wrap(err)
}

func inTx[T any](ctx context.Context, db *pgxpool.Pool, run func(pgx.Tx) (T, error)) (result T, err error) {
	tx, beginErr := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if beginErr != nil {
		return result, dbError("begin_transaction", beginErr)
	}
	committed := false
	defer func() {
		if !committed {
			err = oops.Join(err, tx.Rollback(context.WithoutCancel(ctx)))
		}
	}()
	result, err = run(tx)
	if err != nil {
		return result, err
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return result, dbError("commit_transaction", commitErr)
	}
	committed = true
	return result, nil
}
