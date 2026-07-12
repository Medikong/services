package shared

import (
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/samber/oops"
)

var (
	currencyPattern  = regexp.MustCompile(`^[A-Z]{3}$`)
	opaqueRefPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)
)

type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (m Money) Validate() error {
	amount := new(big.Rat)
	if _, ok := amount.SetString(m.Amount); !ok || amount.Sign() < 0 {
		return oops.In("coupon_value").Code("coupon.money_invalid").New("money amount must be a non-negative decimal")
	}
	if !currencyPattern.MatchString(m.Currency) {
		return oops.In("coupon_value").Code("coupon.currency_invalid").New("money currency must be an ISO-style uppercase code")
	}
	return nil
}

type ExternalRef struct {
	Context string `json:"context"`
	Type    string `json:"type"`
	ID      string `json:"id"`
}

func (r ExternalRef) Validate() error {
	if strings.TrimSpace(r.Context) == "" || strings.TrimSpace(r.Type) == "" || !opaqueRefPattern.MatchString(r.ID) {
		return oops.In("coupon_value").Code("coupon.external_ref_invalid").New("external reference is incomplete or malformed")
	}
	return nil
}

type SnapshotRef struct {
	SourceRef     ExternalRef `json:"sourceRef"`
	SourceVersion string      `json:"sourceVersion"`
	CapturedAt    time.Time   `json:"capturedAt"`
	PayloadHash   string      `json:"payloadHash"`
}

func (r SnapshotRef) Validate() error {
	if err := r.SourceRef.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.SourceVersion) == "" || r.CapturedAt.IsZero() || !strings.HasPrefix(r.PayloadHash, "sha256:") || len(r.PayloadHash) != 50 {
		return oops.In("coupon_value").Code("coupon.snapshot_ref_invalid").New("snapshot reference is incomplete or malformed")
	}
	return nil
}

type IssuerAndFunding struct {
	IssuerType              string       `json:"issuerType"`
	IssuerRef               ExternalRef  `json:"issuerRef"`
	FunderType              string       `json:"funderType"`
	FunderRef               *ExternalRef `json:"funderRef,omitempty"`
	PlatformSharePercentage string       `json:"platformSharePercentage,omitempty"`
	ApprovalRef             string       `json:"approvalRef,omitempty"`
}

func (v IssuerAndFunding) Validate() error {
	if err := v.IssuerRef.Validate(); err != nil {
		return err
	}
	if v.FunderRef != nil {
		if err := v.FunderRef.Validate(); err != nil {
			return err
		}
	}
	switch v.IssuerType {
	case "platform", "seller", "partnership", "compensation":
	default:
		return oops.In("coupon_value").Code("coupon.issuer_type_invalid").New("issuer type is not supported")
	}
	switch v.FunderType {
	case "seller":
		if v.FunderRef == nil {
			return oops.In("coupon_value").Code("coupon.funder_ref_required").New("seller funding requires a funder reference")
		}
	case "joint":
		if v.FunderRef == nil || strings.TrimSpace(v.PlatformSharePercentage) == "" {
			return oops.In("coupon_value").Code("coupon.joint_funding_invalid").New("joint funding requires a funder reference and platform share")
		}
		share := new(big.Rat)
		if _, ok := share.SetString(v.PlatformSharePercentage); !ok || share.Sign() < 0 || share.Cmp(big.NewRat(100, 1)) > 0 {
			return oops.In("coupon_value").Code("coupon.joint_funding_invalid").New("joint funding platform share must be between zero and one hundred")
		}
	case "platform", "compensation":
		if strings.TrimSpace(v.PlatformSharePercentage) != "" {
			return oops.In("coupon_value").Code("coupon.funding_share_unexpected").New("platform share is only valid for joint funding")
		}
	default:
		return oops.In("coupon_value").Code("coupon.funder_type_invalid").New("funder type is not supported")
	}
	return nil
}
