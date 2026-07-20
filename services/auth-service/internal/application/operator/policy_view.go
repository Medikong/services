package operator

import (
	"encoding/json"
	"time"

	domainpolicy "github.com/Medikong/services/services/auth-service/internal/domain/policy"
	"github.com/samber/oops"
)

type PolicyView struct {
	Version                int64
	Status                 string
	EffectiveAt            time.Time
	LoginLock              map[string]any
	SessionTTL             map[string]any
	RefreshRotation        map[string]any
	VerificationRules      []map[string]any
	SessionRevocationRules []map[string]any
}

func policyView(items []domainpolicy.Snapshot) (PolicyView, error) {
	view := PolicyView{Status: "active"}
	for _, item := range items {
		if item.Version > view.Version {
			view.Version, view.EffectiveAt = item.Version, item.EffectiveAt
		}
		switch item.Name {
		case "login_lock":
			if err := json.Unmarshal(item.Rules, &view.LoginLock); err != nil {
				return PolicyView{}, err
			}
		case "session_ttl":
			if err := json.Unmarshal(item.Rules, &view.SessionTTL); err != nil {
				return PolicyView{}, err
			}
		case "refresh_rotation":
			if err := json.Unmarshal(item.Rules, &view.RefreshRotation); err != nil {
				return PolicyView{}, err
			}
		case "verification_rules":
			if err := json.Unmarshal(item.Rules, &view.VerificationRules); err != nil {
				return PolicyView{}, err
			}
		case "session_revocation_rules":
			if err := json.Unmarshal(item.Rules, &view.SessionRevocationRules); err != nil {
				return PolicyView{}, err
			}
		}
	}
	if !completePolicyView(view) {
		return PolicyView{}, oops.In("operator_policy_view").Code("policy.snapshot_incomplete").New("active policy snapshot is incomplete")
	}
	return view, nil
}

func policyViewFromGlobal(snapshot domainpolicy.GlobalSnapshot) (PolicyView, error) {
	var document struct {
		LoginLock              map[string]any   `json:"loginLock"`
		SessionTTL             map[string]any   `json:"sessionTtl"`
		RefreshRotation        map[string]any   `json:"refreshRotation"`
		VerificationRules      []map[string]any `json:"verificationRules"`
		SessionRevocationRules []map[string]any `json:"sessionRevocationRules"`
	}
	if err := json.Unmarshal(snapshot.Document, &document); err != nil {
		return PolicyView{}, err
	}
	view := PolicyView{
		Version: snapshot.Version, Status: snapshot.Status, EffectiveAt: snapshot.EffectiveAt,
		LoginLock: document.LoginLock, SessionTTL: document.SessionTTL,
		RefreshRotation: document.RefreshRotation, VerificationRules: document.VerificationRules,
		SessionRevocationRules: document.SessionRevocationRules,
	}
	if !completePolicyView(view) {
		return PolicyView{}, oops.In("operator_policy_view").Code("policy.snapshot_incomplete").New("active global policy snapshot is incomplete")
	}
	return view, nil
}

func completePolicyView(view PolicyView) bool {
	return view.Version > 0 && view.Status == "active" && view.LoginLock != nil && view.SessionTTL != nil &&
		view.RefreshRotation != nil && len(view.VerificationRules) > 0 && len(view.SessionRevocationRules) > 0
}

func (v PolicyView) document() (json.RawMessage, error) {
	return json.Marshal(map[string]any{
		"loginLock": v.LoginLock, "sessionTtl": v.SessionTTL, "refreshRotation": v.RefreshRotation,
		"verificationRules": v.VerificationRules, "sessionRevocationRules": v.SessionRevocationRules,
	})
}

func publicPolicyName(name string) string {
	switch name {
	case "login_lock":
		return "login-lock"
	case "session_ttl":
		return "session-ttl"
	case "refresh_rotation":
		return "refresh-rotation"
	case "verification_rules":
		return "verification"
	case "session_revocation_rules":
		return "session-revocation"
	default:
		return ""
	}
}
