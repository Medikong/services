package operator

import (
	"encoding/json"
	"testing"
	"time"

	domainpolicy "github.com/Medikong/services/services/auth-service/internal/domain/policy"
)

func TestPolicyViewFromGlobalSnapshotPreservesSingleVersion(t *testing.T) {
	effectiveAt := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	document := json.RawMessage(`{
		"loginLock":{"failureThreshold":5,"windowSeconds":900,"lockSeconds":900,"resetFailureOnSuccess":true},
		"sessionTtl":{"webIdleSeconds":1800,"webAbsoluteSeconds":43200,"mobileAccessSeconds":900,"mobileRefreshSeconds":1209600,"webRememberMeSeconds":2592000,"internalContextSeconds":300},
		"refreshRotation":{"enabled":true,"reuseAction":"revoke_family_and_session"},
		"verificationRules":[{"purpose":"phone_change","channel":"sms_code","ttlSeconds":300,"maxAttempts":5,"maxSends":3,"resendIntervalSeconds":60}],
		"sessionRevocationRules":[{"trigger":"password_reset","scopes":["user_sessions","refresh_family"]}]
	}`)
	view, err := policyViewFromGlobal(domainpolicy.GlobalSnapshot{Version: 8, Status: "active", EffectiveAt: effectiveAt, Document: document})
	if err != nil {
		t.Fatalf("view global snapshot: %v", err)
	}
	if view.Version != 8 || !view.EffectiveAt.Equal(effectiveAt) || view.LoginLock["failureThreshold"] != float64(5) {
		t.Fatalf("unexpected view: %#v", view)
	}
	encoded, err := view.document()
	if err != nil || len(encoded) == 0 {
		t.Fatalf("encode complete document: %v", err)
	}
}
