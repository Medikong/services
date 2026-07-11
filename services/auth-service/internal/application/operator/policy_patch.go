package operator

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
)

var policyTrigger = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func patchPolicyRules(name string, previous json.RawMessage, patch map[string]any) (json.RawMessage, error) {
	if err := validatePatchEnvelope(name, patch); err != nil {
		return nil, err
	}
	if name == "verification_rules" || name == "session_revocation_rules" {
		rules, ok := patch["rules"].([]any)
		if !ok {
			return nil, fmt.Errorf("rules must be an array")
		}
		if name == "verification_rules" {
			if err := validateVerificationRules(rules); err != nil {
				return nil, err
			}
		} else if err := validateSessionRevocationRules(rules); err != nil {
			return nil, err
		}
		return json.Marshal(rules)
	}

	var rules map[string]any
	if err := json.Unmarshal(previous, &rules); err != nil || rules == nil {
		return nil, fmt.Errorf("stored policy shape is invalid")
	}
	for key, value := range patch {
		if key != "policyName" && key != "changeReason" {
			rules[key] = value
		}
	}
	if err := validatePolicyValues(name, rules); err != nil {
		return nil, err
	}
	return json.Marshal(rules)
}

func validatePatchEnvelope(name string, patch map[string]any) error {
	allowed := map[string]bool{"policyName": true, "changeReason": true}
	switch name {
	case "login_lock":
		for _, key := range []string{"failureThreshold", "windowSeconds", "lockSeconds", "resetFailureOnSuccess"} {
			allowed[key] = true
		}
	case "session_ttl":
		for _, key := range []string{"webIdleSeconds", "webAbsoluteSeconds", "mobileAccessSeconds", "mobileRefreshSeconds", "webRememberMeSeconds", "internalContextSeconds"} {
			allowed[key] = true
		}
	case "refresh_rotation":
		allowed["enabled"], allowed["reuseAction"] = true, true
	case "verification_rules", "session_revocation_rules":
		allowed["rules"] = true
	default:
		return fmt.Errorf("unknown policy")
	}
	if len(patch) < 3 {
		return fmt.Errorf("a policy field is required")
	}
	for key := range patch {
		if !allowed[key] {
			return fmt.Errorf("unsupported policy field")
		}
	}
	name, ok := patch["policyName"].(string)
	if !ok || dbPolicyName(name) == "" {
		return fmt.Errorf("policyName is invalid")
	}
	reason, ok := patch["changeReason"].(string)
	if !ok || len(reason) == 0 || len(reason) > 500 {
		return fmt.Errorf("changeReason is invalid")
	}
	return nil
}

func validatePolicyValues(name string, rules map[string]any) error {
	switch name {
	case "login_lock":
		if !integerBetween(rules["failureThreshold"], 1, 20) || !integerBetween(rules["windowSeconds"], 1, 86400) || !integerBetween(rules["lockSeconds"], 1, 604800) || !isBool(rules["resetFailureOnSuccess"]) {
			return fmt.Errorf("login lock policy is invalid")
		}
	case "session_ttl":
		keys := []string{"webIdleSeconds", "webAbsoluteSeconds", "mobileAccessSeconds", "mobileRefreshSeconds", "webRememberMeSeconds", "internalContextSeconds"}
		maxes := []int64{604800, 31536000, 86400, 31536000, 31536000, 86400}
		for index, key := range keys {
			if !integerBetween(rules[key], 1, maxes[index]) {
				return fmt.Errorf("session ttl policy is invalid")
			}
		}
		webIdle, _ := integer(rules["webIdleSeconds"])
		webAbsolute, _ := integer(rules["webAbsoluteSeconds"])
		mobileAccess, _ := integer(rules["mobileAccessSeconds"])
		mobileRefresh, _ := integer(rules["mobileRefreshSeconds"])
		rememberMe, _ := integer(rules["webRememberMeSeconds"])
		if webIdle > webAbsolute || mobileAccess >= mobileRefresh || mobileRefresh > rememberMe {
			return fmt.Errorf("session ttl relationships are invalid")
		}
	case "refresh_rotation":
		if !isBool(rules["enabled"]) || rules["reuseAction"] != "revoke_family_and_session" {
			return fmt.Errorf("refresh rotation policy is invalid")
		}
	}
	return nil
}

func validateVerificationRules(rules []any) error {
	if len(rules) == 0 || len(rules) > 50 {
		return fmt.Errorf("verification rules are invalid")
	}
	channels := map[string]map[string]bool{
		"signup_email":   {"email_code": true},
		"signup_phone":   {"sms_code": true},
		"phone_signin":   {"sms_code": true},
		"password_reset": {"email_code": true, "sms_code": true},
		"identity_link":  {"sms_code": true},
		"phone_change":   {"sms_code": true},
	}
	seen := make(map[string]bool, len(rules))
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok || !exactKeys(rule, "purpose", "channel", "ttlSeconds", "maxAttempts", "maxSends", "resendIntervalSeconds") {
			return fmt.Errorf("verification rule is invalid")
		}
		purpose, purposeOK := rule["purpose"].(string)
		channel, channelOK := rule["channel"].(string)
		if !purposeOK || !channelOK || !channels[purpose][channel] || !integerBetween(rule["ttlSeconds"], 1, 86400) || !integerBetween(rule["maxAttempts"], 1, 20) || !integerBetween(rule["maxSends"], 1, 20) || !integerBetween(rule["resendIntervalSeconds"], 1, 86400) {
			return fmt.Errorf("verification rule is invalid")
		}
		key := purpose + "\x00" + channel
		if seen[key] {
			return fmt.Errorf("verification rule is duplicated")
		}
		seen[key] = true
	}
	return nil
}

func validateSessionRevocationRules(rules []any) error {
	if len(rules) == 0 || len(rules) > 50 {
		return fmt.Errorf("session revocation rules are invalid")
	}
	allowedScopes := map[string]bool{"current_session": true, "identity_sessions": true, "user_sessions": true, "refresh_family": true}
	seen := make(map[string]bool, len(rules))
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok || !exactKeys(rule, "trigger", "scopes") {
			return fmt.Errorf("session revocation rule is invalid")
		}
		trigger, ok := rule["trigger"].(string)
		scopes, okScopes := rule["scopes"].([]any)
		if !ok || len(trigger) == 0 || len(trigger) > 32 || !policyTrigger.MatchString(trigger) || !okScopes || len(scopes) == 0 || len(scopes) > 4 || seen[trigger] {
			return fmt.Errorf("session revocation rule is invalid")
		}
		scopeSeen := make(map[string]bool, len(scopes))
		for _, rawScope := range scopes {
			scope, ok := rawScope.(string)
			if !ok || !allowedScopes[scope] || scopeSeen[scope] {
				return fmt.Errorf("session revocation rule is invalid")
			}
			scopeSeen[scope] = true
		}
		if trigger == "password_reset" && !(scopeSeen["user_sessions"] && scopeSeen["refresh_family"]) {
			return fmt.Errorf("password_reset revocation scopes are invalid")
		}
		seen[trigger] = true
	}
	return nil
}

func exactKeys(value map[string]any, keys ...string) bool {
	if len(value) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func integerBetween(value any, min, max int64) bool {
	integerValue, ok := integer(value)
	return ok && integerValue >= min && integerValue <= max
}

func integer(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		if math.Trunc(typed) == typed && typed >= math.MinInt64 && typed <= math.MaxInt64 {
			return int64(typed), true
		}
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		result, err := typed.Int64()
		return result, err == nil
	}
	return 0, false
}

func isBool(value any) bool {
	_, ok := value.(bool)
	return ok
}
