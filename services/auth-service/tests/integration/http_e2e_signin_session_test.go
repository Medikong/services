//go:build integration

package integration_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	httpE2EInitialPassword = "initial-password-123"
	httpE2ENewPassword     = "rotated-password-456"
	httpE2EWrongPassword   = "wrong-password-999"
	httpE2EPhone           = "+821012345678"
)

type httpE2EPreAuth struct {
	channel        string
	intentID       string
	csrfToken      string
	authFlowToken  string
	authFlowCookie *http.Cookie
}

type httpE2EIntentData struct {
	AuthIntentID  string    `json:"authIntentId"`
	ExpiresAt     time.Time `json:"expiresAt"`
	NextPath      string    `json:"nextPath"`
	CSRFToken     string    `json:"csrfToken"`
	AuthFlowToken string    `json:"authFlowToken"`
}

type httpE2ENext struct {
	Path     string `json:"path"`
	IntentID string `json:"intentId"`
}

type httpE2ESessionData struct {
	SessionID string    `json:"sessionId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type httpE2EAccessData struct {
	AccessToken          string    `json:"accessToken"`
	AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
}

type httpE2ETokenData struct {
	AccessToken           string    `json:"accessToken"`
	AccessTokenExpiresAt  time.Time `json:"accessTokenExpiresAt"`
	RefreshToken          string    `json:"refreshToken"`
	RefreshTokenExpiresAt time.Time `json:"refreshTokenExpiresAt"`
}

type httpE2EAuthenticationData struct {
	UserID             string             `json:"userId"`
	SessionID          string             `json:"sessionId"`
	CSRFToken          string             `json:"csrfToken"`
	CredentialDelivery string             `json:"credentialDelivery"`
	Session            httpE2ESessionData `json:"session"`
	Access             httpE2EAccessData  `json:"access"`
	Tokens             httpE2ETokenData   `json:"tokens"`
	Next               httpE2ENext        `json:"next"`
}

type httpE2EChallengeData struct {
	Status      string    `json:"status"`
	ChallengeID string    `json:"challengeId"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type httpE2EPasswordResetStartData struct {
	PasswordResetID string    `json:"passwordResetId"`
	Status          string    `json:"status"`
	MethodOptions   []string  `json:"methodOptions"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type httpE2EPasswordResetVerifyData struct {
	PasswordResetID    string    `json:"passwordResetId"`
	Status             string    `json:"status"`
	ExpiresAt          time.Time `json:"expiresAt"`
	CredentialDelivery string    `json:"credentialDelivery"`
	ResetGrant         string    `json:"resetGrant"`
}

type httpE2ERefreshData struct {
	CredentialDelivery    string             `json:"credentialDelivery"`
	Session               httpE2ESessionData `json:"session"`
	Access                httpE2EAccessData  `json:"access"`
	Tokens                httpE2ETokenData   `json:"tokens"`
	SessionID             string             `json:"-"`
	AccessToken           string             `json:"-"`
	AccessTokenExpiresAt  time.Time          `json:"-"`
	RefreshToken          string             `json:"-"`
	RefreshTokenExpiresAt time.Time          `json:"-"`
}

type httpE2EContextData struct {
	Authenticated     bool                  `json:"authenticated"`
	UserID            string                `json:"userId"`
	Session           httpE2EContextSession `json:"session"`
	LinkedMethodTypes []string              `json:"linkedMethodTypes"`
	CSRFToken         string                `json:"csrfToken"`
}

type httpE2EContextSession struct {
	SessionID            string    `json:"sessionId"`
	Channel              string    `json:"channel"`
	AuthenticationMethod string    `json:"authenticationMethod"`
	AuthenticatedAt      time.Time `json:"authenticatedAt"`
	ExpiresAt            time.Time `json:"expiresAt"`
}

type httpE2EConcurrentResult struct {
	response *httpE2EResponse
	err      error
}

func TestProductionHTTPSignInPasswordResetAndSessionAPIs(t *testing.T) {
	harness := newProductionHTTPHarness(t)
	t.Run("JWKS active key and conditional cache", func(t *testing.T) {
		response := harness.do(httpE2ERequest{Method: http.MethodGet, Path: "/.well-known/jwks.json"})
		if response.status != http.StatusOK || !strings.HasPrefix(response.header.Get("Cache-Control"), "public, max-age=") || response.header.Get("ETag") == "" || response.header.Get("Content-Type") != "application/json" {
			t.Fatal("JWKS response headers do not match the public cache behavior")
		}
		var document struct {
			Keys []struct {
				KeyID     string `json:"kid"`
				Algorithm string `json:"alg"`
				Use       string `json:"use"`
				Modulus   string `json:"n"`
				Exponent  string `json:"e"`
			} `json:"keys"`
		}
		if err := json.Unmarshal(response.body, &document); err != nil || len(document.Keys) != 1 || document.Keys[0].KeyID != "http-e2e-key" || document.Keys[0].Algorithm != "RS256" || document.Keys[0].Use != "sig" || document.Keys[0].Modulus == "" || document.Keys[0].Exponent == "" {
			t.Fatal("JWKS active public key is incomplete")
		}
		cached := harness.do(httpE2ERequest{Method: http.MethodGet, Path: "/.well-known/jwks.json", Headers: http.Header{"If-None-Match": {response.header.Get("ETag")}}})
		if cached.status != http.StatusNotModified || len(cached.body) != 0 {
			t.Fatal("JWKS conditional request did not return 304")
		}
	})
	userID, emailID, emailLinkID := uuid.New(), uuid.New(), uuid.New()
	phoneID, phoneLinkID := uuid.New(), uuid.New()
	email := "email-" + emailID.String() + "@example.test"
	seedEmailPrincipal(t, harness.ctx, harness.db, userID, emailID, emailLinkID, httpE2EInitialPassword)
	seedPhoneLink(t, harness.ctx, harness.db, userID, phoneID, phoneLinkID, httpE2EPhone)

	if !t.Run("API.A.300-07", func(t *testing.T) {
		wrongFlow := createHTTPPreAuth(t, harness, "ios")
		wrong := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/signins/email",
			JSON: map[string]any{
				"authIntentId": wrongFlow.intentID,
				"email":        email,
				"password":     httpE2EWrongPassword,
				"rememberMe":   false,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: wrongFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, wrong, http.StatusUnauthorized, "AUTH_SIGNIN_FAILED")
		assertResponseOmits(t, wrong, email, httpE2EWrongPassword, wrongFlow.authFlowToken)

		webFlow := createHTTPPreAuth(t, harness, "web")
		webResponse, webData := signInEmailHTTP(t, harness, webFlow, email, httpE2EInitialPassword, false)
		assertWebAuthenticationDelivery(t, webResponse, webData, false)
		assertResponseOmits(t, webResponse, email, httpE2EInitialPassword)

		mobileFlow := createHTTPPreAuth(t, harness, "ios")
		mobileResponse, mobileData := signInEmailHTTP(t, harness, mobileFlow, email, httpE2EInitialPassword, true)
		assertMobileAuthenticationDelivery(t, mobileResponse, mobileData)
		assertResponseOmits(t, mobileResponse, email, httpE2EInitialPassword)

		var argon2idHash string
		if err := harness.db.QueryRow(harness.ctx, `
			SELECT password_hash FROM auth_password_credentials
			WHERE identity_id = $1 AND password_status = 'active'
		`, emailID).Scan(&argon2idHash); err != nil {
			t.Fatal("read active Argon2id credential")
		}
		const legacyBcryptHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
		if _, err := harness.db.Exec(harness.ctx, `
			UPDATE auth_password_credentials
			SET password_hash = $2, hash_algorithm = 'bcrypt'
			WHERE identity_id = $1 AND password_status = 'active'
		`, emailID, legacyBcryptHash); err != nil {
			t.Fatal("set legacy bcrypt credential")
		}
		legacyFlow := createHTTPPreAuth(t, harness, "ios")
		legacy := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/signins/email",
			JSON: map[string]any{
				"authIntentId": legacyFlow.intentID,
				"email":        email,
				"password":     "password",
				"rememberMe":   false,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: legacyFlow.credentials(harness.origin),
		})
		if _, err := harness.db.Exec(harness.ctx, `
			UPDATE auth_password_credentials
			SET password_hash = $2, hash_algorithm = 'argon2id'
			WHERE identity_id = $1 AND password_status = 'active'
		`, emailID, argon2idHash); err != nil {
			t.Fatal("restore active Argon2id credential")
		}
		decodeHTTPError(t, legacy, http.StatusUnauthorized, "AUTH_SIGNIN_FAILED")
		assertResponseOmits(t, legacy, email, "password", legacyFlow.authFlowToken)

		if _, err := harness.db.Exec(harness.ctx, `
			UPDATE auth_identities
			SET credential_status = 'password_reset_required',
				password_reset_required_at = now(), password_reset_reason = 'integration_test'
			WHERE identity_id = $1
		`, emailID); err != nil {
			t.Fatal("set password reset required state")
		}
		wrongStateFlow := createHTTPPreAuth(t, harness, "ios")
		wrongState := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/signins/email",
			JSON: map[string]any{
				"authIntentId": wrongStateFlow.intentID,
				"email":        email,
				"password":     httpE2EWrongPassword,
				"rememberMe":   false,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: wrongStateFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, wrongState, http.StatusUnauthorized, "AUTH_SIGNIN_FAILED")
		assertResponseOmits(t, wrongState, email, httpE2EWrongPassword, wrongStateFlow.authFlowToken)

		correctStateFlow := createHTTPPreAuth(t, harness, "ios")
		correctState := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/signins/email",
			JSON: map[string]any{
				"authIntentId": correctStateFlow.intentID,
				"email":        email,
				"password":     httpE2EInitialPassword,
				"rememberMe":   false,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: correctStateFlow.credentials(harness.origin),
		})
		if _, err := harness.db.Exec(harness.ctx, `
			UPDATE auth_identities
			SET credential_status = 'active',
				password_reset_required_at = NULL, password_reset_reason = NULL
			WHERE identity_id = $1
		`, emailID); err != nil {
			t.Fatal("restore active credential state")
		}
		decodeHTTPError(t, correctState, http.StatusForbidden, "AUTH_PASSWORD_RESET_REQUIRED")
		assertResponseOmits(t, correctState, email, httpE2EInitialPassword, correctStateFlow.authFlowToken)
	}) {
		t.FailNow()
	}

	var phoneFlow httpE2EPreAuth
	var phoneChallengeID, phoneCode string
	if !t.Run("API.A.300-08", func(t *testing.T) {
		phoneFlow = createHTTPPreAuth(t, harness, "ios")
		invalid := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/signins/phone/challenges",
			JSON: map[string]any{
				"authIntentId": phoneFlow.intentID,
				"phone":        map[string]any{"countryCode": "+82", "nationalNumber": "invalid"},
				"rememberMe":   false,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: phoneFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, invalid, http.StatusBadRequest, "AUTH_INPUT_INVALID")
		assertResponseOmits(t, invalid, httpE2EPhone, phoneFlow.authFlowToken)

		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/signins/phone/challenges",
			JSON: map[string]any{
				"authIntentId": phoneFlow.intentID,
				"phone":        map[string]any{"countryCode": "+82", "nationalNumber": "1012345678"},
				"rememberMe":   false,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: phoneFlow.credentials(harness.origin),
		})
		var data httpE2EChallengeData
		decodeHTTPEnvelope(t, response, http.StatusAccepted, &data)
		if data.Status != "accepted" || data.ChallengeID == "" || data.ExpiresAt.IsZero() {
			t.Fatal("phone sign-in challenge response is incomplete")
		}
		assertResponseOmits(t, response, httpE2EPhone, phoneFlow.authFlowToken)
		phoneChallengeID = data.ChallengeID
		phoneCode = decryptDeliveryCode(t, harness.ctx, harness.db, phoneChallengeID)
	}) {
		t.FailNow()
	}

	if !t.Run("API.A.300-09", func(t *testing.T) {
		wrongCode := differentHTTPVerificationCode(phoneCode)
		wrong := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/signins/phone/challenges/" + phoneChallengeID + "/verify",
			JSON:        map[string]any{"code": wrongCode},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: phoneFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, wrong, http.StatusBadRequest, "AUTH_CHALLENGE_FAILED")
		assertResponseOmits(t, wrong, httpE2EPhone, phoneCode, wrongCode, phoneFlow.authFlowToken)

		response := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/signins/phone/challenges/" + phoneChallengeID + "/verify",
			JSON:        map[string]any{"code": phoneCode},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: phoneFlow.credentials(harness.origin),
		})
		var data httpE2EAuthenticationData
		decodeHTTPEnvelope(t, response, http.StatusOK, &data)
		assertMobileAuthenticationDelivery(t, response, data)
		assertResponseOmits(t, response, httpE2EPhone, phoneCode, phoneFlow.authFlowToken)
	}) {
		t.FailNow()
	}

	var resetFlow httpE2EPreAuth
	var passwordResetID string
	if !t.Run("API.A.300-10", func(t *testing.T) {
		resetFlow = createHTTPPreAuth(t, harness, "ios")
		invalid := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/password-resets",
			JSON: map[string]any{
				"identifierType": "unsupported",
				"email":          email,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: resetFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, invalid, http.StatusBadRequest, "AUTH_INPUT_INVALID")
		assertResponseOmits(t, invalid, email, resetFlow.authFlowToken)

		response := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/password-resets",
			JSON: map[string]any{
				"identifierType": "email",
				"email":          email,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: resetFlow.credentials(harness.origin),
		})
		var data httpE2EPasswordResetStartData
		decodeHTTPEnvelope(t, response, http.StatusAccepted, &data)
		if data.PasswordResetID == "" || data.Status != "accepted" || len(data.MethodOptions) == 0 || data.ExpiresAt.IsZero() {
			t.Fatal("password reset start response is incomplete")
		}
		assertResponseOmits(t, response, email, resetFlow.authFlowToken)
		passwordResetID = data.PasswordResetID
	}) {
		t.FailNow()
	}

	var resetChallengeID, resetCode string
	if !t.Run("API.A.300-11", func(t *testing.T) {
		invalid := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/password-resets/" + passwordResetID + "/challenges",
			JSON:        map[string]any{"method": "unsupported"},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: resetFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, invalid, http.StatusBadRequest, "AUTH_INPUT_INVALID")
		assertResponseOmits(t, invalid, email, resetFlow.authFlowToken)

		response := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/password-resets/" + passwordResetID + "/challenges",
			JSON:        map[string]any{"method": "email"},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: resetFlow.credentials(harness.origin),
		})
		var data httpE2EChallengeData
		decodeHTTPEnvelope(t, response, http.StatusAccepted, &data)
		if data.Status != "accepted" || data.ChallengeID == "" || data.ExpiresAt.IsZero() {
			t.Fatal("password reset challenge response is incomplete")
		}
		assertResponseOmits(t, response, email, resetFlow.authFlowToken)
		resetChallengeID = data.ChallengeID
		resetCode = decryptDeliveryCode(t, harness.ctx, harness.db, resetChallengeID)
	}) {
		t.FailNow()
	}

	var resetGrant string
	if !t.Run("API.A.300-12", func(t *testing.T) {
		wrongCode := differentHTTPVerificationCode(resetCode)
		wrong := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/password-resets/" + passwordResetID + "/challenges/" + resetChallengeID + "/verify",
			JSON:        map[string]any{"code": wrongCode},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: resetFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, wrong, http.StatusBadRequest, "AUTH_CHALLENGE_FAILED")
		assertResponseOmits(t, wrong, email, resetCode, wrongCode, resetFlow.authFlowToken)

		response := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/password-resets/" + passwordResetID + "/challenges/" + resetChallengeID + "/verify",
			JSON:        map[string]any{"code": resetCode},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: resetFlow.credentials(harness.origin),
		})
		var data httpE2EPasswordResetVerifyData
		decodeHTTPEnvelope(t, response, http.StatusOK, &data)
		if data.PasswordResetID != passwordResetID || data.Status != "verified" || data.CredentialDelivery != "mobile_reset_grant" || data.ResetGrant == "" || data.ExpiresAt.IsZero() {
			t.Fatal("password reset verification response is incomplete")
		}
		assertResponseOmits(t, response, email, resetCode, resetFlow.authFlowToken)
		resetGrant = data.ResetGrant
	}) {
		t.FailNow()
	}

	var postResetMobile httpE2EAuthenticationData
	if !t.Run("API.A.300-13", func(t *testing.T) {
		weakPassword := "too-short"
		weak := harness.do(httpE2ERequest{
			Method: http.MethodPut,
			Path:   "/api/v1/auth/password-resets/" + passwordResetID + "/password",
			JSON: map[string]any{
				"credentialDelivery": "mobile_reset_grant",
				"resetGrant":         resetGrant,
				"newPassword":        weakPassword,
				"confirmPassword":    weakPassword,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: resetFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, weak, http.StatusUnprocessableEntity, "AUTH_PASSWORD_POLICY_NOT_MET")
		assertResponseOmits(t, weak, email, resetCode, resetGrant, weakPassword, resetFlow.authFlowToken)
		if _, err := harness.db.Exec(harness.ctx, `
			UPDATE auth_identities
			SET credential_status = 'password_reset_required',
				password_reset_required_at = now(), password_reset_reason = 'integration_test'
			WHERE identity_id = $1
		`, emailID); err != nil {
			t.Fatalf("require password reset before completion: %v", err)
		}

		response := harness.do(httpE2ERequest{
			Method: http.MethodPut,
			Path:   "/api/v1/auth/password-resets/" + passwordResetID + "/password",
			JSON: map[string]any{
				"credentialDelivery": "mobile_reset_grant",
				"resetGrant":         resetGrant,
				"newPassword":        httpE2ENewPassword,
				"confirmPassword":    httpE2ENewPassword,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: resetFlow.credentials(harness.origin),
		})
		assertHTTPNoContent(t, response, http.StatusNoContent)
		assertResponseOmits(t, response, email, resetCode, resetGrant, httpE2ENewPassword, resetFlow.authFlowToken)

		var activeHash, activeAlgorithm string
		if err := harness.db.QueryRow(harness.ctx, `
			SELECT p.password_hash, p.hash_algorithm
			FROM auth_password_credentials p
			JOIN auth_identities i ON i.identity_id = p.identity_id
			WHERE i.normalized_value = $1 AND p.password_status = 'active'
		`, email).Scan(&activeHash, &activeAlgorithm); err != nil {
			t.Fatal("read active password credential after reset")
		}
		if activeAlgorithm != "argon2id" || !strings.HasPrefix(activeHash, "$argon2id$v=19$") {
			t.Fatal("reset password credential is not an Argon2id PHC hash")
		}
		var credentialState string
		if err := harness.db.QueryRow(harness.ctx, `
			SELECT credential_status FROM auth_identities WHERE identity_id = $1
		`, emailID).Scan(&credentialState); err != nil {
			t.Fatal("read identity credential state after reset")
		}
		if credentialState != "active" {
			t.Fatalf("identity credential state after reset = %q, want active", credentialState)
		}
		var replacedHash sql.NullString
		if err := harness.db.QueryRow(harness.ctx, `
			SELECT p.password_hash
			FROM auth_password_credentials p
			JOIN auth_identities i ON i.identity_id = p.identity_id
			WHERE i.normalized_value = $1 AND p.password_status = 'replaced'
			ORDER BY p.replaced_at DESC
			LIMIT 1
		`, email).Scan(&replacedHash); err != nil {
			t.Fatal("read replaced password credential after reset")
		}
		if replacedHash.Valid {
			t.Fatal("replaced password credential retained its verifier")
		}

		verificationFlow := createHTTPPreAuth(t, harness, "ios")
		oldPassword := harness.do(httpE2ERequest{
			Method: http.MethodPost,
			Path:   "/api/v1/auth/signins/email",
			JSON: map[string]any{
				"authIntentId": verificationFlow.intentID,
				"email":        email,
				"password":     httpE2EInitialPassword,
				"rememberMe":   false,
			},
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: verificationFlow.credentials(harness.origin),
		})
		decodeHTTPError(t, oldPassword, http.StatusUnauthorized, "AUTH_SIGNIN_FAILED")
		assertResponseOmits(t, oldPassword, email, httpE2EInitialPassword, verificationFlow.authFlowToken)

		newPassword, data := signInEmailHTTP(t, harness, verificationFlow, email, httpE2ENewPassword, false)
		assertMobileAuthenticationDelivery(t, newPassword, data)
		assertResponseOmits(t, newPassword, email, httpE2ENewPassword)
		postResetMobile = data
	}) {
		t.FailNow()
	}

	var refreshedForLogout httpE2ERefreshData
	if !t.Run("API.A.300-14", func(t *testing.T) {
		sequentialKey := uuid.NewString()
		firstResponse, first := refreshHTTP(t, harness, postResetMobile.Tokens.RefreshToken, sequentialKey)
		secondResponse, second := refreshHTTP(t, harness, postResetMobile.Tokens.RefreshToken, sequentialKey)
		if !sameHTTPRefresh(first, second) {
			t.Fatal("sequential refresh replay did not return the original credential set")
		}
		assertResponseOmits(t, firstResponse, postResetMobile.Tokens.RefreshToken)
		assertResponseOmits(t, secondResponse, postResetMobile.Tokens.RefreshToken)
		refreshedForLogout = first

		_, concurrentSession := signInEmailHTTP(t, harness, createHTTPPreAuth(t, harness, "ios"), email, httpE2ENewPassword, false)
		assertMobileAuthenticationDelivery(t, nil, concurrentSession)
		concurrentKey := uuid.NewString()
		const requestCount = 4
		start := make(chan struct{})
		responses := make(chan httpE2EConcurrentResult, requestCount)
		var wait sync.WaitGroup
		for range requestCount {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				response, err := concurrentRefreshHTTP(harness, concurrentSession.Tokens.RefreshToken, concurrentKey)
				responses <- httpE2EConcurrentResult{response: response, err: err}
			}()
		}
		close(start)
		wait.Wait()
		close(responses)

		var concurrentFirst *httpE2ERefreshData
		responseCount := 0
		for result := range responses {
			if result.err != nil {
				t.Fatal("execute concurrent refresh request")
			}
			var data httpE2ERefreshData
			decodeHTTPEnvelope(t, result.response, http.StatusOK, &data)
			normalizeHTTPRefresh(&data)
			assertCompleteHTTPRefresh(t, data)
			assertResponseOmits(t, result.response, concurrentSession.Tokens.RefreshToken)
			if concurrentFirst == nil {
				copy := data
				concurrentFirst = &copy
			} else if !sameHTTPRefresh(*concurrentFirst, data) {
				t.Fatal("concurrent refresh replay returned different credential sets")
			}
			responseCount++
		}
		if responseCount != requestCount {
			t.Fatal("concurrent refresh replay did not return every response")
		}

		reuse := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/sessions/refresh",
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: httpE2ECredentials{RefreshToken: concurrentSession.Tokens.RefreshToken},
		})
		decodeHTTPError(t, reuse, http.StatusUnauthorized, "AUTH_SESSION_REVOKED")
		assertResponseOmits(t, reuse, concurrentSession.Tokens.RefreshToken)

		webFlow := createHTTPPreAuth(t, harness, "web")
		webSignInResponse, webSession := signInEmailHTTP(t, harness, webFlow, email, httpE2ENewPassword, true)
		oldCookie := responseCookie(t, webSignInResponse, "__Host-dm_refresh")
		webKey := uuid.NewString()
		webRequest := httpE2ERequest{
			Method:  http.MethodPost,
			Path:    "/api/v1/auth/sessions/refresh",
			Headers: idempotencyHTTPHeaders(webKey),
			Credentials: httpE2ECredentials{
				SessionCookie: oldCookie, CSRFToken: webSession.CSRFToken, Origin: harness.origin,
			},
		}
		webFirstResponse := harness.do(webRequest)
		var webFirst httpE2ERefreshData
		decodeHTTPEnvelope(t, webFirstResponse, http.StatusOK, &webFirst)
		normalizeHTTPRefresh(&webFirst)
		if webFirst.CredentialDelivery != "web_jwt_refresh_cookie" || webFirst.SessionID == "" || webFirst.AccessToken == "" || !webFirst.RefreshTokenExpiresAt.IsZero() {
			t.Fatal("web refresh response is incomplete")
		}
		newCookie := responseCookie(t, webFirstResponse, "__Host-dm_refresh")
		assertCredentialCookie(t, newCookie, "__Host-dm_refresh")
		webReplayResponse := harness.do(webRequest)
		var webReplay httpE2ERefreshData
		decodeHTTPEnvelope(t, webReplayResponse, http.StatusOK, &webReplay)
		normalizeHTTPRefresh(&webReplay)
		replayedCookie := responseCookie(t, webReplayResponse, "__Host-dm_refresh")
		if !sameHTTPRefresh(webFirst, webReplay) || replayedCookie.Value != newCookie.Value {
			t.Fatal("web refresh replay changed the original credential delivery")
		}
		webReuseRequest := webRequest
		webReuseRequest.Headers = idempotencyHTTPHeaders(uuid.NewString())
		decodeHTTPError(t, harness.do(webReuseRequest), http.StatusUnauthorized, "AUTH_SESSION_REVOKED")
	}) {
		t.FailNow()
	}

	if !t.Run("API.A.300-15", func(t *testing.T) {
		missingKey := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/sessions/logout",
			RawJSON:     []byte(`{}`),
			Credentials: httpE2ECredentials{RefreshToken: refreshedForLogout.RefreshToken},
		})
		decodeHTTPError(t, missingKey, http.StatusBadRequest, "AUTH_INPUT_INVALID")
		assertResponseOmits(t, missingKey, refreshedForLogout.RefreshToken)
		invalidKey := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/sessions/logout",
			RawJSON:     []byte(`{}`),
			Headers:     idempotencyHTTPHeaders("not-a-uuid"),
			Credentials: httpE2ECredentials{RefreshToken: refreshedForLogout.RefreshToken},
		})
		decodeHTTPError(t, invalidKey, http.StatusBadRequest, "AUTH_INPUT_INVALID")
		assertResponseOmits(t, invalidKey, refreshedForLogout.RefreshToken)

		for _, test := range []struct {
			name string
			body []byte
		}{
			{name: "unknown field", body: []byte(`{"unexpected":true}`)},
			{name: "trailing value", body: []byte(`{} {}`)},
		} {
			t.Run(test.name, func(t *testing.T) {
				response := harness.do(httpE2ERequest{
					Method:      http.MethodPost,
					Path:        "/api/v1/auth/sessions/logout",
					RawJSON:     test.body,
					Headers:     idempotencyHTTPHeaders(uuid.NewString()),
					Credentials: httpE2ECredentials{RefreshToken: refreshedForLogout.RefreshToken},
				})
				decodeHTTPError(t, response, http.StatusBadRequest, "AUTH_INPUT_INVALID")
				assertResponseOmits(t, response, refreshedForLogout.RefreshToken)
			})
		}

		logoutKey := uuid.NewString()
		logoutRequest := httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/sessions/logout",
			RawJSON:     []byte(`{}`),
			Headers:     idempotencyHTTPHeaders(logoutKey),
			Credentials: httpE2ECredentials{RefreshToken: refreshedForLogout.RefreshToken},
		}
		logout := harness.do(logoutRequest)
		assertHTTPNoContent(t, logout, http.StatusNoContent)
		assertResponseOmits(t, logout, refreshedForLogout.RefreshToken)
		assertHTTPNoContent(t, harness.do(logoutRequest), http.StatusNoContent)

		conflict := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/sessions/logout",
			RawJSON:     []byte(`{}`),
			Headers:     idempotencyHTTPHeaders(logoutKey),
			Credentials: httpE2ECredentials{RefreshToken: postResetMobile.Tokens.RefreshToken},
		})
		decodeHTTPError(t, conflict, http.StatusConflict, "AUTH_IDEMPOTENCY_CONFLICT")
		assertResponseOmits(t, conflict, postResetMobile.Tokens.RefreshToken)

		otherMobileResponse, otherMobile := signInEmailHTTP(t, harness, createHTTPPreAuth(t, harness, "ios"), email, httpE2ENewPassword, false)
		assertMobileAuthenticationDelivery(t, otherMobileResponse, otherMobile)
		independentScope := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/sessions/logout",
			RawJSON:     []byte(`{}`),
			Headers:     idempotencyHTTPHeaders(logoutKey),
			Credentials: httpE2ECredentials{RefreshToken: otherMobile.Tokens.RefreshToken},
		})
		assertHTTPNoContent(t, independentScope, http.StatusNoContent)

		missing := harness.do(httpE2ERequest{
			Method:      http.MethodPost,
			Path:        "/api/v1/auth/sessions/logout",
			RawJSON:     []byte(`{}`),
			Headers:     idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: httpE2ECredentials{RefreshToken: "invalid-refresh-credential"},
		})
		decodeHTTPError(t, missing, http.StatusUnauthorized, "AUTH_SESSION_REQUIRED")
		assertResponseOmits(t, missing, "invalid-refresh-credential")

		webResponse, web := signInEmailHTTP(t, harness, createHTTPPreAuth(t, harness, "web"), email, httpE2ENewPassword, true)
		assertWebAuthenticationDelivery(t, webResponse, web, true)
		webCookie := responseCookie(t, webResponse, "__Host-dm_refresh")
		missingCSRF := harness.do(httpE2ERequest{
			Method:  http.MethodPost,
			Path:    "/api/v1/auth/sessions/logout",
			Headers: idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: httpE2ECredentials{
				SessionCookie: webCookie,
				Origin:        harness.origin,
			},
		})
		decodeHTTPError(t, missingCSRF, http.StatusForbidden, "AUTH_CSRF_INVALID")
		assertResponseOmits(t, missingCSRF, webCookie.Value, web.CSRFToken)

		webLogoutRequest := httpE2ERequest{
			Method:  http.MethodPost,
			Path:    "/api/v1/auth/sessions/logout",
			Headers: idempotencyHTTPHeaders(uuid.NewString()),
			Credentials: httpE2ECredentials{
				SessionCookie: webCookie,
				CSRFToken:     web.CSRFToken,
				Origin:        harness.origin,
			},
		}
		webLogout := harness.do(webLogoutRequest)
		assertHTTPNoContent(t, webLogout, http.StatusNoContent)
		clearedResponseCookie(t, webLogout, "__Host-dm_refresh")
		assertResponseOmits(t, webLogout, webCookie.Value, web.CSRFToken)
		webLogoutReplay := harness.do(webLogoutRequest)
		assertHTTPNoContent(t, webLogoutReplay, http.StatusNoContent)
		clearedResponseCookie(t, webLogoutReplay, "__Host-dm_refresh")
	}) {
		t.FailNow()
	}

	if !t.Run("API.A.300-16", func(t *testing.T) {
		anonymous := harness.do(httpE2ERequest{
			Method: http.MethodGet,
			Path:   "/api/v1/auth/context",
			Headers: http.Header{
				"X-User-Id":    []string{uuid.NewString()},
				"X-Session-Id": []string{uuid.NewString()},
				"X-Token-Id":   []string{uuid.NewString()},
			},
		})
		decodeHTTPError(t, anonymous, http.StatusUnauthorized, "AUTH_SESSION_REQUIRED")
		if anonymous.header.Get("Vary") != "Cookie, Authorization" {
			t.Fatal("unauthenticated auth context is missing the Vary header")
		}

		mobileResponse, mobile := signInEmailHTTP(t, harness, createHTTPPreAuth(t, harness, "ios"), email, httpE2ENewPassword, false)
		assertMobileAuthenticationDelivery(t, mobileResponse, mobile)
		bearer := harness.do(httpE2ERequest{
			Method:      http.MethodGet,
			Path:        "/api/v1/auth/context",
			Credentials: httpE2ECredentials{AccessToken: mobile.Tokens.AccessToken},
		})
		var bearerData httpE2EContextData
		decodeHTTPEnvelope(t, bearer, http.StatusOK, &bearerData)
		assertAuthenticatedHTTPContext(t, bearer, bearerData, userID.String(), "ios")

		webResponse, web := signInEmailHTTP(t, harness, createHTTPPreAuth(t, harness, "web"), email, httpE2ENewPassword, false)
		assertWebAuthenticationDelivery(t, webResponse, web, false)
		webCookie := responseCookie(t, webResponse, "__Host-dm_refresh")
		webContext := harness.do(httpE2ERequest{
			Method:      http.MethodGet,
			Path:        "/api/v1/auth/context",
			Credentials: httpE2ECredentials{SessionCookie: webCookie},
		})
		decodeHTTPError(t, webContext, http.StatusUnauthorized, "AUTH_SESSION_REQUIRED")

		multiple := harness.do(httpE2ERequest{
			Method: http.MethodGet,
			Path:   "/api/v1/auth/context",
			Credentials: httpE2ECredentials{
				SessionCookie: webCookie,
				AccessToken:   mobile.Tokens.AccessToken,
			},
		})
		decodeHTTPError(t, multiple, http.StatusBadRequest, "AUTH_MULTIPLE_CREDENTIALS")
		if multiple.header.Get("Vary") != "Cookie, Authorization" {
			t.Fatal("multiple-credential auth context is missing the Vary header")
		}
		assertResponseOmits(t, multiple, webCookie.Value, mobile.Tokens.AccessToken, web.CSRFToken)
	}) {
		t.FailNow()
	}
}

func createHTTPPreAuth(t *testing.T, harness *httpE2EHarness, channel string) httpE2EPreAuth {
	t.Helper()
	response := harness.do(httpE2ERequest{
		Method: http.MethodPost,
		Path:   "/api/v1/auth/intents",
		JSON: map[string]any{
			"returnPath": "/account/security",
			"intentType": "navigation",
		},
		Headers: http.Header{
			"Idempotency-Key":  []string{uuid.NewString()},
			"X-Client-Channel": []string{channel},
		},
	})
	var data httpE2EIntentData
	decodeHTTPEnvelope(t, response, http.StatusCreated, &data)
	if data.AuthIntentID == "" || data.NextPath == "" || data.ExpiresAt.IsZero() {
		t.Fatal("authentication intent response is incomplete")
	}
	result := httpE2EPreAuth{channel: channel, intentID: data.AuthIntentID, csrfToken: data.CSRFToken, authFlowToken: data.AuthFlowToken}
	if channel == "web" {
		if result.csrfToken == "" || result.authFlowToken != "" {
			t.Fatal("web authentication intent used the wrong credential delivery")
		}
		result.authFlowCookie = responseCookie(t, response, "__Host-dm_auth")
		assertCredentialCookie(t, result.authFlowCookie, "__Host-dm_auth")
		if result.authFlowCookie.MaxAge <= 0 {
			t.Fatal("web authentication intent cookie is missing its lifetime")
		}
		return result
	}
	if channel != "ios" || result.authFlowToken == "" || result.csrfToken != "" || len(response.cookies) != 0 {
		t.Fatal("mobile authentication intent used the wrong credential delivery")
	}
	return result
}

func (flow httpE2EPreAuth) credentials(origin string) httpE2ECredentials {
	if flow.channel == "web" {
		return httpE2ECredentials{AuthFlowCookie: flow.authFlowCookie, CSRFToken: flow.csrfToken, Origin: origin}
	}
	return httpE2ECredentials{AuthFlowToken: flow.authFlowToken}
}

func signInEmailHTTP(t *testing.T, harness *httpE2EHarness, flow httpE2EPreAuth, email, password string, rememberMe bool) (*httpE2EResponse, httpE2EAuthenticationData) {
	t.Helper()
	response := harness.do(httpE2ERequest{
		Method: http.MethodPost,
		Path:   "/api/v1/auth/signins/email",
		JSON: map[string]any{
			"authIntentId": flow.intentID,
			"email":        email,
			"password":     password,
			"rememberMe":   rememberMe,
		},
		Headers:     idempotencyHTTPHeaders(uuid.NewString()),
		Credentials: flow.credentials(harness.origin),
	})
	var data httpE2EAuthenticationData
	decodeHTTPEnvelope(t, response, http.StatusOK, &data)
	if flow.channel == "web" {
		data.CSRFToken = flow.csrfToken
	}
	return response, data
}

func assertWebAuthenticationDelivery(t *testing.T, response *httpE2EResponse, data httpE2EAuthenticationData, rememberMe bool) {
	t.Helper()
	if response == nil {
		t.Fatal("web authentication response is nil")
	}
	if data.CredentialDelivery != "web_jwt_refresh_cookie" || data.UserID == "" || data.Session.SessionID == "" || data.Session.ExpiresAt.IsZero() || data.Access.AccessToken == "" || data.Access.AccessTokenExpiresAt.IsZero() || data.CSRFToken == "" || data.Next.Path == "" {
		t.Fatal("web authentication response is incomplete")
	}
	sessionCookie := responseCookie(t, response, "__Host-dm_refresh")
	assertCredentialCookie(t, sessionCookie, "__Host-dm_refresh")
	clearedResponseCookie(t, response, "__Host-dm_auth")
	if (!rememberMe && sessionCookie.MaxAge != 0) || (rememberMe && sessionCookie.MaxAge <= 0) {
		t.Fatal("web session cookie persistence does not match rememberMe")
	}
}

func assertMobileAuthenticationDelivery(t *testing.T, response *httpE2EResponse, data httpE2EAuthenticationData) {
	t.Helper()
	if data.CredentialDelivery != "mobile_tokens" || data.UserID == "" || data.Session.SessionID == "" || data.Session.ExpiresAt.IsZero() || data.Tokens.AccessToken == "" || data.Tokens.RefreshToken == "" || data.Tokens.AccessTokenExpiresAt.IsZero() || data.Tokens.RefreshTokenExpiresAt.IsZero() || data.Next.Path == "" {
		t.Fatal("mobile authentication response is incomplete")
	}
	if response != nil && len(response.cookies) != 0 {
		t.Fatal("mobile authentication response unexpectedly set a cookie")
	}
	if data.Session.ExpiresAt.Before(data.Tokens.RefreshTokenExpiresAt) {
		t.Fatal("mobile session expires before its refresh credential")
	}
}

func refreshHTTP(t *testing.T, harness *httpE2EHarness, refreshToken, idempotencyKey string) (*httpE2EResponse, httpE2ERefreshData) {
	t.Helper()
	response := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/sessions/refresh",
		Headers:     idempotencyHTTPHeaders(idempotencyKey),
		Credentials: httpE2ECredentials{RefreshToken: refreshToken},
	})
	var data httpE2ERefreshData
	decodeHTTPEnvelope(t, response, http.StatusOK, &data)
	normalizeHTTPRefresh(&data)
	assertCompleteHTTPRefresh(t, data)
	return response, data
}

// concurrentRefreshHTTP intentionally avoids testing.T so worker goroutines
// can report failures back to the test goroutine without calling FailNow.
func concurrentRefreshHTTP(harness *httpE2EHarness, refreshToken, idempotencyKey string) (*httpE2EResponse, error) {
	requestID := uuid.NewString()
	request, err := http.NewRequestWithContext(harness.ctx, http.MethodPost, harness.baseURL+"/api/v1/auth/sessions/refresh", nil)
	if err != nil {
		return nil, errors.New("create concurrent refresh request")
	}
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("X-Request-Id", requestID)
	request.Header.Set("X-Refresh-Token", refreshToken)
	response, err := harness.client.Do(request)
	if err != nil {
		return nil, errors.New("execute concurrent refresh request")
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, httpE2EMaxResponseBytes+1))
	if err != nil || len(responseBody) > httpE2EMaxResponseBytes {
		return nil, errors.New("read concurrent refresh response")
	}
	return &httpE2EResponse{
		status:            response.StatusCode,
		header:            response.Header.Clone(),
		cookies:           response.Cookies(),
		body:              responseBody,
		inboundRequestID:  requestID,
		requestIDWasValid: true,
	}, nil
}

func assertCompleteHTTPRefresh(t *testing.T, data httpE2ERefreshData) {
	t.Helper()
	if data.SessionID == "" || data.AccessToken == "" || data.RefreshToken == "" || data.AccessTokenExpiresAt.IsZero() || data.RefreshTokenExpiresAt.IsZero() {
		t.Fatal("refresh response is incomplete")
	}
}

func normalizeHTTPRefresh(data *httpE2ERefreshData) {
	data.SessionID = data.Session.SessionID
	if data.CredentialDelivery == "web_jwt_refresh_cookie" {
		data.AccessToken = data.Access.AccessToken
		data.AccessTokenExpiresAt = data.Access.AccessTokenExpiresAt
		return
	}
	data.AccessToken, data.AccessTokenExpiresAt = data.Tokens.AccessToken, data.Tokens.AccessTokenExpiresAt
	data.RefreshToken, data.RefreshTokenExpiresAt = data.Tokens.RefreshToken, data.Tokens.RefreshTokenExpiresAt
}

func sameHTTPRefresh(left, right httpE2ERefreshData) bool {
	return left.SessionID == right.SessionID && left.AccessToken == right.AccessToken && left.RefreshToken == right.RefreshToken && left.AccessTokenExpiresAt.Equal(right.AccessTokenExpiresAt) && left.RefreshTokenExpiresAt.Equal(right.RefreshTokenExpiresAt)
}

func assertAuthenticatedHTTPContext(t *testing.T, response *httpE2EResponse, data httpE2EContextData, userID, channel string) {
	t.Helper()
	if !data.Authenticated || data.UserID != userID || data.Session.SessionID == "" || data.Session.Channel != channel || data.Session.AuthenticationMethod == "" || data.Session.AuthenticatedAt.IsZero() || data.Session.ExpiresAt.IsZero() {
		t.Fatal("authenticated context response is incomplete")
	}
	if response.header.Get("Vary") != "Cookie, Authorization" {
		t.Fatal("authenticated context response is missing the Vary header")
	}
}

func idempotencyHTTPHeaders(key string) http.Header {
	return http.Header{"Idempotency-Key": []string{key}}
}

func differentHTTPVerificationCode(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}
