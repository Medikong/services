//go:build integration

package integration_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/app"
	appoperator "github.com/Medikong/services/services/auth-service/internal/application/operator"
	"github.com/google/uuid"
)

type identityE2EIntent struct {
	ID            string
	AuthFlowToken string
	CSRFToken     string
	Cookie        *http.Cookie
}

type identityE2EWebSession struct {
	UserID             string         `json:"userId"`
	SessionID          string         `json:"sessionId"`
	CSRFToken          string         `json:"csrfToken"`
	CredentialDelivery string         `json:"credentialDelivery"`
	Next               map[string]any `json:"next"`
	Cookie             *http.Cookie   `json:"-"`
	Session            struct {
		SessionID string    `json:"sessionId"`
		ExpiresAt time.Time `json:"expiresAt"`
	} `json:"session"`
	Access struct {
		AccessToken          string    `json:"accessToken"`
		AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
	} `json:"access"`
}

type identityE2EReauthentication struct {
	Proof     string    `json:"reauthenticationProof"`
	Purpose   string    `json:"purpose"`
	ExpiresAt time.Time `json:"expiresAt"`
	Session   struct {
		SessionID string    `json:"sessionId"`
		ExpiresAt time.Time `json:"expiresAt"`
	} `json:"session"`
	Access struct {
		AccessToken          string    `json:"accessToken"`
		AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
	} `json:"access"`
	SessionID  string       `json:"-"`
	CSRFToken  string       `json:"-"`
	Delivery   string       `json:"credentialDelivery"`
	NewCookie  *http.Cookie `json:"-"`
	OldCookie  *http.Cookie `json:"-"`
	RequestKey string       `json:"-"`
}

type identityE2EOperatorUser struct {
	UserID               string                        `json:"userId"`
	Status               string                        `json:"status"`
	UserAuthStateVersion int64                         `json:"userAuthStateVersion"`
	Identities           []identityE2EOperatorIdentity `json:"identities"`
	ActiveSessionCount   int                           `json:"activeSessionCount"`
}

type identityE2EOperatorIdentity struct {
	IdentityID         string                  `json:"identityId"`
	IdentityLinkID     string                  `json:"identityLinkId"`
	Type               string                  `json:"type"`
	MaskedValue        string                  `json:"maskedValue"`
	VerificationStatus string                  `json:"verificationStatus"`
	LinkStatus         string                  `json:"linkStatus"`
	RowVersion         int64                   `json:"rowVersion"`
	Lock               identityE2EOperatorLock `json:"lock"`
}

type identityE2EOperatorLock struct {
	Locked            bool       `json:"locked"`
	UnlockAvailableAt *time.Time `json:"unlockAvailableAt"`
}

type identityE2EPolicy struct {
	Version                int64                              `json:"version"`
	Status                 string                             `json:"status"`
	EffectiveAt            time.Time                          `json:"effectiveAt"`
	LoginLock              identityE2ELoginLockPolicy         `json:"loginLock"`
	SessionTTL             identityE2ESessionTTLPolicy        `json:"sessionTtl"`
	RefreshRotation        identityE2ERefreshRotationPolicy   `json:"refreshRotation"`
	VerificationRules      []identityE2EVerificationRule      `json:"verificationRules"`
	SessionRevocationRules []identityE2ESessionRevocationRule `json:"sessionRevocationRules"`
}

type identityE2ELoginLockPolicy struct {
	FailureThreshold      int  `json:"failureThreshold"`
	WindowSeconds         int  `json:"windowSeconds"`
	LockSeconds           int  `json:"lockSeconds"`
	ResetFailureOnSuccess bool `json:"resetFailureOnSuccess"`
}

type identityE2ESessionTTLPolicy struct {
	WebIdleSeconds         int `json:"webIdleSeconds"`
	WebAbsoluteSeconds     int `json:"webAbsoluteSeconds"`
	MobileAccessSeconds    int `json:"mobileAccessSeconds"`
	MobileRefreshSeconds   int `json:"mobileRefreshSeconds"`
	WebRememberMeSeconds   int `json:"webRememberMeSeconds"`
	InternalContextSeconds int `json:"internalContextSeconds"`
}

type identityE2ERefreshRotationPolicy struct {
	Enabled     bool   `json:"enabled"`
	ReuseAction string `json:"reuseAction"`
}

type identityE2EVerificationRule struct {
	Purpose               string `json:"purpose"`
	Channel               string `json:"channel"`
	TTLSeconds            int    `json:"ttlSeconds"`
	MaxAttempts           int    `json:"maxAttempts"`
	MaxSends              int    `json:"maxSends"`
	ResendIntervalSeconds int    `json:"resendIntervalSeconds"`
}

type identityE2ESessionRevocationRule struct {
	Trigger string   `json:"trigger"`
	Scopes  []string `json:"scopes"`
}

func TestHTTPIdentityOperatorAndActionE2E(t *testing.T) {
	approval := &appoperator.StaticApprovalPort{}
	harness := newProductionHTTPHarness(t, app.ServerOptions{ApprovalPort: approval, AuthorizationDecisionPort: allowAuthorizationDecision{}})

	t.Run("production server excludes API.A.300-30", func(t *testing.T) {
		response := harness.do(httpE2ERequest{Method: http.MethodGet, Path: "/api/v1/dev/auth/verification-messages/" + uuid.NewString()})
		decodeHTTPError(t, response, http.StatusNotFound, "AUTH_ROUTE_NOT_FOUND")
	})

	userID, emailIdentityID, emailLinkID := uuid.New(), uuid.New(), uuid.New()
	password := "identity-http-e2e-password"
	email := identityFixtureEmail(emailIdentityID)
	seedEmailPrincipal(t, harness.ctx, harness.db, userID, emailIdentityID, emailLinkID, password)
	session := signInIdentityE2EWeb(t, harness, email, password, true, "navigation", nil)

	t.Run("API.A.300-17 error", func(t *testing.T) {
		wrongPassword := "identity-http-e2e-wrong-password"
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/reauthentications/email",
			JSON:   map[string]any{"purpose": "link_identity", "password": wrongPassword},
			Headers: http.Header{
				"Idempotency-Key": {uuid.NewString()},
			},
			Credentials: identitySessionCredentials(session),
		})
		decodeHTTPError(t, response, http.StatusUnauthorized, "AUTH_SIGNIN_FAILED")
		assertResponseOmits(t, response, email, wrongPassword, session.Cookie.Value, session.CSRFToken)
	})

	var linkReauthentication identityE2EReauthentication
	t.Run("API.A.300-17 success and exact delivery recovery", func(t *testing.T) {
		linkReauthentication = reauthenticateIdentityE2E(t, harness, session, password, "link_identity")
		session.Cookie = linkReauthentication.NewCookie
		session.CSRFToken = linkReauthentication.CSRFToken
		session.Access = linkReauthentication.Access
	})

	t.Run("API.A.300-18 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/method-links",
			JSON: map[string]any{
				"method":                "phone",
				"destination":           map[string]any{"countryCode": "+82", "nationalNumber": "1090000001"},
				"reauthenticationProof": "invalid-proof",
			},
			Headers:     http.Header{"Idempotency-Key": {uuid.NewString()}},
			Credentials: identitySessionCredentials(session),
		})
		decodeHTTPError(t, response, http.StatusGone, "AUTH_REAUTHENTICATION_PROOF_INVALID")
	})

	linkKey := uuid.NewString()
	linkPhone := map[string]any{"countryCode": "+82", "nationalNumber": "1090000002"}
	var linkID string
	t.Run("API.A.300-18 success and replay", func(t *testing.T) {
		request := httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/method-links",
			JSON: map[string]any{
				"method": "phone", "destination": linkPhone,
				"reauthenticationProof": linkReauthentication.Proof,
			},
			Headers:     http.Header{"Idempotency-Key": {linkKey}},
			Credentials: identitySessionCredentials(session),
		}
		var first struct {
			LinkID    string    `json:"linkIntentId"`
			Status    string    `json:"status"`
			Method    string    `json:"method"`
			ExpiresAt time.Time `json:"expiresAt"`
		}
		decodeHTTPEnvelope(t, harness.do(request), http.StatusCreated, &first)
		if _, err := uuid.Parse(first.LinkID); err != nil || first.Status != "requested" {
			t.Fatal("method-link response is incomplete")
		}
		var replay struct {
			LinkID    string    `json:"linkIntentId"`
			Status    string    `json:"status"`
			Method    string    `json:"method"`
			ExpiresAt time.Time `json:"expiresAt"`
		}
		decodeHTTPEnvelope(t, harness.do(request), http.StatusCreated, &replay)
		if replay.LinkID != first.LinkID || replay.Status != first.Status {
			t.Fatal("method-link replay did not return the original result")
		}
		linkID = first.LinkID
	})

	t.Run("API.A.300-19 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method:  http.MethodPost,
			Path:    "/api/v1/auth/method-links/" + uuid.NewString() + "/challenges",
			JSON:    map[string]any{"channel": "sms"},
			Headers: http.Header{"Idempotency-Key": {uuid.NewString()}}, Credentials: identitySessionCredentials(session),
		})
		decodeHTTPError(t, response, http.StatusNotFound, "AUTH_IDENTITY_LINK_NOT_FOUND")
	})

	var linkChallengeID string
	t.Run("API.A.300-19 success", func(t *testing.T) {
		var data struct {
			ChallengeID       string    `json:"challengeId"`
			MaskedDestination string    `json:"maskedDestination"`
			ExpiresAt         time.Time `json:"expiresAt"`
		}
		response := harness.do(httpE2ERequest{
			Method:  http.MethodPost,
			Path:    "/api/v1/auth/method-links/" + linkID + "/challenges",
			JSON:    map[string]any{"channel": "sms"},
			Headers: http.Header{"Idempotency-Key": {uuid.NewString()}}, Credentials: identitySessionCredentials(session),
		})
		decodeHTTPEnvelope(t, response, http.StatusCreated, &data)
		if _, err := uuid.Parse(data.ChallengeID); err != nil {
			t.Fatal("method-link challenge response is incomplete")
		}
		linkChallengeID = data.ChallengeID
	})

	linkCode := decryptDeliveryCode(t, harness.ctx, harness.db, linkChallengeID)
	t.Run("API.A.300-20 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/method-links/" + linkID + "/complete",
			JSON: map[string]any{
				"challengeId": linkChallengeID,
				"proof":       map[string]any{"type": "code", "value": differentVerificationCode(linkCode)},
			},
			Headers: http.Header{"Idempotency-Key": {uuid.NewString()}}, Credentials: identitySessionCredentials(session),
		})
		decodeHTTPError(t, response, http.StatusBadRequest, "AUTH_CHALLENGE_FAILED")
		assertResponseOmits(t, response, linkCode, session.Cookie.Value, session.CSRFToken)
	})

	t.Run("API.A.300-20 success", func(t *testing.T) {
		var data struct {
			IdentityLinkID string `json:"identityLinkId"`
			Status         string `json:"status"`
			Method         string `json:"method"`
		}
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/method-links/" + linkID + "/complete",
			JSON: map[string]any{
				"challengeId": linkChallengeID,
				"proof":       map[string]any{"type": "code", "value": linkCode},
			},
			Headers: http.Header{"Idempotency-Key": {uuid.NewString()}}, Credentials: identitySessionCredentials(session),
		})
		decodeHTTPEnvelope(t, response, http.StatusOK, &data)
		if data.IdentityLinkID != linkID || data.Status != "active" {
			t.Fatal("method-link completion response is incomplete")
		}
	})

	t.Run("API.A.300-18 existing-link success", func(t *testing.T) {
		reauthenticated := reauthenticateIdentityE2E(t, harness, session, password, "link_identity")
		session.Cookie = reauthenticated.NewCookie
		session.CSRFToken = reauthenticated.CSRFToken
		session.Access = reauthenticated.Access
		var data struct {
			IdentityLinkID string `json:"identityLinkId"`
			Status         string `json:"status"`
			Method         string `json:"method"`
		}
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/method-links",
			JSON: map[string]any{
				"method":                "phone",
				"destination":           linkPhone,
				"reauthenticationProof": reauthenticated.Proof,
			},
			Headers:     http.Header{"Idempotency-Key": {uuid.NewString()}},
			Credentials: identitySessionCredentials(session),
		})
		decodeHTTPEnvelope(t, response, http.StatusOK, &data)
		if data.IdentityLinkID != linkID || data.Status != "active" || data.Method != "phone" {
			t.Fatal("existing method-link response is incomplete")
		}
	})

	var replacementReauthentication identityE2EReauthentication
	t.Run("API.A.300-17 replacement reauthentication", func(t *testing.T) {
		replacementReauthentication = reauthenticateIdentityE2E(t, harness, session, password, "replace_phone")
		session.Cookie = replacementReauthentication.NewCookie
		session.CSRFToken = replacementReauthentication.CSRFToken
		session.Access = replacementReauthentication.Access
	})

	t.Run("API.A.300-21 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost, Path: "/api/v1/auth/phone-replacements",
			JSON: map[string]any{
				"newPhone":              map[string]any{"countryCode": "+82", "nationalNumber": "1090000003"},
				"reauthenticationProof": "invalid-proof",
			},
			Headers: http.Header{"Idempotency-Key": {uuid.NewString()}}, Credentials: identitySessionCredentials(session),
		})
		decodeHTTPError(t, response, http.StatusGone, "AUTH_REAUTHENTICATION_PROOF_INVALID")
	})

	replacementKey := uuid.NewString()
	var replacementID string
	t.Run("API.A.300-21 success and replay", func(t *testing.T) {
		request := httpE2ERequest{
			Method: http.MethodPost, Path: "/api/v1/auth/phone-replacements",
			JSON: map[string]any{
				"newPhone":              map[string]any{"countryCode": "+82", "nationalNumber": "1090000004"},
				"reauthenticationProof": replacementReauthentication.Proof,
			},
			Headers: http.Header{"Idempotency-Key": {replacementKey}}, Credentials: identitySessionCredentials(session),
		}
		var first struct {
			ReplacementID string    `json:"replacementId"`
			Status        string    `json:"status"`
			ExpiresAt     time.Time `json:"expiresAt"`
		}
		decodeHTTPEnvelope(t, harness.do(request), http.StatusCreated, &first)
		var replay struct {
			ReplacementID string    `json:"replacementId"`
			Status        string    `json:"status"`
			ExpiresAt     time.Time `json:"expiresAt"`
		}
		decodeHTTPEnvelope(t, harness.do(request), http.StatusCreated, &replay)
		if first.ReplacementID == "" || replay.ReplacementID != first.ReplacementID || replay.Status != first.Status {
			t.Fatal("phone-replacement replay did not return the original result")
		}
		replacementID = first.ReplacementID
	})

	t.Run("API.A.300-22 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/phone-replacements/" + uuid.NewString() + "/challenges",
			JSON:   map[string]any{}, Headers: http.Header{"Idempotency-Key": {uuid.NewString()}},
			Credentials: identitySessionCredentials(session),
		})
		decodeHTTPError(t, response, http.StatusNotFound, "AUTH_IDENTITY_LINK_NOT_FOUND")
	})

	var replacementChallengeID string
	t.Run("API.A.300-22 success", func(t *testing.T) {
		var data struct {
			ChallengeID       string    `json:"challengeId"`
			MaskedDestination string    `json:"maskedDestination"`
			ExpiresAt         time.Time `json:"expiresAt"`
			ResendAvailableAt time.Time `json:"resendAvailableAt"`
		}
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/phone-replacements/" + replacementID + "/challenges",
			JSON:   map[string]any{}, Headers: http.Header{"Idempotency-Key": {uuid.NewString()}},
			Credentials: identitySessionCredentials(session),
		})
		decodeHTTPEnvelope(t, response, http.StatusCreated, &data)
		if _, err := uuid.Parse(data.ChallengeID); err != nil {
			t.Fatal("phone-replacement challenge response is incomplete")
		}
		replacementChallengeID = data.ChallengeID
	})

	replacementCode := decryptDeliveryCode(t, harness.ctx, harness.db, replacementChallengeID)
	t.Run("API.A.300-23 challenge error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost, Path: "/api/v1/auth/phone-replacements/" + replacementID + "/complete",
			JSON: map[string]any{
				"challengeId": replacementChallengeID,
				"proof":       map[string]any{"type": "code", "value": differentVerificationCode(replacementCode)},
			},
			Headers: http.Header{"Idempotency-Key": {uuid.NewString()}}, Credentials: identitySessionCredentials(session),
		})
		decodeHTTPError(t, response, http.StatusBadRequest, "AUTH_CHALLENGE_FAILED")
	})

	t.Run("API.A.300-23 success, recovery, and expired recovery", func(t *testing.T) {
		oldCookie := session.Cookie
		oldCSRF := session.CSRFToken
		key := uuid.NewString()
		request := httpE2ERequest{
			Method: http.MethodPost, Path: "/api/v1/auth/phone-replacements/" + replacementID + "/complete",
			JSON: map[string]any{
				"challengeId": replacementChallengeID,
				"proof":       map[string]any{"type": "code", "value": replacementCode},
			},
			Headers:     http.Header{"Idempotency-Key": {key}},
			Credentials: identitySessionCredentials(session),
		}
		var first struct {
			ReplacementID      string `json:"replacementId"`
			Status             string `json:"status"`
			CredentialDelivery string `json:"credentialDelivery"`
			Session            struct {
				SessionID string    `json:"sessionId"`
				ExpiresAt time.Time `json:"expiresAt"`
			} `json:"session"`
			Access struct {
				AccessToken          string    `json:"accessToken"`
				AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
			} `json:"access"`
		}
		firstResponse := harness.do(request)
		decodeHTTPEnvelope(t, firstResponse, http.StatusOK, &first)
		newCookie := responseCookie(t, firstResponse, "__Secure-dm_refresh")
		assertCredentialCookie(t, newCookie, "__Secure-dm_refresh")
		if newCookie.MaxAge <= 0 || newCookie.MaxAge > oldCookie.MaxAge {
			t.Fatal("remembered replacement session did not preserve its cookie lifetime")
		}
		var recovered struct {
			ReplacementID      string `json:"replacementId"`
			Status             string `json:"status"`
			CredentialDelivery string `json:"credentialDelivery"`
			Session            struct {
				SessionID string    `json:"sessionId"`
				ExpiresAt time.Time `json:"expiresAt"`
			} `json:"session"`
			Access struct {
				AccessToken          string    `json:"accessToken"`
				AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
			} `json:"access"`
		}
		recoveryResponse := harness.do(request)
		decodeHTTPEnvelope(t, recoveryResponse, http.StatusOK, &recovered)
		recoveredCookie := responseCookie(t, recoveryResponse, "__Secure-dm_refresh")
		if recovered.ReplacementID != first.ReplacementID || recovered.Session.SessionID != first.Session.SessionID || recovered.Access.AccessToken != first.Access.AccessToken || recoveredCookie.Value != newCookie.Value {
			t.Fatal("phone-replacement delivery recovery changed the original result")
		}
		if _, err := harness.db.Exec(harness.ctx, `
			UPDATE auth_idempotency_replay_payloads
			SET expires_at = now() - interval '1 second'
			WHERE payload_kind = 'phone_replacement_credential_delivery'
		`); err != nil {
			t.Fatal("expire phone-replacement recovery fixture")
		}
		decodeHTTPError(t, harness.do(request), http.StatusGone, "AUTH_SESSION_DELIVERY_EXPIRED")
		session.Cookie = newCookie
		session.CSRFToken = oldCSRF
		session.SessionID = first.Session.SessionID
		session.Access = first.Access
	})

	var actionSession identityE2EWebSession
	var actionIntent identityE2EIntent
	t.Run("action-resume setup", func(t *testing.T) {
		actionContext := map[string]any{"dropId": uuid.NewString(), "optionId": uuid.NewString(), "quantity": 1}
		actionIntent = createIdentityE2EWebIntent(t, harness, "purchase", actionContext)
		actionSession = completeIdentityE2EWebSignIn(t, harness, actionIntent, email, password, false)
	})

	t.Run("API.A.300-29 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost, Path: "/api/v1/auth/intents/" + uuid.NewString() + "/action-resume",
			JSON: map[string]any{}, Headers: http.Header{"Idempotency-Key": {uuid.NewString()}},
			Credentials: identitySessionCredentials(actionSession),
		})
		decodeHTTPError(t, response, http.StatusGone, "AUTH_INTENT_EXPIRED")
	})

	t.Run("API.A.300-29 success and replay", func(t *testing.T) {
		key := uuid.NewString()
		request := httpE2ERequest{
			Method: http.MethodPost, Path: "/api/v1/auth/intents/" + actionIntent.ID + "/action-resume",
			JSON: map[string]any{}, Headers: http.Header{"Idempotency-Key": {key}},
			Credentials: identitySessionCredentials(actionSession),
		}
		var first struct {
			IntentID   string         `json:"intentId"`
			Action     string         `json:"action"`
			Context    map[string]any `json:"actionContext"`
			ReturnPath string         `json:"returnPath"`
		}
		decodeHTTPEnvelope(t, harness.do(request), http.StatusOK, &first)
		var replay struct {
			IntentID   string         `json:"intentId"`
			Action     string         `json:"action"`
			Context    map[string]any `json:"actionContext"`
			ReturnPath string         `json:"returnPath"`
		}
		decodeHTTPEnvelope(t, harness.do(request), http.StatusOK, &replay)
		if first.IntentID != actionIntent.ID || first.Action != "purchase" || replay.IntentID != first.IntentID || replay.Action != first.Action {
			t.Fatal("action-resume replay did not return the original action")
		}
	})

	operatorID, operatorIdentityID, operatorLinkID := uuid.New(), uuid.New(), uuid.New()
	operatorPassword := "operator-http-e2e-password"
	operatorEmail := identityFixtureEmail(operatorIdentityID)
	seedEmailPrincipal(t, harness.ctx, harness.db, operatorID, operatorIdentityID, operatorLinkID, operatorPassword)
	operatorSession := signInIdentityE2EWeb(t, harness, operatorEmail, operatorPassword, false, "navigation", nil)
	operatorCredentials := httpE2ECredentials{AccessToken: operatorSession.Access.AccessToken}
	targetUserID, targetIdentityID, targetLinkID := uuid.New(), uuid.New(), uuid.New()
	seedRefreshPrincipal(t, harness.ctx, harness.db, targetUserID, targetIdentityID, targetLinkID)

	t.Run("API.A.300-24 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodGet, Path: "/api/v1/operator/auth/users/" + targetUserID.String(),
			Headers:     http.Header{"X-Audit-Reason-Code": {"CUSTOMER_SUPPORT"}},
			Credentials: operatorCredentials,
		})
		decodeHTTPError(t, response, http.StatusForbidden, "AUTH_FORBIDDEN")
	})

	t.Run("API.A.300-24 success", func(t *testing.T) {
		var data identityE2EOperatorUser
		response := harness.do(httpE2ERequest{
			Method: http.MethodGet, Path: "/api/v1/operator/auth/users/" + targetUserID.String(),
			Headers:     http.Header{"X-Audit-Reason-Code": {"CUSTOMER_SUPPORT"}, "X-Authorization-Decision": {"allow"}},
			Credentials: operatorCredentials,
		})
		decodeHTTPEnvelope(t, response, http.StatusOK, &data)
		if data.UserID != targetUserID.String() || data.Status != "active" || len(data.Identities) != 1 {
			t.Fatal("operator user response is incomplete")
		}
		identity := data.Identities[0]
		if identity.IdentityID != targetIdentityID.String() || identity.IdentityLinkID != targetLinkID.String() || identity.Type != "phone" || identity.MaskedValue == "" || identity.VerificationStatus != "verified" || identity.LinkStatus != "active" {
			t.Fatal("operator identity response is incomplete")
		}
		assertResponseOmits(t, response, "phone-"+targetIdentityID.String())
	})

	t.Run("API.A.300-25 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{Method: http.MethodGet, Path: "/api/v1/operator/auth/policies", Credentials: operatorCredentials})
		decodeHTTPError(t, response, http.StatusForbidden, "AUTH_FORBIDDEN")
	})

	var policyETag string
	t.Run("API.A.300-25 success", func(t *testing.T) {
		var data identityE2EPolicy
		response := harness.do(httpE2ERequest{Method: http.MethodGet, Path: "/api/v1/operator/auth/policies", Headers: http.Header{"X-Authorization-Decision": {"allow"}}, Credentials: operatorCredentials})
		decodeHTTPEnvelope(t, response, http.StatusOK, &data)
		policyETag = response.header.Get("ETag")
		if data.Version < 1 || data.Status != "active" || data.EffectiveAt.IsZero() || data.LoginLock.FailureThreshold < 1 || data.SessionTTL.MobileRefreshSeconds < 1 || data.RefreshRotation.ReuseAction != "revoke_family_and_session" || len(data.VerificationRules) == 0 || len(data.SessionRevocationRules) == 0 || policyETag != fmt.Sprintf("\"policy-%d\"", data.Version) {
			t.Fatal("operator policy response is missing its version field")
		}
	})

	policyPatch := map[string]any{"policyName": "login-lock", "failureThreshold": 6, "changeReason": "HTTP_E2E_POLICY_UPDATE"}
	t.Run("API.A.300-26 error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodPatch, Path: "/api/v1/operator/auth/policies/login-lock", JSON: policyPatch,
			Headers:     http.Header{"Idempotency-Key": {uuid.NewString()}, "If-Match": {"\"policy-0\""}, "X-Authorization-Decision": {"allow"}},
			Credentials: operatorCredentials,
		})
		decodeHTTPError(t, response, http.StatusPreconditionFailed, "AUTH_POLICY_PRECONDITION_FAILED")
	})

	t.Run("API.A.300-26 success and replay", func(t *testing.T) {
		key := uuid.NewString()
		request := httpE2ERequest{
			Method: http.MethodPatch, Path: "/api/v1/operator/auth/policies/login-lock", JSON: policyPatch,
			Headers:     http.Header{"Idempotency-Key": {key}, "If-Match": {policyETag}, "X-Authorization-Decision": {"allow"}},
			Credentials: operatorCredentials,
		}
		var first struct {
			Name        string    `json:"name"`
			Version     int64     `json:"version"`
			Status      string    `json:"status"`
			EffectiveAt time.Time `json:"effectiveAt"`
		}
		firstResponse := harness.do(request)
		decodeHTTPEnvelope(t, firstResponse, http.StatusOK, &first)
		if firstResponse.header.Get("ETag") != fmt.Sprintf("\"policy-%d\"", first.Version) {
			t.Fatal("updated policy response has an invalid ETag")
		}
		var replay struct {
			Name        string    `json:"name"`
			Version     int64     `json:"version"`
			Status      string    `json:"status"`
			EffectiveAt time.Time `json:"effectiveAt"`
		}
		replayResponse := harness.do(request)
		decodeHTTPEnvelope(t, replayResponse, http.StatusOK, &replay)
		if replayResponse.header.Get("ETag") != firstResponse.header.Get("ETag") {
			t.Fatal("policy replay changed the ETag")
		}
		if first.Name != "login-lock" || first.Version <= 1 || replay != first {
			t.Fatal("operator policy replay did not return the original version")
		}
	})

	manualKey := uuid.NewString()
	manualBody := map[string]any{
		"caseId": "case-http-e2e",
		"target": map[string]any{"type": "identity_link", "id": targetLinkID.String()},
		"action": "revoke_identity_link", "reasonCode": "CUSTOMER_SUPPORT",
		"approvalId": "approval-http-e2e", "evidenceRef": "case-evidence://http-e2e",
		"expectedTargetVersion": 0,
	}
	manualRequest := httpE2ERequest{
		Method: http.MethodPost, Path: "/api/v1/operator/auth/manual-actions", JSON: manualBody,
		Headers: http.Header{"Idempotency-Key": {manualKey}, "X-Authorization-Decision": {"allow"}}, Credentials: operatorCredentials,
	}
	t.Run("API.A.300-27 approval error", func(t *testing.T) {
		approval.Allow = false
		decodeHTTPError(t, harness.do(manualRequest), http.StatusConflict, "AUTH_APPROVAL_REQUIRED")
	})

	t.Run("API.A.300-27 success and replay", func(t *testing.T) {
		approval.Allow = true
		var first struct {
			ActionID      string `json:"actionId"`
			Action        string `json:"action"`
			Status        string `json:"status"`
			TargetVersion int64  `json:"targetVersion"`
		}
		decodeHTTPEnvelope(t, harness.do(manualRequest), http.StatusOK, &first)
		var replay struct {
			ActionID      string `json:"actionId"`
			Action        string `json:"action"`
			Status        string `json:"status"`
			TargetVersion int64  `json:"targetVersion"`
		}
		decodeHTTPEnvelope(t, harness.do(manualRequest), http.StatusOK, &replay)
		if first.ActionID == "" || first.TargetVersion != 1 || replay != first {
			t.Fatal("manual-action replay did not return the original result")
		}
		var count int
		if err := harness.db.QueryRow(harness.ctx, `SELECT count(*) FROM auth_manual_actions WHERE manual_action_id = $1`, first.ActionID).Scan(&count); err != nil || count != 1 {
			t.Fatal("manual-action idempotency did not retain exactly one result")
		}
	})
}

func TestHTTPDevelopmentVirtualMessageE2E(t *testing.T) {
	harness := newDevelopmentHTTPHarness(t)
	intent := createIdentityE2EMobileIntent(t, harness)
	email := "development-" + uuid.NewString() + "@example.test"
	password := "development-http-e2e-password"
	credentials := httpE2ECredentials{AuthFlowToken: intent.AuthFlowToken}
	var registration registrationHTTPStartData
	decodeHTTPEnvelope(t, harness.do(httpE2ERequest{
		Method: http.MethodPost,
		Path:   "/api/v1/auth/registrations",
		JSON: registrationHTTPBody(
			intent.ID,
			email,
			password,
			"+82",
			"1090000005",
			uuid.NewString(),
			uuid.NewString(),
		),
		Headers:     registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: credentials,
	}), http.StatusCreated, &registration)
	challenge := registrationHTTPIssueChallenge(t, harness, registration.RegistrationID, "email", credentials)

	challengeID := challenge.ChallengeID

	t.Run("API.A.300-30 hidden credential error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodGet, Path: "/api/v1/dev/auth/verification-messages/" + challengeID,
			Credentials: httpE2ECredentials{AuthFlowToken: intent.AuthFlowToken, DevelopmentAccessToken: "invalid-development-access"},
		})
		decodeHTTPError(t, response, http.StatusNotFound, "AUTH_VIRTUAL_MESSAGE_NOT_FOUND")
	})

	var virtualCode string
	t.Run("API.A.300-30 success", func(t *testing.T) {
		var data struct {
			ChallengeID       string    `json:"challengeId"`
			Channel           string    `json:"channel"`
			Status            string    `json:"status"`
			Code              string    `json:"code"`
			MaskedDestination string    `json:"maskedDestination"`
			ExpiresAt         time.Time `json:"expiresAt"`
		}
		response := harness.do(httpE2ERequest{
			Method: http.MethodGet, Path: "/api/v1/dev/auth/verification-messages/" + challengeID,
			Credentials: httpE2ECredentials{AuthFlowToken: intent.AuthFlowToken, DevelopmentAccessToken: httpE2EDevelopmentToken},
		})
		decodeHTTPEnvelope(t, response, http.StatusOK, &data)
		if data.ChallengeID != challengeID || data.Status != "ready" || len(data.Code) != 6 {
			t.Fatal("virtual-message response is incomplete")
		}
		virtualCode = data.Code
	})

	t.Run("terminal virtual message is unavailable", func(t *testing.T) {
		response := harness.do(registrationHTTPVerifyRequest(
			registration.RegistrationID,
			challengeID,
			virtualCode,
			uuid.NewString(),
			credentials,
		))
		registrationHTTPDecodeVerified(t, response, challengeID, 1)
		response = harness.do(httpE2ERequest{
			Method: http.MethodGet, Path: "/api/v1/dev/auth/verification-messages/" + challengeID,
			Credentials: httpE2ECredentials{AuthFlowToken: intent.AuthFlowToken, DevelopmentAccessToken: httpE2EDevelopmentToken},
		})
		decodeHTTPError(t, response, http.StatusGone, "AUTH_VIRTUAL_MESSAGE_UNAVAILABLE")
	})
}

func TestHTTPDevelopmentBulkTokensE2E(t *testing.T) {
	harness := newDevelopmentHTTPHarness(t)
	path := "/api/v1/dev/auth/test-tokens/bulk"

	t.Run("hidden credential error", func(t *testing.T) {
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost, Path: path, JSON: map[string]any{"count": 1},
			Credentials: httpE2ECredentials{DevelopmentAccessToken: "invalid-development-access"},
		})
		decodeHTTPError(t, response, http.StatusNotFound, "AUTH_DEVELOPMENT_ENDPOINT_NOT_FOUND")
	})

	type tokenData struct {
		UserID               string    `json:"userId"`
		SessionID            string    `json:"sessionId"`
		AccessToken          string    `json:"accessToken"`
		AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
	}
	type bulkData struct {
		Count  int         `json:"count"`
		Tokens []tokenData `json:"tokens"`
	}

	seenUsers, seenSessions := map[string]struct{}{}, map[string]struct{}{}
	for _, testCase := range []struct {
		count      int
		ttlSeconds int64
		wantTTL    time.Duration
	}{
		{count: 3, wantTTL: 24 * time.Hour},
		{count: 2, ttlSeconds: 7200, wantTTL: 2 * time.Hour},
	} {
		body := map[string]any{"count": testCase.count}
		if testCase.ttlSeconds > 0 {
			body["ttlSeconds"] = testCase.ttlSeconds
		}
		requestedAt := time.Now().UTC()
		var data bulkData
		response := harness.do(httpE2ERequest{
			Method: http.MethodPost, Path: path, JSON: body,
			Credentials: httpE2ECredentials{DevelopmentAccessToken: httpE2EDevelopmentToken},
		})
		decodeHTTPEnvelope(t, response, http.StatusCreated, &data)
		if data.Count != testCase.count || len(data.Tokens) != testCase.count {
			t.Fatalf("bulk token count = %d/%d, want %d", data.Count, len(data.Tokens), testCase.count)
		}
		for _, token := range data.Tokens {
			if _, err := uuid.Parse(token.UserID); err != nil {
				t.Fatalf("invalid user id %q", token.UserID)
			}
			if _, err := uuid.Parse(token.SessionID); err != nil {
				t.Fatalf("invalid session id %q", token.SessionID)
			}
			if token.AccessToken == "" || !token.AccessTokenExpiresAt.After(time.Now()) {
				t.Fatal("bulk token response is incomplete")
			}
			if token.AccessTokenExpiresAt.Before(requestedAt.Add(testCase.wantTTL-time.Minute)) || token.AccessTokenExpiresAt.After(requestedAt.Add(testCase.wantTTL+time.Minute)) {
				t.Fatalf("access token TTL = %s, want approximately %s", token.AccessTokenExpiresAt.Sub(requestedAt), testCase.wantTTL)
			}
			if _, duplicate := seenUsers[token.UserID]; duplicate {
				t.Fatalf("duplicate user id %s", token.UserID)
			}
			if _, duplicate := seenSessions[token.SessionID]; duplicate {
				t.Fatalf("duplicate session id %s", token.SessionID)
			}
			seenUsers[token.UserID], seenSessions[token.SessionID] = struct{}{}, struct{}{}
			var sessionExpiresAt time.Time
			if err := harness.db.QueryRow(harness.ctx, `SELECT absolute_expires_at FROM auth_sessions WHERE session_id = $1`, token.SessionID).Scan(&sessionExpiresAt); err != nil {
				t.Fatalf("read generated session expiry: %v", err)
			}
			if sessionExpiresAt.Before(token.AccessTokenExpiresAt) {
				t.Fatalf("session expires before access token: %s < %s", sessionExpiresAt, token.AccessTokenExpiresAt)
			}

			contextResponse := harness.do(httpE2ERequest{
				Method: http.MethodGet, Path: "/api/v1/auth/context",
				Credentials: httpE2ECredentials{AccessToken: token.AccessToken},
			})
			assertHTTPResponse(t, contextResponse, http.StatusOK, "application/json")
			var envelope struct {
				Data map[string]any      `json:"data"`
				Meta httpE2EResponseMeta `json:"meta"`
			}
			decodeHTTPJSON(t, contextResponse.body, &envelope)
			if authenticated, _ := envelope.Data["authenticated"].(bool); !authenticated || envelope.Data["userId"] != token.UserID {
				t.Fatal("issued access token did not authenticate its generated user")
			}

			statusResponse := harness.do(httpE2ERequest{
				Method: http.MethodPost, Path: "/internal/session/status/loadtest/probe",
				Credentials: httpE2ECredentials{AccessToken: token.AccessToken},
			})
			assertHTTPNoContent(t, statusResponse, http.StatusOK)
			if statusResponse.header.Get("X-User-Id") != token.UserID || statusResponse.header.Get("X-Session-Id") != token.SessionID {
				t.Fatal("issued access token did not pass the ingress session-status check")
			}
		}
	}
}

func createIdentityE2EWebIntent(t *testing.T, harness *httpE2EHarness, intentType string, actionContext map[string]any) identityE2EIntent {
	t.Helper()
	body := map[string]any{"returnPath": "/drops/http-e2e", "intentType": intentType}
	if actionContext != nil {
		body["actionContext"] = actionContext
	}
	response := harness.do(httpE2ERequest{
		Method: http.MethodPost, Path: "/api/v1/auth/intents", JSON: body,
		Headers: http.Header{"X-Client-Channel": {"web"}, "Idempotency-Key": {uuid.NewString()}},
	})
	var data struct {
		ID        string    `json:"authIntentId"`
		ExpiresAt time.Time `json:"expiresAt"`
		NextPath  string    `json:"nextPath"`
		CSRFToken string    `json:"csrfToken"`
	}
	decodeHTTPEnvelope(t, response, http.StatusCreated, &data)
	cookie := responseCookie(t, response, "__Host-dm_auth")
	assertCredentialCookie(t, cookie, "__Host-dm_auth")
	if _, err := uuid.Parse(data.ID); err != nil || data.CSRFToken == "" {
		t.Fatal("web authentication intent response is incomplete")
	}
	return identityE2EIntent{ID: data.ID, CSRFToken: data.CSRFToken, Cookie: cookie}
}

func createIdentityE2EMobileIntent(t *testing.T, harness *httpE2EHarness) identityE2EIntent {
	t.Helper()
	response := harness.do(httpE2ERequest{
		Method: http.MethodPost, Path: "/api/v1/auth/intents",
		JSON:    map[string]any{"returnPath": "/drops/http-e2e", "intentType": "navigation"},
		Headers: http.Header{"X-Client-Channel": {"ios"}, "Idempotency-Key": {uuid.NewString()}},
	})
	var data struct {
		ID            string    `json:"authIntentId"`
		ExpiresAt     time.Time `json:"expiresAt"`
		NextPath      string    `json:"nextPath"`
		AuthFlowToken string    `json:"authFlowToken"`
	}
	decodeHTTPEnvelope(t, response, http.StatusCreated, &data)
	if data.ID == "" || data.AuthFlowToken == "" {
		t.Fatal("mobile authentication intent response is incomplete")
	}
	return identityE2EIntent{ID: data.ID, AuthFlowToken: data.AuthFlowToken}
}

func signInIdentityE2EWeb(t *testing.T, harness *httpE2EHarness, email, password string, rememberMe bool, intentType string, actionContext map[string]any) identityE2EWebSession {
	t.Helper()
	intent := createIdentityE2EWebIntent(t, harness, intentType, actionContext)
	return completeIdentityE2EWebSignIn(t, harness, intent, email, password, rememberMe)
}

func completeIdentityE2EWebSignIn(t *testing.T, harness *httpE2EHarness, intent identityE2EIntent, email, password string, rememberMe bool) identityE2EWebSession {
	t.Helper()
	response := harness.do(httpE2ERequest{
		Method: http.MethodPost, Path: "/api/v1/auth/signins/email",
		JSON:    map[string]any{"authIntentId": intent.ID, "email": email, "password": password, "rememberMe": rememberMe},
		Headers: http.Header{"Idempotency-Key": {uuid.NewString()}},
		Credentials: httpE2ECredentials{
			AuthFlowCookie: intent.Cookie, CSRFToken: intent.CSRFToken, Origin: httpE2ETestOrigin,
		},
	})
	var data identityE2EWebSession
	decodeHTTPEnvelope(t, response, http.StatusOK, &data)
	data.SessionID = data.Session.SessionID
	data.CSRFToken = intent.CSRFToken
	data.Cookie = responseCookie(t, response, "__Secure-dm_refresh")
	assertCredentialCookie(t, data.Cookie, "__Secure-dm_refresh")
	if rememberMe && data.Cookie.MaxAge <= 0 {
		t.Fatal("remembered web session is missing Max-Age")
	}
	if !rememberMe && data.Cookie.MaxAge != 0 {
		t.Fatal("non-remembered web session unexpectedly has Max-Age")
	}
	if data.UserID == "" || data.SessionID == "" || data.Access.AccessToken == "" || data.CSRFToken == "" {
		t.Fatal("web sign-in response is incomplete")
	}
	return data
}

func identitySessionCredentials(session identityE2EWebSession) httpE2ECredentials {
	return httpE2ECredentials{AccessToken: session.Access.AccessToken}
}

func reauthenticateIdentityE2E(t *testing.T, harness *httpE2EHarness, session identityE2EWebSession, password, purpose string) identityE2EReauthentication {
	t.Helper()
	key := uuid.NewString()
	request := httpE2ERequest{
		Method: http.MethodPost, Path: "/api/v1/auth/reauthentications/email",
		JSON:    map[string]any{"purpose": purpose, "password": password},
		Headers: http.Header{"Idempotency-Key": {key}}, Credentials: identitySessionCredentials(session),
	}
	var first identityE2EReauthentication
	firstResponse := harness.do(request)
	decodeHTTPEnvelope(t, firstResponse, http.StatusOK, &first)
	first.SessionID = first.Session.SessionID
	first.CSRFToken = session.CSRFToken
	first.NewCookie = responseCookie(t, firstResponse, "__Secure-dm_refresh")
	first.OldCookie = session.Cookie
	first.RequestKey = key
	assertCredentialCookie(t, first.NewCookie, "__Secure-dm_refresh")
	if (session.Cookie.MaxAge == 0 && first.NewCookie.MaxAge != 0) || (session.Cookie.MaxAge > 0 && (first.NewCookie.MaxAge <= 0 || first.NewCookie.MaxAge > session.Cookie.MaxAge)) {
		t.Fatal("reauthentication changed the remember-me cookie behavior")
	}
	var recovered identityE2EReauthentication
	recoveryResponse := harness.do(request)
	decodeHTTPEnvelope(t, recoveryResponse, http.StatusOK, &recovered)
	recovered.SessionID = recovered.Session.SessionID
	recovered.CSRFToken = session.CSRFToken
	recoveredCookie := responseCookie(t, recoveryResponse, "__Secure-dm_refresh")
	if recovered.Proof != first.Proof || recovered.SessionID != first.SessionID || recovered.CSRFToken != first.CSRFToken || recoveredCookie.Value != first.NewCookie.Value {
		t.Fatal("reauthentication delivery recovery changed the original result")
	}
	return first
}

func identityFixtureEmail(identityID uuid.UUID) string {
	return "email-" + identityID.String() + "@example.test"
}

func differentVerificationCode(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}
