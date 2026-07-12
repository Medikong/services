//go:build integration

package integration_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/bootstrap"
	"github.com/Medikong/services/services/auth-service/internal/application/outboxrelay"
	appregistration "github.com/Medikong/services/services/auth-service/internal/application/registration"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/access"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/inbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	registrationdomain "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	sessiondomain "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/Medikong/services/services/auth-service/internal/security"
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
		Status                string   `json:"status"`
		VerifiedMethods       []string `json:"verifiedMethods"`
		RequiredVerifications []string `json:"requiredVerifications"`
	} `json:"registration"`
}

type registrationHTTPPendingData struct {
	RegistrationID string `json:"registrationId"`
	Status         string `json:"status"`
	Retryable      bool   `json:"retryable"`
	StatusPath     string `json:"statusPath"`
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
	SessionID          string `json:"sessionId"`
	CSRFToken          string `json:"csrfToken"`
	Next               struct {
		Path     string `json:"path"`
		IntentID string `json:"intentId"`
	} `json:"next"`
}

func TestProductionHTTPRegistrationContract(t *testing.T) {
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
	decodeHTTPProblem(t, invalidRedirect, http.StatusBadRequest, "AUTH_REDIRECT_INVALID")

	intentData, authFlowCookie := registrationHTTPCreateWebIntent(t, harness, "/drops/registration")
	methodsResponse := harness.do(httpE2ERequest{
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/methods?intentId=" + intentData.AuthIntentID,
		Credentials: httpE2ECredentials{AuthFlowCookie: authFlowCookie},
	})
	var methodsData registrationHTTPMethodsData
	decodeHTTPEnvelope(t, methodsResponse, http.StatusOK, &methodsData)
	if methodsData.IntentID != intentData.AuthIntentID || len(methodsData.Methods) != 2 {
		t.Fatal("API.A.300-02 success data does not match the contract")
	}

	missingIntent := harness.do(httpE2ERequest{
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/methods?intentId=" + uuid.NewString(),
		Credentials: httpE2ECredentials{AuthFlowCookie: authFlowCookie},
	})
	decodeHTTPProblem(t, missingIntent, http.StatusNotFound, "AUTH_INTENT_NOT_FOUND")

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
	decodeHTTPProblem(t, strictJSON, http.StatusBadRequest, "AUTH_INPUT_INVALID")
	assertResponseOmits(t, strictJSON, email, phone, password)

	missingCSRF := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations",
		JSON:        registrationHTTPBody(intentData.AuthIntentID, email, password, phoneCountryCode, phoneNationalNumber, profileRequestID, agreementReceiptID),
		Headers:     registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: httpE2ECredentials{AuthFlowCookie: authFlowCookie, Origin: harness.origin},
	})
	decodeHTTPProblem(t, missingCSRF, http.StatusForbidden, "AUTH_CSRF_INVALID")
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
	decodeHTTPProblem(t, invalidOrigin, http.StatusForbidden, "AUTH_CSRF_INVALID")
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
		t.Fatal("API.A.300-03 success data does not match the contract")
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
	decodeHTTPProblem(t, identifierConflict, http.StatusConflict, "AUTH_IDENTIFIER_UNAVAILABLE")
	assertResponseOmits(t, identifierConflict, email, phone, password)

	preAuthCredentials := httpE2ECredentials{
		AuthFlowCookie: authFlowCookie,
		CSRFToken:      intentData.CSRFToken,
		Origin:         harness.origin,
	}
	completeKey := uuid.NewString()
	verificationRequired := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/complete",
		JSON:        map[string]any{},
		Headers:     registrationHTTPHeaders(completeKey, nil),
		Credentials: preAuthCredentials,
	})
	decodeHTTPProblem(t, verificationRequired, http.StatusConflict, "AUTH_VERIFICATION_REQUIRED")

	invalidMethod := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/challenges",
		JSON:        map[string]any{"method": "postal"},
		Headers:     registrationHTTPHeaders(uuid.NewString(), nil),
		Credentials: preAuthCredentials,
	})
	decodeHTTPProblem(t, invalidMethod, http.StatusBadRequest, "AUTH_INPUT_INVALID")

	emailChallenge := registrationHTTPIssueChallenge(t, harness, startData.RegistrationID, "email", preAuthCredentials)
	emailCode := decryptDeliveryCode(t, harness.ctx, harness.db, emailChallenge.ChallengeID)
	wrongEmailVerification := harness.do(registrationHTTPVerifyRequest(
		startData.RegistrationID,
		emailChallenge.ChallengeID,
		registrationHTTPDifferentCode(emailCode),
		uuid.NewString(),
		preAuthCredentials,
	))
	decodeHTTPProblem(t, wrongEmailVerification, http.StatusBadRequest, "AUTH_CHALLENGE_FAILED")
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
		registrationHTTPDecodeVerified(t, response, phoneChallenge.ChallengeID, 2)
		assertResponseOmits(t, response, email, phone, password, phoneCode)
	}
	phoneVerificationRetry := harness.do(registrationHTTPVerifyRequest(startData.RegistrationID, phoneChallenge.ChallengeID, phoneCode, phoneVerifyKey, preAuthCredentials))
	registrationHTTPDecodeVerified(t, phoneVerificationRetry, phoneChallenge.ChallengeID, 2)
	registrationHTTPAssertChallengeExactlyOnce(t, harness, phoneChallenge.ChallengeID, phoneVerifyKey, 1)

	pendingResponse := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/complete",
		JSON:        map[string]any{},
		Headers:     registrationHTTPHeaders(completeKey, nil),
		Credentials: preAuthCredentials,
	})
	var pendingData registrationHTTPPendingData
	decodeHTTPEnvelope(t, pendingResponse, http.StatusAccepted, &pendingData)
	if pendingData.RegistrationID != startData.RegistrationID || pendingData.Status != "awaiting_user_link" || !pendingData.Retryable || pendingData.StatusPath == "" || pendingResponse.header.Get("Location") != pendingData.StatusPath || pendingResponse.header.Get("Retry-After") != "2" {
		t.Fatal("API.A.300-06 accepted response does not match the contract")
	}
	assertResponseOmits(t, pendingResponse, email, phone, password, emailCode, phoneCode)

	statusByToken := harness.do(httpE2ERequest{
		Method: http.MethodGet,
		Path:   "/api/v1/auth/registrations/" + startData.RegistrationID,
		Credentials: httpE2ECredentials{
			RegistrationStatusToken: startData.RegistrationStatusToken,
		},
	})
	registrationHTTPDecodeStatus(t, statusByToken, startData.RegistrationID, "awaiting_user_link")

	statusByAuthFlow := harness.do(httpE2ERequest{
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID,
		Credentials: httpE2ECredentials{AuthFlowCookie: authFlowCookie},
	})
	registrationHTTPDecodeStatus(t, statusByAuthFlow, startData.RegistrationID, "awaiting_user_link")

	statusNotFound := harness.do(httpE2ERequest{
		Method: http.MethodGet,
		Path:   "/api/v1/auth/registrations/" + startData.RegistrationID,
		Credentials: httpE2ECredentials{
			RegistrationStatusToken: "rst_invalid_fixture",
		},
	})
	decodeHTTPProblem(t, statusNotFound, http.StatusNotFound, "AUTH_REGISTRATION_NOT_FOUND")

	registrationHTTPConsumeUserLinkTwice(t, harness, startData.RegistrationID)
	completedResponse := harness.do(httpE2ERequest{
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/registrations/" + startData.RegistrationID + "/complete",
		JSON:        map[string]any{},
		Headers:     registrationHTTPHeaders(completeKey, nil),
		Credentials: preAuthCredentials,
	})
	var completedData registrationHTTPCompletedData
	decodeHTTPEnvelope(t, completedResponse, http.StatusOK, &completedData)
	if completedData.RegistrationID != startData.RegistrationID || completedData.Status != "completed" || completedData.CredentialDelivery != "web_session" || completedData.UserID == "" || completedData.SessionID == "" || completedData.CSRFToken == "" || completedData.Next.Path != "/drops/registration" || completedData.Next.IntentID != intentData.AuthIntentID {
		t.Fatal("API.A.300-06 completed response does not match the contract")
	}
	sessionCookie := responseCookie(t, completedResponse, "__Host-dm_session")
	assertCredentialCookie(t, sessionCookie, "__Host-dm_session")
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
		t.Fatal("API.A.300-01 success data does not match the contract")
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
		t.Fatal("API.A.300-04 success data does not match the contract")
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

func registrationHTTPDecodeVerified(t *testing.T, response *httpE2EResponse, challengeID string, wantMethods int) {
	t.Helper()
	var data registrationHTTPVerifyData
	decodeHTTPEnvelope(t, response, http.StatusOK, &data)
	if data.ChallengeID != challengeID || data.Status != "verified" || data.Registration.Status != "pending_verification" || len(data.Registration.VerifiedMethods) != wantMethods || len(data.Registration.RequiredVerifications) != 2 {
		t.Fatal("API.A.300-05 success data does not match the contract")
	}
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
		t.Fatal("API.A.300-28 success data does not match the contract")
	}
}

func registrationHTTPConsumeUserLinkTwice(t *testing.T, harness *httpE2EHarness, registrationIDValue string) {
	t.Helper()
	registrationID, err := uuid.Parse(registrationIDValue)
	if err != nil {
		t.Fatal("prepare HTTP E2E User link fixture")
	}
	var causationID uuid.UUID
	if err := harness.db.QueryRow(harness.ctx, `
		SELECT verification_completed_event_id
		FROM auth_registrations
		WHERE registration_id=$1
	`, registrationID).Scan(&causationID); err != nil {
		t.Fatal("read HTTP E2E User link causation")
	}
	outboxRepository := outbox.NewPostgresRepository(harness.db)
	publisher := &outboxrelay.RecordingPublisher{}
	relay, err := outboxrelay.New(outboxRepository, publisher, outboxrelay.Config{
		WorkerID:     "http-e2e-context",
		BatchSize:    100,
		PollInterval: time.Second,
		Lease:        time.Minute,
		MaxAttempts:  3,
		BaseBackoff:  time.Second,
		MaxBackoff:   time.Minute,
	})
	if err != nil {
		t.Fatal("construct HTTP E2E outbox relay")
	}
	result, err := relay.RunOnce(harness.ctx)
	if err != nil || result.Published == 0 {
		t.Fatal("publish HTTP E2E registration events")
	}
	foundVerificationCompletion := false
	for _, published := range publisher.Events {
		if published.ID == causationID && published.Type == "Auth.RegistrationVerificationCompleted" {
			foundVerificationCompletion = true
			break
		}
	}
	if !foundVerificationCompletion {
		t.Fatal("HTTP E2E registration completion was not published")
	}

	// The concrete Context transport is the only remaining external Port. The
	// test publishes the durable outbound event above, then injects the narrow,
	// versioned inbound contract without inventing an address or credential.
	service := registrationHTTPInboxConsumer(harness)
	event := appregistration.UserLinkEvent{
		SourceEventID:  uuid.New(),
		CausationID:    causationID,
		RegistrationID: registrationID,
		UserID:         uuid.New(),
		LinkRequestID:  uuid.New(),
	}
	if err := service.ConsumeUserLinkEvent(harness.ctx, event); err != nil {
		t.Fatal("consume HTTP E2E User link event")
	}
	if err := service.ConsumeUserLinkEvent(harness.ctx, event); err != nil {
		t.Fatal("consume duplicate HTTP E2E User link event")
	}

	var inboxCount, linkCount, auditCount int
	if err := harness.db.QueryRow(harness.ctx, `
		SELECT count(*)
		FROM auth_inbox_messages
		WHERE consumer_name='context_user_link' AND source_event_id=$1 AND process_status='processed'
	`, event.SourceEventID).Scan(&inboxCount); err != nil {
		t.Fatal("count HTTP E2E User link inbox messages")
	}
	if err := harness.db.QueryRow(harness.ctx, `
		SELECT count(*)
		FROM auth_identity_links
		WHERE user_id=$1 AND link_status='active'
	`, event.UserID).Scan(&linkCount); err != nil {
		t.Fatal("count HTTP E2E User identity links")
	}
	if err := harness.db.QueryRow(harness.ctx, `
		SELECT count(*)
		FROM audit_outbox
		WHERE event_name='auth.registration.linked' AND idempotency_key=$1
	`, "user-link:"+event.SourceEventID.String()).Scan(&auditCount); err != nil {
		t.Fatal("count HTTP E2E User link audit records")
	}
	if inboxCount != 1 || linkCount != 2 || auditCount != 1 {
		t.Fatal("HTTP E2E User link inbox did not deduplicate the event")
	}
}

func registrationHTTPInboxConsumer(harness *httpE2EHarness) *appregistration.Service {
	keys := security.Keys{
		CredentialHMAC: []byte(httpE2ECredentialKey),
		ReplayKey:      []byte(httpE2EReplayKey),
		JWTKey:         []byte(httpE2EJWTKey),
		JWTIssuer:      httpE2EJWTIssuer,
	}
	intentRepository := intent.NewPostgresRepository(harness.db)
	idempotencyRepository := idempotency.NewPostgresRepository(harness.db)
	bootstrapService := bootstrap.NewService(harness.db, keys, bootstrap.Config{IntentTTL: 15 * time.Minute}, intentRepository, idempotencyRepository)
	identityRepository := identity.NewPostgresRepository(harness.db)
	outboxRepository := outbox.NewPostgresRepository(harness.db)
	accessRepository := access.NewPostgresRepository(harness.db)
	sessionService := appsession.NewService(
		harness.db,
		keys,
		appsession.Config{AccessTTL: 5 * time.Minute, RefreshTTL: time.Hour, SessionTTL: time.Hour, RememberMeSessionTTL: 24 * time.Hour, RecoveryTTL: 2 * time.Minute},
		sessiondomain.NewPostgresRepository(harness.db),
		accessRepository,
		idempotencyRepository,
		outboxRepository,
	)
	return appregistration.NewService(
		harness.db,
		keys,
		appregistration.Config{RegistrationTTL: 30 * time.Minute, StatusTokenRetention: 5 * time.Minute, ChallengeTTL: 10 * time.Minute, SessionDeliveryWindow: 10 * time.Minute},
		bootstrapService,
		registrationdomain.NewPostgresRepository(harness.db),
		challenge.NewPostgresRepository(harness.db, challenge.PostgresOptions{}),
		identityRepository,
		idempotencyRepository,
		inbox.NewPostgresRepository(harness.db),
		outboxRepository,
		accessRepository,
		intentRepository,
		sessionService,
	)
}
