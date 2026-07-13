package couponcode

import (
	"crypto/hmac"
	"crypto/sha256"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/samber/oops"
)

const (
	HashAlgorithmHMACSHA256 int16 = 1
	NormalizationV1         int16 = 1
)

type BatchStatus string

const (
	BatchDraft   BatchStatus = "draft"
	BatchActive  BatchStatus = "active"
	BatchClosed  BatchStatus = "closed"
	BatchRevoked BatchStatus = "revoked"
)

type CodeStatus string

const (
	CodeAvailable CodeStatus = "available"
	CodeReserved  CodeStatus = "reserved"
	CodeRedeemed  CodeStatus = "redeemed"
	CodeExpired   CodeStatus = "expired"
	CodeDiscarded CodeStatus = "discarded"
)

type Batch struct {
	ID                  string      `json:"codeBatchId"`
	CampaignID          string      `json:"campaignId"`
	Status              BatchStatus `json:"status"`
	Format              string      `json:"format"`
	Quantity            int64       `json:"quantity"`
	CreatedCount        int64       `json:"createdCount"`
	DistributionChannel string      `json:"distributionChannel"`
	CreatorRef          string      `json:"creatorRef"`
	Version             int64       `json:"version"`
	CreatedAt           time.Time   `json:"createdAt"`
	UpdatedAt           time.Time   `json:"updatedAt"`
}

func (b Batch) Validate() error {
	if strings.TrimSpace(b.ID) == "" || strings.TrimSpace(b.CampaignID) == "" || strings.TrimSpace(b.Format) == "" || strings.TrimSpace(b.DistributionChannel) == "" || strings.TrimSpace(b.CreatorRef) == "" || b.Quantity < 0 || b.CreatedCount < 0 || b.CreatedCount > b.Quantity || b.Version < 0 {
		return oops.In("coupon_code").Code("coupon_code.batch_invalid").New("coupon code batch is incomplete or inconsistent")
	}
	switch b.Status {
	case BatchDraft, BatchActive, BatchClosed, BatchRevoked:
		return nil
	default:
		return oops.In("coupon_code").Code("coupon_code.batch_status_invalid").New("coupon code batch status is not supported")
	}
}

type Code struct {
	ID                     string     `json:"codeId"`
	BatchID                string     `json:"codeBatchId"`
	CampaignID             string     `json:"campaignId"`
	Hash                   []byte     `json:"-"`
	Suffix                 string     `json:"codeSuffix"`
	HashAlgorithmVersion   int16      `json:"hashAlgorithmVersion"`
	NormalizationVersion   int16      `json:"normalizationVersion"`
	Status                 CodeStatus `json:"status"`
	ReservedIssueRequestID string     `json:"reservedIssueRequestId,omitempty"`
	ReservedUntil          *time.Time `json:"reservedUntil,omitempty"`
	RedeemedUserCouponID   string     `json:"redeemedUserCouponId,omitempty"`
	RedeemedAt             *time.Time `json:"redeemedAt,omitempty"`
	Version                int64      `json:"version"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
}

func (c Code) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.BatchID) == "" || strings.TrimSpace(c.CampaignID) == "" || len(c.Hash) != sha256.Size || c.HashAlgorithmVersion < 1 || c.NormalizationVersion < 1 || c.Version < 0 {
		return oops.In("coupon_code").Code("coupon_code.invalid").New("coupon code fingerprint and identity are required")
	}
	switch c.Status {
	case CodeAvailable:
		if c.ReservedIssueRequestID != "" || c.ReservedUntil != nil || c.RedeemedUserCouponID != "" || c.RedeemedAt != nil {
			return ErrInvalidTransition
		}
	case CodeReserved:
		if c.ReservedIssueRequestID == "" || c.ReservedUntil == nil || c.RedeemedUserCouponID != "" || c.RedeemedAt != nil {
			return ErrInvalidTransition
		}
	case CodeRedeemed:
		if c.ReservedIssueRequestID == "" || c.RedeemedUserCouponID == "" || c.RedeemedAt == nil {
			return ErrInvalidTransition
		}
	case CodeExpired, CodeDiscarded:
	default:
		return oops.In("coupon_code").Code("coupon_code.status_invalid").New("coupon code status is not supported")
	}
	return nil
}

// Fingerprint returns only a keyed digest and a short display suffix. The
// normalized source is deliberately not returned so callers cannot persist it.
func Fingerprint(raw string, key []byte) (digest []byte, suffix string, err error) {
	if len(key) < 32 {
		return nil, "", oops.In("coupon_code").Code("coupon_code.hash_key_invalid").New("coupon code hash key must be at least 32 bytes")
	}
	if !utf8.ValidString(raw) {
		return nil, "", oops.In("coupon_code").Code("coupon_code.input_invalid").New("coupon code encoding is invalid")
	}
	normalized := normalize(raw)
	length := utf8.RuneCountInString(normalized)
	if length < 4 || length > 128 {
		return nil, "", oops.In("coupon_code").Code("coupon_code.input_invalid").New("coupon code length is invalid")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(normalized))
	digest = mac.Sum(nil)
	runes := []rune(normalized)
	start := len(runes) - 4
	if start < 0 {
		start = 0
	}
	return digest, string(runes[start:]), nil
}

func normalize(raw string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, strings.TrimSpace(raw))
}

func (c Code) Reserve(issueRequestID string, until, now time.Time) (Code, error) {
	if issueRequestID == "" || !until.After(now) {
		return Code{}, ErrInvalidTransition
	}
	if c.Status == CodeReserved && c.ReservedIssueRequestID == issueRequestID {
		return c, nil
	}
	if c.Status != CodeAvailable {
		return Code{}, ErrCodeUnavailable
	}
	c.Status = CodeReserved
	c.ReservedIssueRequestID = issueRequestID
	c.ReservedUntil = &until
	return c, nil
}

func (c Code) Redeem(issueRequestID, userCouponID string, now time.Time) (Code, error) {
	if c.Status == CodeRedeemed && c.ReservedIssueRequestID == issueRequestID && c.RedeemedUserCouponID == userCouponID {
		return c, nil
	}
	if c.Status != CodeReserved || c.ReservedIssueRequestID != issueRequestID || userCouponID == "" {
		return Code{}, ErrInvalidTransition
	}
	c.Status = CodeRedeemed
	c.RedeemedUserCouponID = userCouponID
	c.RedeemedAt = &now
	return c, nil
}

func (c Code) Release(issueRequestID string) (Code, error) {
	if c.Status == CodeAvailable && c.ReservedIssueRequestID == "" {
		return c, nil
	}
	if c.Status != CodeReserved || c.ReservedIssueRequestID != issueRequestID {
		return Code{}, ErrInvalidTransition
	}
	c.Status = CodeAvailable
	c.ReservedIssueRequestID = ""
	c.ReservedUntil = nil
	return c, nil
}

var (
	ErrNotFound            = oops.In("coupon_code").Code("coupon_code.not_found").New("coupon code was not found")
	ErrCodeUnavailable     = oops.In("coupon_code").Code("coupon_code.unavailable").New("coupon code is unavailable")
	ErrInvalidTransition   = oops.In("coupon_code").Code("coupon_code.transition_invalid").New("coupon code transition is not allowed")
	ErrVersionConflict     = oops.In("coupon_code").Code("coupon_code.version_conflict").New("coupon code batch version does not match")
	ErrIdempotencyConflict = oops.In("coupon_code").Code("coupon_code.idempotency_conflict").New("idempotency key was reused with a different request")
	ErrCommandInProgress   = oops.In("coupon_code").Code("coupon_code.command_in_progress").New("the same command is already processing")
)
