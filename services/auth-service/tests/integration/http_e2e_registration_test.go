//go:build integration

package integration_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

type registrationHTTPIntentData struct {
	AuthIntentID string    `json:"authIntentId"`
	ExpiresAt    time.Time `json:"expiresAt"`
	NextPath     string    `json:"nextPath"`
	CSRFToken    string    `json:"csrfToken"`
}

type registrationHTTPMethodsData struct {
	IntentID string `json:"intentId"`
	Methods  []struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	} `json:"methods"`
}

type registrationHTTPStartData struct {
	RegistrationID                  string    `json:"registrationId"`
	Status                          string    `json:"status"`
	RequiredVerifications           []string  `json:"requiredVerifications"`
	VerifiedMethods                 []string  `json:"verifiedMethods"`
	ExpiresAt                       time.Time `json:"expiresAt"`
	RegistrationStatusToken         string    `json:"registrationStatusToken"`
	RegistrationStatusTokenExpireAt time.Time `json:"registrationStatusTokenExpiresAt"`
}

type registrationHTTPChallengeData struct {
	ChallengeID       string    `json:"challengeId"`
	Method            string    `json:"method"`
	MaskedDestination string    `json:"maskedDestination"`
	ExpiresAt         time.Time `json:"expiresAt"`
	ResendAvailableAt time.Time `json:"resendAvailableAt"`
}

type registrationHTTPVerifyData struct {
	ChallengeID  string `json:"challengeId"`
	Status       string `json:"status"`
	Registration struct {
		Status                      string   `json:"status"`
		VerifiedMethods             []string `json:"verifiedMethods"`
		RequiredVerifications       []string `json:"requiredVerifications"`
		RegistrationCompletionProof string   `json:"registrationCompletionProof"`
	} `json:"registration"`
}

type registrationHTTPStatusData struct {
	RegistrationID  string    `json:"registrationId"`
	Status          string    `json:"status"`
	VerifiedMethods []string  `json:"verifiedMethods"`
	Retryable       bool      `json:"retryable"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type registrationHTTPCompletedData struct {
	RegistrationID     string `json:"registrationId"`
	Status             string `json:"status"`
	CredentialDelivery string `json:"credentialDelivery"`
	UserID             string `json:"userId"`
	Session            struct {
		SessionID string    `json:"sessionId"`
		ExpiresAt time.Time `json:"expiresAt"`
	} `json:"session"`
	Access struct {
		AccessToken          string    `json:"accessToken"`
		AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
	} `json:"access"`
	Next struct {
		Path     string `json:"path"`
		IntentID string `json:"intentId"`
	} `json:"next"`
}

func TestProductionHTTPRegistrationAPI(t *testing.T) {
	harness := newProductionHTTPHarness(t)
	email := "registration-" + uuid.NewString() + "@example.test"
	phoneCountryCode, phoneNationalNumber := "+82", "1012345678"
	phone := phoneCountryCode + phoneNationalNumber
	password := "http-e2e-registration-password"
	profileRequestID, agreementReceiptID := uuid.NewString(), uuid.NewString()

	invalidRedirect := harness.do(httpE2ERequest{
		Method: http.MethodPost,
		Path:   "/api/v1/auth/intents",
		JSON:   map[string]any{"returnPath": "https://outside.example.test", "intentType": "navigation"},
		Headers: registrationHTTPHeaders(uuid.NewString(), map[string]string{
			"X-Client-Channel": "web",
		}),
	})
	decodeHTTPError(t, invalidRedirect, http.StatusBadRequest, "AUTH_REDIRECT_INVALID")

	intentData, authFlowCookie := registrationHTTPCreateWebIntent(t, harness, "/drops/registration")
	methodsResponse := harness.do(httpE2ERequest{
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/methods?intentId=" + intentData.AuthIntentID,
		Credentials: httpE2ECredentials{AuthFlowCookie: authFlowCookie},
	})
	var methodsData registrationHTTPMethodsData
	decodeHTTPEnvelope(t, methodsResponse, http.StatusOK, &methodsData)
	if methodsData.IntentID != intentData.AuthIntentID || len(methodsData.Methods) != 2 {
		t.Fatal("API.A.300-02 success data does not match the expected result")
	}

	missingIntent := harness.do(httpE2ERequest{
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/methods?intentId=" + uuid.NewString(),
		Credentials: httpE2ECredentials{AuthFlowCookie: authFlowCookie},
	})
	decodeHTTPError(t, missingIntent, http.StatusNotFound, "AUTH_INTENT_NOT_FOUND")

	registrationBody := registrationHTTPBody(intentData.AuthIntentID, email, password, phoneCountryCode, phoneNationalNumber, profileRequestID, agreementReceiptID)
	registrationBody["unexpected"] = true
	strictJSON := harness.do(httpE2ERequest{
		Method:  http.MethodPost,
		Path:    "/api/v1/auth/registrations",
		JSON:    registrationBody,
		Headers: registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: httpE2ECredentials{
			AuthFlowCookie: authFlowCookie,
			CSRFToken:      intentData.CSRFToken,
			Origin:         harness.origin,
		},
	})
	decodeHTTPError(t, strictJSON, http.StatusBadRequest, "AUTH_INPUT_INVALID")
	assertResponseOmits(t, strictJSON, email, phone, password)

	missingCSRF := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations",
		JSON:        registrationHTTPBody(intentData.AuthIntentID, email, password, phoneCountryCode, phoneNationalNumber, profileRequestID, agreementReceiptID),
		Headers:     registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: httpE2ECredentials{AuthFlowCookie: authFlowCookie, Origin: harness.origin},
	})
	decodeHTTPError(t, missingCSRF, http.StatusForbidden, "AUTH_CSRF_INVALID")
	assertResponseOmits(t, missingCSRF, email, phone, password)

	invalidOrigin := harness.do(httpE2ERequest{
		Method:  http.MethodPost,
		Path:    "/api/v1/auth/registrations",
		JSON:    registrationHTTPBody(intentData.AuthIntentID, email, password, phoneCountryCode, phoneNationalNumber, profileRequestID, agreementReceiptID),
		Headers: registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: httpE2ECredentials{
			AuthFlowCookie: authFlowCookie,
			CSRFToken:      intentData.CSRFToken,
			Origin:         "https://untrusted.example.test",
		},
	})
	decodeHTTPError(t, invalidOrigin, http.StatusForbidden, "AUTH_CSRF_INVALID")
	assertResponseOmits(t, invalidOrigin, email, phone, password)

	startKey := uuid.NewString()
	startResponse := harness.do(httpE2ERequest{
		Method:  http.MethodPost,
		Path:    "/api/v1/auth/registrations",
		JSON:    registrationHTTPBody(intentData.AuthIntentID, email, password, phoneCountryCode, phoneNationalNumber, profileRequestID, agreementReceiptID),
		Headers: registrationHTTPHeaders(startKey, nil),
		Credentials: httpE2ECredentials{
			AuthFlowCookie: authFlowCookie,
			CSRFToken:      intentData.CSRFToken,
			Origin:         harness.origin,
		},
	})
	var startData registrationHTTPStartData
	decodeHTTPEnvelope(t, startResponse, http.StatusCreated, &startData)
	if startData.RegistrationID == "" || startData.Status != "pending_verification" || len(startData.RequiredVerifications) != 2 || startData.RegistrationStatusToken == "" || startData.ExpiresAt.IsZero() || startData.RegistrationStatusTokenExpireAt.IsZero() {
		t.Fatal("API.A.300-03 success data does not match the expected result")
	}
	assertResponseOmits(t, startResponse, email, phone, password)

	secondIntent, secondAuthFlowCookie := registrationHTTPCreateWebIntent(t, harness, "/drops/registration-conflict")
	identifierConflict := harness.do(httpE2ERequest{
		Method:  http.MethodPost,
		Path:    "/api/v1/auth/registrations",
		JSON:    registrationHTTPBody(secondIntent.AuthIntentID, email, password, phoneCountryCode, phoneNationalNumber, uuid.NewString(), uuid.NewString()),
		Headers: registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: httpE2ECredentials{
			AuthFlowCookie: secondAuthFlowCookie,
			CSRFToken:      secondIntent.CSRFToken,
			Origin:         harness.origin,
		},
	})
	decodeHTTPError(t, identifierConflict, http.StatusConflict, "AUTH_IDENTIFIER_UNAVAILABLE")
	assertResponseOmits(t, identifierConflict, email, phone, password)

	preAuthCredentials := httpE2ECredentials{
		AuthFlowCookie: authFlowCookie,
		CSRFToken:      intentData.CSRFToken,
		Origin:         harness.origin,
	}
	createdUserID := uuid.New()
	userCreationProof := signUserCreationProof(t, startData.RegistrationID, createdUserID, 1)
	completionBody := map[string]any{"userId": createdUserID.String(), "userCreationProof": userCreationProof}
	completeKey := uuid.NewString()
	verificationRequired := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/complete",
		JSON:        completionBody,
		Headers:     registrationHTTPHeaders(completeKey, nil),
		Credentials: preAuthCredentials,
	})
	decodeHTTPError(t, verificationRequired, http.StatusConflict, "AUTH_VERIFICATION_REQUIRED")

	invalidMethod := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/challenges",
		JSON:        map[string]any{"method": "postal"},
		Headers:     registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: preAuthCredentials,
	})
	decodeHTTPError(t, invalidMethod, http.StatusBadRequest, "AUTH_INPUT_INVALID")

	emailChallenge := registrationHTTPIssueChallenge(t, harness, startData.RegistrationID, "email", preAuthCredentials)
	emailCode := decryptDeliveryCode(t, harness.ctx, harness.db, emailChallenge.ChallengeID)
	wrongEmailVerification := harness.do(registrationHTTPVerifyRequest(
		startData.RegistrationID,
		emailChallenge.ChallengeID,
		registrationHTTPDifferentCode(emailCode),
		uuid.NewString(),
		preAuthCredentials,
	))
	decodeHTTPError(t, wrongEmailVerification, http.StatusBadRequest, "AUTH_CHALLENGE_FAILED")
	assertResponseOmits(t, wrongEmailVerification, email, phone, password, emailCode)

	emailVerifyKey := uuid.NewString()
	emailVerificationRequest := registrationHTTPVerifyRequest(startData.RegistrationID, emailChallenge.ChallengeID, emailCode, emailVerifyKey, preAuthCredentials)
	emailVerification := harness.do(emailVerificationRequest)
	registrationHTTPDecodeVerified(t, emailVerification, emailChallenge.ChallengeID, 1)
	emailVerificationRetry := harness.do(emailVerificationRequest)
	registrationHTTPDecodeVerified(t, emailVerificationRetry, emailChallenge.ChallengeID, 1)
	registrationHTTPAssertChallengeExactlyOnce(t, harness, emailChallenge.ChallengeID, emailVerifyKey, 2)
	assertResponseOmits(t, emailVerification, email, phone, password, emailCode)

	phoneChallenge := registrationHTTPIssueChallenge(t, harness, startData.RegistrationID, "phone", preAuthCredentials)
	phoneCode := decryptDeliveryCode(t, harness.ctx, harness.db, phoneChallenge.ChallengeID)
	phoneVerifyKey := uuid.NewString()
	concurrentResponses := registrationHTTPConcurrent(func() *httpE2EResponse {
		return harness.do(registrationHTTPVerifyRequest(startData.RegistrationID, phoneChallenge.ChallengeID, phoneCode, phoneVerifyKey, preAuthCredentials))
	})
	for _, response := range concurrentResponses {
		proof := registrationHTTPDecodeVerified(t, response, phoneChallenge.ChallengeID, 2)
		if proof == "" {
			t.Fatal("API.A.300-05 did not return registrationCompletionProof after both verifications")
		}
		assertResponseOmits(t, response, email, phone, password, phoneCode)
	}
	phoneVerificationRetry := harness.do(registrationHTTPVerifyRequest(startData.RegistrationID, phoneChallenge.ChallengeID, phoneCode, phoneVerifyKey, preAuthCredentials))
	registrationHTTPDecodeVerified(t, phoneVerificationRetry, phoneChallenge.ChallengeID, 2)
	registrationHTTPAssertChallengeExactlyOnce(t, harness, phoneChallenge.ChallengeID, phoneVerifyKey, 1)

	statusByToken := harness.do(httpE2ERequest{
		Method: http.MethodGet,
		Path:   "/api/v1/auth/registrations/" + startData.RegistrationID,
		Credentials: httpE2ECredentials{
			RegistrationStatusToken: startData.RegistrationStatusToken,
		},
	})
	registrationHTTPDecodeStatus(t, statusByToken, startData.RegistrationID, "pending_verification")

	statusByAuthFlow := harness.do(httpE2ERequest{
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID,
		Credentials: httpE2ECredentials{AuthFlowCookie: authFlowCookie},
	})
	registrationHTTPDecodeStatus(t, statusByAuthFlow, startData.RegistrationID, "pending_verification")

	statusNotFound := harness.do(httpE2ERequest{
		Method: http.MethodGet,
		Path:   "/api/v1/auth/registrations/" + startData.RegistrationID,
		Credentials: httpE2ECredentials{
			RegistrationStatusToken: "rst_invalid_fixture",
		},
	})
	decodeHTTPError(t, statusNotFound, http.StatusNotFound, "AUTH_REGISTRATION_NOT_FOUND")

	invalidProofResponse := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/complete",
		JSON:        map[string]any{"userId": createdUserID.String(), "userCreationProof": userCreationProof + "tampered"},
		Headers:     registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: preAuthCredentials,
	})
	decodeHTTPError(t, invalidProofResponse, http.StatusForbidden, "AUTH_USER_CREATION_PROOF_INVALID")
	completedResponse := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/complete",
		JSON:        completionBody,
		Headers:     registrationHTTPHeaders(completeKey, nil),
		Credentials: preAuthCredentials,
	})
	var completedData registrationHTTPCompletedData
	decodeHTTPEnvelope(t, completedResponse, http.StatusOK, &completedData)
	if completedData.RegistrationID != startData.RegistrationID || completedData.Status != "completed" || completedData.CredentialDelivery != "web_jwt_refresh_cookie" || completedData.UserID != createdUserID.String() || completedData.Session.SessionID == "" || completedData.Session.ExpiresAt.IsZero() || completedData.Access.AccessToken == "" || completedData.Access.AccessTokenExpiresAt.IsZero() || completedData.Next.Path != "/drops/registration" || completedData.Next.IntentID != intentData.AuthIntentID {
		t.Fatal("API.A.300-06 completed response does not match the expected result")
	}
	replayedResponse := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/complete",
		JSON:        completionBody,
		Headers:     registrationHTTPHeaders(completeKey, nil),
		Credentials: preAuthCredentials,
	})
	var replayedData registrationHTTPCompletedData
	decodeHTTPEnvelope(t, replayedResponse, http.StatusOK, &replayedData)
	if replayedData.Session.SessionID != completedData.Session.SessionID || replayedData.Access.AccessToken != completedData.Access.AccessToken {
		t.Fatal("API.A.300-06 idempotent retry did not replay the same logical session")
	}
	sessionCookie := responseCookie(t, completedResponse, "__Host-dm_refresh")
	assertCredentialCookie(t, sessionCookie, "__Host-dm_refresh")
	if sessionCookie.MaxAge <= 0 {
		t.Fatal("remembered registration session is missing Max-Age")
	}
	clearedResponseCookie(t, completedResponse, "__Host-dm_auth")
	assertResponseOmits(t, completedResponse, email, phone, password, emailCode, phoneCode, startData.RegistrationStatusToken)
}

func registrationHTTPCreateWebIntent(t *testing.T, harness *httpE2EHarness, returnPath string) (registrationHTTPIntentData, *http.Cookie) {
	t.Helper()
	response := harness.do(httpE2ERequest{
		Method: http.MethodPost,
		Path:   "/api/v1/auth/intents",
		JSON:   map[string]any{"returnPath": returnPath, "intentType": "navigation"},
		Headers: registrationHTTPHeaders(uuid.NewString(), map[string]string{
			"X-Client-Channel": "web",
		}),
	})
	var data registrationHTTPIntentData
	decodeHTTPEnvelope(t, response, http.StatusCreated, &data)
	if data.AuthIntentID == "" || data.CSRFToken == "" || data.NextPath == "" || data.ExpiresAt.IsZero() {
		t.Fatal("API.A.300-01 success data does not match the expected result")
	}
	cookie := responseCookie(t, response, "__Host-dm_auth")
	assertCredentialCookie(t, cookie, "__Host-dm_auth")
	return data, cookie
}

func registrationHTTPBody(intentID, email, password, countryCode, nationalNumber, profileRequestID, agreementReceiptID string) map[string]any {
	return map[string]any{
		"authIntentId":       intentID,
		"email":              email,
		"password":           password,
		"phone":              map[string]any{"countryCode": countryCode, "nationalNumber": nationalNumber},
		"profileRequestId":   profileRequestID,
		"agreementReceiptId": agreementReceiptID,
		"rememberMe":         true,
	}
}

func registrationHTTPHeaders(idempotencyKey string, values map[string]string) http.Header {
	header := make(http.Header)
	header.Set("Idempotency-Key", idempotencyKey)
	for name, value := range values {
		header.Set(name, value)
	}
	return header
}

func registrationHTTPIssueChallenge(t *testing.T, harness *httpE2EHarness, registrationID, method string, credentials httpE2ECredentials) registrationHTTPChallengeData {
	t.Helper()
	response := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + registrationID + "/challenges",
		JSON:        map[string]any{"method": method},
		Headers:     registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: credentials,
	})
	var data registrationHTTPChallengeData
	decodeHTTPEnvelope(t, response, http.StatusCreated, &data)
	if data.ChallengeID == "" || data.Method != method || data.MaskedDestination == "" || data.ExpiresAt.IsZero() || data.ResendAvailableAt.IsZero() {
		t.Fatal("API.A.300-04 success data does not match the expected result")
	}
	return data
}

func registrationHTTPVerifyRequest(registrationID, challengeID, code, idempotencyKey string, credentials httpE2ECredentials) httpE2ERequest {
	return httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + registrationID + "/challenges/" + challengeID + "/verify",
		JSON:        map[string]any{"code": code},
		Headers:     registrationHTTPHeaders(idempotencyKey, nil),
		Credentials: credentials,
	}
}

func registrationHTTPDecodeVerified(t *testing.T, response *httpE2EResponse, challengeID string, wantMethods int) string {
	t.Helper()
	var data registrationHTTPVerifyData
	decodeHTTPEnvelope(t, response, http.StatusOK, &data)
	wantStatus := "pending_verification"
	if wantMethods == 2 {
		wantStatus = "verified"
	}
	if data.ChallengeID != challengeID || data.Status != "verified" || data.Registration.Status != wantStatus || len(data.Registration.VerifiedMethods) != wantMethods || len(data.Registration.RequiredVerifications) != 2 {
		t.Fatal("API.A.300-05 success data does not match the expected result")
	}
	return data.Registration.RegistrationCompletionProof
}

func registrationHTTPDifferentCode(code string) string {
	changed := []byte(code)
	if len(changed) != 6 {
		return "000000"
	}
	if changed[0] == '0' {
		changed[0] = '1'
	} else {
		changed[0] = '0'
	}
	return string(changed)
}

func registrationHTTPConcurrent(run func() *httpE2EResponse) [2]*httpE2EResponse {
	ready := make(chan struct{})
	responses := make(chan *httpE2EResponse, 2)
	for index := 0; index < 2; index++ {
		go func() {
			var response *httpE2EResponse
			defer func() { responses <- response }()
			<-ready
			response = run()
		}()
	}
	close(ready)
	return [2]*httpE2EResponse{<-responses, <-responses}
}

func registrationHTTPAssertChallengeExactlyOnce(t *testing.T, harness *httpE2EHarness, challengeID, auditKey string, wantVersion int64) {
	t.Helper()
	parsedID, err := uuid.Parse(challengeID)
	if err != nil {
		t.Fatal("read HTTP E2E Challenge terminal state")
	}
	var terminalCount int
	if err := harness.db.QueryRow(harness.ctx, `
		SELECT count(*)
		FROM auth_challenges
		WHERE challenge_id=$1 AND status='verified' AND consumed_at IS NOT NULL AND verified_at IS NOT NULL
	`, parsedID).Scan(&terminalCount); err != nil {
		t.Fatal("count HTTP E2E Challenge terminal state")
	}
	var rowVersion int64
	if err := harness.db.QueryRow(harness.ctx, `SELECT row_version FROM auth_challenges WHERE challenge_id=$1`, parsedID).Scan(&rowVersion); err != nil {
		t.Fatal("read HTTP E2E Challenge version")
	}
	var auditCount int
	if err := harness.db.QueryRow(harness.ctx, `
		SELECT count(*)
		FROM audit_outbox
		WHERE event_name='auth.registration.challenge_verified' AND idempotency_key=$1
	`, auditKey).Scan(&auditCount); err != nil {
		t.Fatal("count HTTP E2E Challenge audit records")
	}
	if terminalCount != 1 || rowVersion != wantVersion || auditCount != 1 {
		t.Fatal("HTTP E2E Challenge was not consumed and audited exactly once")
	}
}

func registrationHTTPDecodeStatus(t *testing.T, response *httpE2EResponse, registrationID, wantStatus string) {
	t.Helper()
	var data registrationHTTPStatusData
	decodeHTTPEnvelope(t, response, http.StatusOK, &data)
	if data.RegistrationID != registrationID || data.Status != wantStatus || !data.Retryable || len(data.VerifiedMethods) != 2 || data.ExpiresAt.IsZero() {
		t.Fatal("API.A.300-28 success data does not match the expected result")
	}
}
