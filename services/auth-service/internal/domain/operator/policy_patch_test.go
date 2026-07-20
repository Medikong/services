package operator

import (
	"encoding/json"
	"testing"
)

func TestPatchPolicyRulesKeepsOnlySupportedFields(t *testing.T) {
	previous := json.RawMessage(`{"failureThreshold":5,"windowSeconds":900,"lockSeconds":900,"resetFailureOnSuccess":true}`)
	raw, err := PatchPolicyRules("login_lock", previous, map[string]any{
		"policyName":       "login-lock",
		"failureThreshold": float64(6),
		"changeReason":     "SECURITY_BASELINE_UPDATE",
	})
	if err != nil {
		t.Fatalf("patch policy: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["failureThreshold"] != float64(6) || result["lockSeconds"] != float64(900) || len(result) != 4 {
		t.Fatalf("unexpected merged rules: %#v", result)
	}
}

func TestPatchPolicyRulesRejectsInvalidRelationshipAndUnknownField(t *testing.T) {
	previous := json.RawMessage(`{"webIdleSeconds":1800,"webAbsoluteSeconds":43200,"mobileAccessSeconds":900,"mobileRefreshSeconds":1209600,"webRememberMeSeconds":2592000,"internalContextSeconds":300}`)
	_, err := PatchPolicyRules("session_ttl", previous, map[string]any{
		"policyName":         "session-ttl",
		"webIdleSeconds":     float64(50000),
		"webAbsoluteSeconds": float64(100),
		"changeReason":       "SECURITY_BASELINE_UPDATE",
	})
	if err == nil {
		t.Fatal("expected invalid TTL relationship to be rejected")
	}
	_, err = PatchPolicyRules("session_ttl", previous, map[string]any{
		"policyName":   "session-ttl",
		"unknown":      true,
		"changeReason": "SECURITY_BASELINE_UPDATE",
	})
	if err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestPatchPolicyRulesReplacesAndValidatesRuleBundles(t *testing.T) {
	raw, err := PatchPolicyRules("verification_rules", nil, map[string]any{
		"policyName": "verification",
		"rules": []any{
			map[string]any{"purpose": "password_reset", "channel": "email_code", "ttlSeconds": float64(300), "maxAttempts": float64(5), "maxSends": float64(3), "resendIntervalSeconds": float64(60)},
		},
		"changeReason": "SECURITY_BASELINE_UPDATE",
	})
	if err != nil || len(raw) == 0 {
		t.Fatalf("expected valid replacement, raw=%s err=%v", raw, err)
	}
	_, err = PatchPolicyRules("verification_rules", nil, map[string]any{
		"policyName": "verification",
		"rules": []any{
			map[string]any{"purpose": "phone_change", "channel": "sms_code", "ttlSeconds": float64(300), "maxAttempts": float64(5), "maxSends": float64(3), "resendIntervalSeconds": float64(60)},
			map[string]any{"purpose": "phone_change", "channel": "sms_code", "ttlSeconds": float64(300), "maxAttempts": float64(5), "maxSends": float64(3), "resendIntervalSeconds": float64(60)},
		},
		"changeReason": "SECURITY_BASELINE_UPDATE",
	})
	if err == nil {
		t.Fatal("expected duplicate verification rule to be rejected")
	}
}
