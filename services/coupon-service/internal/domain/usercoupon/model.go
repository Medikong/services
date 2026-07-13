package usercoupon

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/samber/oops"
)

type Status string

const (
	StatusGranted Status = "granted"
	StatusExpired Status = "expired"
	StatusRevoked Status = "revoked"
)

type Coupon struct {
	ID             string          `json:"userCouponId"`
	CampaignID     string          `json:"campaignId"`
	PolicyVersion  int64           `json:"policyVersion"`
	UserID         string          `json:"userId"`
	IssueRequestID string          `json:"issueRequestId"`
	Status         Status          `json:"status"`
	UsableFrom     time.Time       `json:"usableFrom"`
	ExpiresAt      time.Time       `json:"expiresAt"`
	GrantSnapshot  json.RawMessage `json:"grantSnapshot"`
	ResultRef      string          `json:"resultRef"`
	Version        int64           `json:"version"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

func (c Coupon) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.CampaignID) == "" || c.PolicyVersion < 1 || strings.TrimSpace(c.UserID) == "" || strings.TrimSpace(c.IssueRequestID) == "" || c.UsableFrom.IsZero() || !c.UsableFrom.Before(c.ExpiresAt) || !json.Valid(c.GrantSnapshot) || strings.TrimSpace(c.ResultRef) == "" || c.Version < 0 {
		return oops.In("user_coupon").Code("user_coupon.invalid").New("user coupon identity, validity, snapshot, and result are required")
	}
	switch c.Status {
	case StatusGranted, StatusExpired, StatusRevoked:
		return nil
	default:
		return oops.In("user_coupon").Code("user_coupon.status_invalid").New("user coupon status is not supported")
	}
}

func (c Coupon) Expire(asOf time.Time) (Coupon, error) {
	if c.Status == StatusExpired {
		return c, nil
	}
	if c.Status != StatusGranted || asOf.Before(c.ExpiresAt) {
		return Coupon{}, ErrInvalidTransition
	}
	c.Status = StatusExpired
	c.Version++
	return c, nil
}

var (
	ErrNotFound             = oops.In("user_coupon").Code("user_coupon.not_found").New("user coupon was not found")
	ErrIssueRequestConflict = oops.In("user_coupon").Code("user_coupon.issue_request_conflict").New("issue request already produced a different user coupon")
	ErrInvalidTransition    = oops.In("user_coupon").Code("user_coupon.transition_invalid").New("user coupon transition is not allowed")
	ErrVersionConflict      = oops.In("user_coupon").Code("user_coupon.version_conflict").New("user coupon version does not match")
	ErrIdempotencyConflict  = oops.In("user_coupon").Code("user_coupon.idempotency_conflict").New("idempotency key was reused with a different request")
	ErrCommandInProgress    = oops.In("user_coupon").Code("user_coupon.command_in_progress").New("the same command is already processing")
)
