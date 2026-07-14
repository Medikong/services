package app

import (
	"encoding/json"
	"time"

	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/domain/campaign"
	"github.com/Medikong/services/services/coupon-service/internal/domain/readmodel"
	"github.com/Medikong/services/services/coupon-service/internal/domain/shared"
	couponhttp "github.com/Medikong/services/services/coupon-service/internal/transport/http"
)

func domainExternal(value couponhttp.ExternalRef) shared.ExternalRef {
	return shared.ExternalRef{Context: value.Context, Type: value.Type, ID: value.ID}
}

func transportExternal(value shared.ExternalRef) couponhttp.ExternalRef {
	return couponhttp.ExternalRef{Context: value.Context, Type: value.Type, ID: value.ID}
}

func domainSnapshot(value couponhttp.SnapshotRef) (shared.SnapshotRef, error) {
	capturedAt, err := parseTime(value.CapturedAt)
	if err != nil {
		return shared.SnapshotRef{}, err
	}
	return shared.SnapshotRef{
		SourceRef: domainExternal(value.SourceRef), SourceVersion: value.SourceVersion,
		CapturedAt: capturedAt, PayloadHash: value.PayloadHash,
	}, nil
}

func transportSnapshot(value shared.SnapshotRef) couponhttp.SnapshotRef {
	return couponhttp.SnapshotRef{
		SourceRef: transportExternal(value.SourceRef), SourceVersion: value.SourceVersion,
		CapturedAt: value.CapturedAt.UTC().Format(time.RFC3339Nano), PayloadHash: value.PayloadHash,
	}
}

func domainMoney(value couponhttp.Money) shared.Money {
	return shared.Money{Amount: value.Amount, Currency: value.Currency}
}

func transportMoney(value shared.Money) couponhttp.Money {
	return couponhttp.Money{Amount: value.Amount, Currency: value.Currency}
}

func campaignBenefit(value couponhttp.Benefit) campaign.Benefit {
	result := campaign.Benefit{Type: campaign.BenefitType(value.Type), Percentage: value.Percentage}
	if value.Amount != nil {
		money := domainMoney(*value.Amount)
		result.Amount = &money
		result.Currency = money.Currency
	}
	if value.MaxDiscountAmount != nil {
		money := domainMoney(*value.MaxDiscountAmount)
		result.MaxDiscountAmount = &money
		result.Currency = money.Currency
	}
	return result
}

func campaignApplicability(value couponhttp.ApplicabilityPolicy, effectiveAt time.Time) ([]campaign.ApplicabilityPolicy, error) {
	conditionType := "all"
	if value.MinimumOrderAmount != nil {
		conditionType = "minimum_order_amount"
	}
	condition, err := json.Marshal(struct {
		MinimumOrderAmount *shared.Money `json:"minimumOrderAmount,omitempty"`
		StackingPolicyRef  string        `json:"stackingPolicyRef,omitempty"`
	}{
		MinimumOrderAmount: optionalMoney(value.MinimumOrderAmount),
		StackingPolicyRef:  value.StackingPolicyRef,
	})
	if err != nil {
		return nil, oops.In("coupon_http_backend").Code("coupon.applicability_encode_failed").Wrap(err)
	}
	type target struct {
		ref       couponhttp.ExternalRef
		inclusion string
	}
	targets := make([]target, 0, len(value.IncludeTargets)+len(value.ExcludeTargets)+1)
	if len(value.IncludeTargets) == 0 {
		targets = append(targets, target{ref: couponhttp.ExternalRef{Context: "coupon", Type: "all", ID: "all"}, inclusion: "include"})
	}
	for _, ref := range value.IncludeTargets {
		targets = append(targets, target{ref: ref, inclusion: "include"})
	}
	for _, ref := range value.ExcludeTargets {
		targets = append(targets, target{ref: ref, inclusion: "exclude"})
	}
	result := make([]campaign.ApplicabilityPolicy, 0, len(targets))
	for _, item := range targets {
		result = append(result, campaign.ApplicabilityPolicy{
			TargetType: item.ref.Type, TargetRef: item.ref.ID, Inclusion: item.inclusion,
			ConditionType: conditionType, ConditionValue: append(json.RawMessage(nil), condition...),
			EffectiveFrom: effectiveAt,
		})
	}
	return result, nil
}

func optionalMoney(value *couponhttp.Money) *shared.Money {
	if value == nil {
		return nil
	}
	money := domainMoney(*value)
	return &money
}

func campaignFunding(value couponhttp.IssuerAndFunding) shared.IssuerAndFunding {
	result := shared.IssuerAndFunding{
		IssuerType: value.IssuerType, IssuerRef: domainExternal(value.IssuerRef),
		FunderType: value.FunderType, PlatformSharePercentage: value.PlatformSharePercentage,
	}
	if value.FunderRef != nil {
		ref := domainExternal(*value.FunderRef)
		result.FunderRef = &ref
	}
	return result
}

func readExternal(value readmodel.ExternalRef) couponhttp.ExternalRef {
	return couponhttp.ExternalRef{Context: value.Context, Type: value.Type, ID: value.ID}
}

func readMoney(value readmodel.Money) couponhttp.Money {
	return couponhttp.Money{Amount: value.Amount, Currency: value.Currency}
}

func readBenefit(value readmodel.Benefit) couponhttp.Benefit {
	result := couponhttp.Benefit{Type: value.Type, Percentage: value.Percentage}
	if value.Amount != nil {
		money := readMoney(*value.Amount)
		result.Amount = &money
	}
	if value.MaxDiscountAmount != nil {
		money := readMoney(*value.MaxDiscountAmount)
		result.MaxDiscountAmount = &money
	}
	return result
}

func readApplicability(value readmodel.ApplicabilityPolicy) couponhttp.ApplicabilityPolicy {
	version := value.PolicySchemaVersion
	result := couponhttp.ApplicabilityPolicy{
		PolicySchemaVersion: &version, StackingPolicyRef: value.StackingPolicyRef,
		IncludeTargets: make([]couponhttp.ExternalRef, 0, len(value.IncludeTargets)),
		ExcludeTargets: make([]couponhttp.ExternalRef, 0, len(value.ExcludeTargets)),
	}
	for _, ref := range value.IncludeTargets {
		result.IncludeTargets = append(result.IncludeTargets, readExternal(ref))
	}
	for _, ref := range value.ExcludeTargets {
		result.ExcludeTargets = append(result.ExcludeTargets, readExternal(ref))
	}
	if value.MinimumOrderAmount != nil {
		money := readMoney(*value.MinimumOrderAmount)
		result.MinimumOrderAmount = &money
	}
	return result
}

func readFunding(value readmodel.IssuerAndFunding) couponhttp.IssuerAndFunding {
	result := couponhttp.IssuerAndFunding{
		IssuerType: value.IssuerType, IssuerRef: readExternal(value.IssuerRef),
		FunderType: value.FunderType, PlatformSharePercentage: value.PlatformSharePercentage,
	}
	if value.FunderRef != nil {
		ref := readExternal(*value.FunderRef)
		result.FunderRef = &ref
	}
	return result
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, oops.In("coupon_http_backend").Code("coupon.time_invalid").Wrap(err)
	}
	return parsed.UTC(), nil
}
