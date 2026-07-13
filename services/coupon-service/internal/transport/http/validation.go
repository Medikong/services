package http

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

var (
	campaignIDPattern   = regexp.MustCompile(`^camp_[A-Za-z0-9_-]{8,120}$`)
	userCouponIDPattern = regexp.MustCompile(`^ucpn_[A-Za-z0-9_-]{8,120}$`)
	redemptionIDPattern = regexp.MustCompile(`^redm_[A-Za-z0-9_-]{8,120}$`)
	bulkJobIDPattern    = regexp.MustCompile(`^bjob_[A-Za-z0-9_-]{8,120}$`)
	controlIDPattern    = regexp.MustCompile(`^ctrl_[A-Za-z0-9_-]{8,120}$`)
	recoveryIDPattern   = regexp.MustCompile(`^rcvy_[A-Za-z0-9_-]{8,120}$`)
	opaqueRefPattern    = regexp.MustCompile(`^[A-Za-z0-9._:/-]+$`)
	payloadHashPattern  = regexp.MustCompile(`^sha256:[A-Za-z0-9_-]{43}$`)
	moneyPattern        = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,4})?$`)
	currencyPattern     = regexp.MustCompile(`^[A-Z]{3}$`)
)

func validateBody(body any) *httpcontract.Error {
	switch value := body.(type) {
	case *RedeemCouponCodeRequest:
		return stringRange("code", value.Code, 4, 128)
	case *ValidateCouponRequest:
		if !userCouponIDPattern.MatchString(value.UserCouponID) {
			return invalid("userCouponId", "invalid_format")
		}
		if value.PolicyVersion == nil || *value.PolicyVersion < 1 {
			return invalid("policyVersion", "required_positive_integer")
		}
		if problem := validateOrderSnapshot(value.OrderSnapshot); problem != nil {
			return problem
		}
		return optionalOpaqueRef("stackingPolicyRef", value.StackingPolicyRef)
	case *ExpectedVersionRequest:
		return nonNegativeVersion(value.ExpectedVersion)
	case *RedemptionTransitionRequest:
		if problem := nonNegativeVersion(value.ExpectedVersion); problem != nil {
			return problem
		}
		if problem := validateExternalRef("resultRef", value.ResultRef); problem != nil {
			return problem
		}
		if value.ResultSnapshot != nil {
			if problem := validateSnapshot("resultSnapshot", *value.ResultSnapshot); problem != nil {
				return problem
			}
		}
		return stringRange("reasonCode", value.ReasonCode, 1, 80)
	case *CreateCouponCampaignRequest:
		if problem := stringRange("displayName", value.DisplayName, 1, 120); problem != nil {
			return problem
		}
		if !utf8.ValidString(value.Description) || utf8.RuneCountInString(value.Description) > 1000 {
			return invalid("description", "too_long")
		}
		if problem := validateBenefit(value.Benefit); problem != nil {
			return problem
		}
		if problem := validateApplicability(value.Applicability); problem != nil {
			return problem
		}
		if problem := validateIssuerAndFunding(value.IssuerAndFunding); problem != nil {
			return problem
		}
		if problem := dateTime("usableFrom", value.UsableFrom); problem != nil {
			return problem
		}
		if problem := dateTime("expiresAt", value.ExpiresAt); problem != nil {
			return problem
		}
		if problem := validateSnapshot("ownerSnapshot", value.OwnerSnapshot); problem != nil {
			return problem
		}
		return optionalOpaqueRef("externalBusinessRef", value.ExternalBusinessRef)
	case *ConfigureIssuanceRequest:
		if problem := nonNegativeVersion(value.ExpectedVersion); problem != nil {
			return problem
		}
		if value.TotalQuantity == nil || *value.TotalQuantity < 1 || *value.TotalQuantity > 1_000_000_000 {
			return invalid("totalQuantity", "out_of_range")
		}
		if value.PerUserLimit == nil || *value.PerUserLimit < 1 || *value.PerUserLimit > 10_000 {
			return invalid("perUserLimit", "out_of_range")
		}
		if problem := dateTime("claimStartsAt", value.ClaimStartsAt); problem != nil {
			return problem
		}
		return dateTime("claimEndsAt", value.ClaimEndsAt)
	case *ReviewCampaignRequest:
		if problem := nonNegativeVersion(value.ExpectedVersion); problem != nil {
			return problem
		}
		if !oneOf(value.Decision, "approved", "rejected", "held") {
			return invalid("decision", "invalid_enum")
		}
		if problem := stringRange("reasonCode", value.ReasonCode, 1, 80); problem != nil {
			return problem
		}
		return validateSnapshot("sellerOwnershipSnapshot", value.SellerOwnershipSnapshot)
	case *CreatePolicyVersionRequest:
		if problem := nonNegativeVersion(value.ExpectedVersion); problem != nil {
			return problem
		}
		if problem := dateTime("effectiveAt", value.EffectiveAt); problem != nil {
			return problem
		}
		if value.Benefit == nil && value.Applicability == nil && value.IssuerAndFunding == nil {
			return invalid("body", "min_properties")
		}
		if value.Benefit != nil {
			if problem := validateBenefit(*value.Benefit); problem != nil {
				return problem
			}
		}
		if value.Applicability != nil {
			if problem := validateApplicability(*value.Applicability); problem != nil {
				return problem
			}
		}
		if value.IssuerAndFunding != nil {
			return validateIssuerAndFunding(*value.IssuerAndFunding)
		}
		return nil
	case *CreateBulkIssueJobRequest:
		if !campaignIDPattern.MatchString(value.CampaignID) {
			return invalid("campaignId", "invalid_format")
		}
		if problem := validateSnapshot("audienceSnapshot", value.AudienceSnapshot); problem != nil {
			return problem
		}
		if problem := dateTime("evaluationAsOf", value.EvaluationAsOf); problem != nil {
			return problem
		}
		return validateExternalRef("operationTaskRef", value.OperationTaskRef)
	case *ApplyOperationalControlRequest:
		if !oneOf(value.Scope.Type, "campaign", "drop", "user_group") {
			return invalid("scope.type", "invalid_enum")
		}
		if problem := validateExternalRef("scope.ref", value.Scope.Ref); problem != nil {
			return problem
		}
		if value.BlockIssuance == nil || value.BlockRedemption == nil || value.Active == nil {
			return invalid("body", "required_boolean")
		}
		if problem := dateTime("effectiveFrom", value.EffectiveFrom); problem != nil {
			return problem
		}
		if !utf8.ValidString(value.ReasonCode) || utf8.RuneCountInString(value.ReasonCode) > 80 {
			return invalid("reasonCode", "too_long")
		}
		return validateExternalRef("operationTaskRef", value.OperationTaskRef)
	case *ApplyReadOnlyNoticeRequest:
		if problem := nonNegativeVersion(value.ExpectedVersion); problem != nil {
			return problem
		}
		if problem := stringRange("message", value.Message, 1, 500); problem != nil {
			return problem
		}
		if problem := dateTime("effectiveFrom", value.EffectiveFrom); problem != nil {
			return problem
		}
		if value.Active == nil {
			return invalid("active", "required_boolean")
		}
		return nil
	case *RetryRecoveryRequest:
		return validateRecoveryRequest(value.ReasonCode, value.OperationTaskRef)
	case *FinalizeRecoveryRequest:
		return validateRecoveryRequest(value.ReasonCode, value.OperationTaskRef)
	case *CreateCompensationIssueRequest:
		if !campaignIDPattern.MatchString(value.CampaignID) {
			return invalid("campaignId", "invalid_format")
		}
		if problem := stringRange("userId", value.UserID, 1, 128); problem != nil {
			return problem
		}
		if problem := validateExternalRef("sourceRef", value.SourceRef); problem != nil {
			return problem
		}
		return stringRange("reasonCode", value.ReasonCode, 1, 80)
	default:
		return invalid("body", "unsupported_schema")
	}
}

func validateOrderSnapshot(value OrderCandidateSnapshot) *httpcontract.Error {
	if problem := validateSnapshot("orderSnapshot.snapshotRef", value.SnapshotRef); problem != nil {
		return problem
	}
	if problem := opaqueRef("orderSnapshot.orderId", value.OrderID); problem != nil {
		return problem
	}
	if problem := stringRange("orderSnapshot.userId", value.UserID, 1, 128); problem != nil {
		return problem
	}
	if len(value.Items) < 1 || len(value.Items) > 500 {
		return invalid("orderSnapshot.items", "out_of_range")
	}
	for index, item := range value.Items {
		prefix := "orderSnapshot.items"
		if problem := validateExternalRef(prefix+".productRef", item.ProductRef); problem != nil {
			return problem
		}
		if problem := validateExternalRef(prefix+".sellerRef", item.SellerRef); problem != nil {
			return problem
		}
		if item.DropRef != nil {
			if problem := validateExternalRef(prefix+".dropRef", *item.DropRef); problem != nil {
				return problem
			}
		}
		if item.CategoryRef != nil {
			if problem := validateExternalRef(prefix+".categoryRef", *item.CategoryRef); problem != nil {
				return problem
			}
		}
		if item.Quantity == nil || *item.Quantity < 1 || *item.Quantity > 10_000 {
			return invalid(prefix+"["+strconv.Itoa(index)+"].quantity", "out_of_range")
		}
		if problem := validateMoney(prefix+".unitPrice", item.UnitPrice); problem != nil {
			return problem
		}
	}
	return validateMoney("orderSnapshot.shippingFee", value.ShippingFee)
}

func validateBenefit(value Benefit) *httpcontract.Error {
	if !oneOf(value.Type, "fixed_amount", "percentage", "shipping_fee") {
		return invalid("benefit.type", "invalid_enum")
	}
	if value.Amount != nil {
		if problem := validateMoney("benefit.amount", *value.Amount); problem != nil {
			return problem
		}
	}
	if value.MaxDiscountAmount != nil {
		if problem := validateMoney("benefit.maxDiscountAmount", *value.MaxDiscountAmount); problem != nil {
			return problem
		}
	}
	if value.Percentage != "" {
		matched, _ := regexp.MatchString(`^(100|[0-9]{1,2})(\.[0-9]{1,2})?$`, value.Percentage)
		if !matched {
			return invalid("benefit.percentage", "invalid_format")
		}
	}
	return nil
}

func validateApplicability(value ApplicabilityPolicy) *httpcontract.Error {
	if value.PolicySchemaVersion == nil || *value.PolicySchemaVersion != 1 {
		return invalid("applicability.policySchemaVersion", "invalid_enum")
	}
	if value.IncludeTargets == nil || len(value.IncludeTargets) > 100 || value.ExcludeTargets == nil || len(value.ExcludeTargets) > 100 {
		return invalid("applicability", "invalid_targets")
	}
	for _, target := range append(append([]ExternalRef{}, value.IncludeTargets...), value.ExcludeTargets...) {
		if problem := validateExternalRef("applicability.target", target); problem != nil {
			return problem
		}
	}
	if value.MinimumOrderAmount != nil {
		if problem := validateMoney("applicability.minimumOrderAmount", *value.MinimumOrderAmount); problem != nil {
			return problem
		}
	}
	return optionalOpaqueRef("applicability.stackingPolicyRef", value.StackingPolicyRef)
}

func validateIssuerAndFunding(value IssuerAndFunding) *httpcontract.Error {
	if !oneOf(value.IssuerType, "platform", "seller", "partnership", "compensation") {
		return invalid("issuerAndFunding.issuerType", "invalid_enum")
	}
	if problem := validateExternalRef("issuerAndFunding.issuerRef", value.IssuerRef); problem != nil {
		return problem
	}
	if !oneOf(value.FunderType, "platform", "seller", "joint", "compensation") {
		return invalid("issuerAndFunding.funderType", "invalid_enum")
	}
	if value.FunderRef != nil {
		if problem := validateExternalRef("issuerAndFunding.funderRef", *value.FunderRef); problem != nil {
			return problem
		}
	}
	if value.PlatformSharePercentage != "" {
		matched, _ := regexp.MatchString(`^(100|[0-9]{1,2})(\.[0-9]{1,2})?$`, value.PlatformSharePercentage)
		if !matched {
			return invalid("issuerAndFunding.platformSharePercentage", "invalid_format")
		}
	}
	if (value.FunderType == "seller" || value.FunderType == "joint") && value.FunderRef == nil {
		return invalid("issuerAndFunding.funderRef", "required_for_funder_type")
	}
	if value.FunderType == "joint" && value.PlatformSharePercentage == "" {
		return invalid("issuerAndFunding.platformSharePercentage", "required_for_joint_funding")
	}
	if value.FunderType != "joint" && value.PlatformSharePercentage != "" {
		return invalid("issuerAndFunding.platformSharePercentage", "unexpected_for_funder_type")
	}
	return nil
}

func validateRecoveryRequest(reason string, task ExternalRef) *httpcontract.Error {
	if problem := stringRange("reasonCode", reason, 1, 80); problem != nil {
		return problem
	}
	return validateExternalRef("operationTaskRef", task)
}

func validateSnapshot(field string, value SnapshotRef) *httpcontract.Error {
	if problem := validateExternalRef(field+".sourceRef", value.SourceRef); problem != nil {
		return problem
	}
	if problem := stringRange(field+".sourceVersion", value.SourceVersion, 1, 128); problem != nil {
		return problem
	}
	if problem := dateTime(field+".capturedAt", value.CapturedAt); problem != nil {
		return problem
	}
	if !payloadHashPattern.MatchString(value.PayloadHash) {
		return invalid(field+".payloadHash", "invalid_format")
	}
	return nil
}

func validateExternalRef(field string, value ExternalRef) *httpcontract.Error {
	if problem := stringRange(field+".context", value.Context, 1, 64); problem != nil {
		return problem
	}
	if problem := stringRange(field+".type", value.Type, 1, 64); problem != nil {
		return problem
	}
	return opaqueRef(field+".id", value.ID)
}

func validateMoney(field string, value Money) *httpcontract.Error {
	if !moneyPattern.MatchString(value.Amount) {
		return invalid(field+".amount", "invalid_format")
	}
	if !currencyPattern.MatchString(value.Currency) {
		return invalid(field+".currency", "invalid_format")
	}
	return nil
}

func nonNegativeVersion(value *int64) *httpcontract.Error {
	if value == nil || *value < 0 {
		return invalid("expectedVersion", "required_non_negative_integer")
	}
	return nil
}

func dateTime(field, value string) *httpcontract.Error {
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return invalid(field, "invalid_date_time")
	}
	return nil
}

func opaqueRef(field, value string) *httpcontract.Error {
	if len(value) < 1 || len(value) > 200 || !opaqueRefPattern.MatchString(value) {
		return invalid(field, "invalid_format")
	}
	return nil
}

func optionalOpaqueRef(field, value string) *httpcontract.Error {
	if value == "" {
		return nil
	}
	return opaqueRef(field, value)
}

func stringRange(field, value string, minimum, maximum int) *httpcontract.Error {
	if !utf8.ValidString(value) {
		return invalid(field, "invalid_encoding")
	}
	trimmedLength := utf8.RuneCountInString(strings.TrimSpace(value))
	if trimmedLength < minimum || utf8.RuneCountInString(value) > maximum {
		return invalid(field, "invalid_length")
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func invalid(field, reason string) *httpcontract.Error {
	return httpcontract.InputInvalid(field, reason)
}
