//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/packages/go-contracts/headers"
	platformdb "github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/packages/go-platform/operational"
	"github.com/Medikong/services/services/user-service/internal/domain/user"
	"github.com/Medikong/services/services/user-service/internal/platform/observability"
	"github.com/Medikong/services/services/user-service/internal/security"
	userhttp "github.com/Medikong/services/services/user-service/internal/transport/http"
)

type testRuntime struct {
	client       *http.Client
	server       *httptest.Server
	databaseURL  string
	pool         *pgxpool.Pool
	authSigner   security.Signer
	mediaSigner  security.Signer
	userVerifier security.Verifier
	health       *operational.Handler
}

func TestUserHTTPPostgresContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	runtime := newTestRuntime(t, ctx)

	registrationID := "reg_01JUSERINTEGRATION"
	registrationProof, err := runtime.authSigner.Sign(security.ProofClaims{
		Audience: "user-service", Purpose: "create_user", RegistrationID: registrationID,
		EmailVerified: true, PhoneVerified: true, Nonce: uuid.NewString(),
	}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	acceptedAt := time.Now().UTC().Truncate(time.Second)
	createBody := map[string]any{
		"registrationId":              registrationID,
		"registrationCompletionProof": registrationProof,
		"profile":                     map[string]any{"privateName": "홍길동", "nickname": "dropfan", "introduction": nil},
		"requiredAgreements": []map[string]any{{
			"agreementCode": "TERMS_OF_SERVICE", "agreementVersion": "2026-07-01", "acceptedAt": acceptedAt,
		}},
	}
	createHeaders := map[string]string{"Origin": "http://client.test", headers.IdempotencyKey: "create-request-1"}
	status, body := doJSON(t, runtime.client, http.MethodPost, runtime.server.URL+"/api/v1/users", createBody, createHeaders)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", status, redactedBody(body))
	}
	var created struct {
		Data struct {
			UserID            uuid.UUID `json:"userId"`
			UserVersion       int64     `json:"userVersion"`
			CreatedAt         time.Time `json:"createdAt"`
			UserCreationProof string    `json:"userCreationProof"`
		} `json:"data"`
	}
	decodeBody(t, body, &created)
	if created.Data.UserID == uuid.Nil || created.Data.UserVersion != 1 || created.Data.UserCreationProof == "" {
		t.Fatalf("unexpected create response: user id present=%v version=%d proof present=%v", created.Data.UserID != uuid.Nil, created.Data.UserVersion, created.Data.UserCreationProof != "")
	}
	claims, err := runtime.userVerifier.Verify(created.Data.UserCreationProof, "auth-service", "complete_registration")
	if err != nil || claims.UserID != created.Data.UserID.String() || claims.RegistrationID != registrationID {
		t.Fatalf("user creation proof: user match=%v registration match=%v error=%v", claims.UserID == created.Data.UserID.String(), claims.RegistrationID == registrationID, err)
	}

	status, replayBody := doJSON(t, runtime.client, http.MethodPost, runtime.server.URL+"/api/v1/users", createBody, map[string]string{
		"Origin": "http://client.test", headers.IdempotencyKey: "another-observability-key",
	})
	if status != http.StatusOK {
		t.Fatalf("create replay status = %d body=%s", status, redactedBody(replayBody))
	}
	var replayed struct {
		Data struct {
			UserID      uuid.UUID `json:"userId"`
			UserVersion int64     `json:"userVersion"`
		} `json:"data"`
	}
	decodeBody(t, replayBody, &replayed)
	if replayed.Data.UserID != created.Data.UserID || replayed.Data.UserVersion != 1 {
		t.Fatalf("create replay changed result: user match=%v version=%d", replayed.Data.UserID == created.Data.UserID, replayed.Data.UserVersion)
	}

	conflictBody := cloneMap(createBody)
	conflictBody["profile"] = map[string]any{"privateName": "홍길동", "nickname": "other", "introduction": nil}
	assertAPIError(t, runtime, http.MethodPost, "/api/v1/users", conflictBody, createHeaders, http.StatusConflict, "USER_REGISTRATION_CONFLICT")

	userPrincipal := encodePrincipal(t, principal.Principal{Type: principal.TypeUser, UserID: created.Data.UserID.String(), Roles: []string{"customer"}})
	profileHeaders := map[string]string{headers.Principal: userPrincipal}
	status, body = doJSON(t, runtime.client, http.MethodGet, runtime.server.URL+"/api/v1/users/me/profile", nil, profileHeaders)
	if status != http.StatusOK {
		t.Fatalf("profile status = %d body=%s", status, redactedBody(body))
	}
	assertProfile(t, body, "dropfan", 1, "active")

	patchBody := map[string]any{"expectedUserVersion": 1, "nickname": "new-dropfan", "introduction": nil}
	patchHeaders := map[string]string{headers.Principal: userPrincipal, headers.IdempotencyKey: "profile-change-1", "Origin": "http://client.test"}
	status, body = doJSON(t, runtime.client, http.MethodPatch, runtime.server.URL+"/api/v1/users/me/profile", patchBody, patchHeaders)
	if status != http.StatusOK {
		t.Fatalf("profile update status = %d body=%s", status, redactedBody(body))
	}
	assertMutationVersion(t, body, 2)
	status, body = doJSON(t, runtime.client, http.MethodPatch, runtime.server.URL+"/api/v1/users/me/profile", patchBody, patchHeaders)
	if status != http.StatusOK {
		t.Fatalf("profile replay status = %d body=%s", status, redactedBody(body))
	}
	assertMutationVersion(t, body, 2)

	concurrentStatuses := concurrentProfileUpdates(t, runtime, userPrincipal)
	sort.Ints(concurrentStatuses)
	if len(concurrentStatuses) != 2 || concurrentStatuses[0] != http.StatusOK || concurrentStatuses[1] != http.StatusConflict {
		t.Fatalf("concurrent profile statuses = %v, want [200 409]", concurrentStatuses)
	}
	status, body = doJSON(t, runtime.client, http.MethodGet, runtime.server.URL+"/api/v1/users/me/profile", nil, profileHeaders)
	if status != http.StatusOK {
		t.Fatalf("profile after concurrency status = %d body=%s", status, redactedBody(body))
	}
	assertProfileVersion(t, body, 3)

	invalidProof := "invalid-sensitive-proof-value"
	invalidImageBody := map[string]any{"mediaAssetId": "asset_profile_1", "mediaAssetProof": invalidProof, "expectedUserVersion": 3}
	invalidHeaders := map[string]string{headers.Principal: userPrincipal, headers.IdempotencyKey: "image-invalid", "Origin": "http://client.test"}
	status, body = doJSON(t, runtime.client, http.MethodPut, runtime.server.URL+"/api/v1/users/me/profile-image", invalidImageBody, invalidHeaders)
	if status != http.StatusForbidden || !responseHasCode(body, "USER_PROFILE_MEDIA_PROOF_INVALID") {
		t.Fatalf("invalid media proof status = %d body=%s", status, redactedBody(body))
	}
	if strings.Contains(string(body), invalidProof) || strings.Contains(string(body), runtime.databaseURL) {
		t.Fatalf("sensitive value appeared in error response: %s", body)
	}

	mediaProof, err := runtime.mediaSigner.Sign(security.ProofClaims{
		Audience: "user-service", Purpose: "user_profile", UserID: created.Data.UserID.String(),
		MediaAssetID: "asset_profile_1", ScanCompleted: true, Nonce: uuid.NewString(),
	}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	imageBody := map[string]any{"mediaAssetId": "asset_profile_1", "mediaAssetProof": mediaProof, "expectedUserVersion": 3}
	imageHeaders := map[string]string{headers.Principal: userPrincipal, headers.IdempotencyKey: "image-change-1", "Origin": "http://client.test"}
	status, body = doJSON(t, runtime.client, http.MethodPut, runtime.server.URL+"/api/v1/users/me/profile-image", imageBody, imageHeaders)
	if status != http.StatusOK {
		t.Fatalf("image update status = %d body=%s", status, redactedBody(body))
	}
	assertMutationVersion(t, body, 4)
	status, body = doJSON(t, runtime.client, http.MethodPut, runtime.server.URL+"/api/v1/users/me/profile-image", imageBody, imageHeaders)
	if status != http.StatusOK {
		t.Fatalf("image replay status = %d body=%s", status, redactedBody(body))
	}
	assertMutationVersion(t, body, 4)

	operatorID := uuid.NewString()
	operatorPrincipal := encodePrincipal(t, principal.Principal{
		Type: principal.TypeUser, UserID: operatorID, SessionID: uuid.NewString(), AuthLevel: "strong",
		Roles: []string{"user.account_status.change", "user.account_status.read"},
	})
	statusBody := map[string]any{"targetStatus": "restricted", "reasonCode": "POLICY_VIOLATION", "expectedUserVersion": 4}
	operatorHeaders := map[string]string{
		headers.Principal: operatorPrincipal, headers.IdempotencyKey: "status-change-1",
		"Origin": "http://client.test", "X-Csrf-Verified": "true",
	}
	statusPath := fmt.Sprintf("/api/v1/operator/users/%s/status-transitions", created.Data.UserID)
	status, body = doJSON(t, runtime.client, http.MethodPost, runtime.server.URL+statusPath, statusBody, operatorHeaders)
	if status != http.StatusOK {
		t.Fatalf("status change status = %d body=%s", status, redactedBody(body))
	}
	var changed struct {
		Data struct {
			StatusChangeID        uuid.UUID `json:"statusChangeId"`
			UserVersion           int64     `json:"userVersion"`
			AccountStatus         string    `json:"accountStatus"`
			UserStatusChangeProof string    `json:"userStatusChangeProof"`
		} `json:"data"`
	}
	decodeBody(t, body, &changed)
	if changed.Data.UserVersion != 5 || changed.Data.AccountStatus != "restricted" || changed.Data.StatusChangeID == uuid.Nil {
		t.Fatalf("unexpected status change response: status change id present=%v version=%d status=%q proof present=%v", changed.Data.StatusChangeID != uuid.Nil, changed.Data.UserVersion, changed.Data.AccountStatus, changed.Data.UserStatusChangeProof != "")
	}
	statusClaims, err := runtime.userVerifier.Verify(changed.Data.UserStatusChangeProof, "auth-service", "apply_user_status")
	if err != nil || statusClaims.UserVersion != 5 || statusClaims.StatusChangeID != changed.Data.StatusChangeID.String() || statusClaims.ChangedAt == 0 {
		t.Fatalf("status proof: version=%d status change match=%v changed time present=%v error=%v", statusClaims.UserVersion, statusClaims.StatusChangeID == changed.Data.StatusChangeID.String(), statusClaims.ChangedAt != 0, err)
	}
	status, replayBody = doJSON(t, runtime.client, http.MethodPost, runtime.server.URL+statusPath, statusBody, operatorHeaders)
	if status != http.StatusOK {
		t.Fatalf("status replay status = %d body=%s", status, redactedBody(replayBody))
	}
	var replayedStatus struct {
		Data struct {
			StatusChangeID uuid.UUID `json:"statusChangeId"`
			UserVersion    int64     `json:"userVersion"`
		} `json:"data"`
	}
	decodeBody(t, replayBody, &replayedStatus)
	if replayedStatus.Data.StatusChangeID != changed.Data.StatusChangeID || replayedStatus.Data.UserVersion != 5 {
		t.Fatalf("status replay changed result: status change match=%v version=%d", replayedStatus.Data.StatusChangeID == changed.Data.StatusChangeID, replayedStatus.Data.UserVersion)
	}
	var historyCount int
	if err := queryRow(runtime, ctx, "SELECT count(*) FROM user_status_history WHERE user_id = $1", created.Data.UserID).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 1 {
		t.Fatalf("status history count = %d, want 1", historyCount)
	}

	status, body = doJSON(t, runtime.client, http.MethodGet, runtime.server.URL+fmt.Sprintf("/api/v1/operator/users/%s/status", created.Data.UserID), nil, map[string]string{headers.Principal: operatorPrincipal})
	if status != http.StatusOK {
		t.Fatalf("operator status query = %d body=%s", status, redactedBody(body))
	}
	assertMutationVersion(t, body, 5)

	inactivePatch := map[string]any{"expectedUserVersion": 5, "nickname": "blocked-change"}
	inactiveHeaders := map[string]string{headers.Principal: userPrincipal, headers.IdempotencyKey: "profile-while-restricted", "Origin": "http://client.test"}
	assertAPIError(t, runtime, http.MethodPatch, "/api/v1/users/me/profile", inactivePatch, inactiveHeaders, http.StatusConflict, "USER_ACCOUNT_NOT_ACTIVE")

	status, body = doJSON(t, runtime.client, http.MethodPost, runtime.server.URL+"/internal/dev/proofs/registration", map[string]any{"registrationId": "reg-dev"}, nil)
	if status != http.StatusNotFound {
		t.Fatalf("disabled development route status = %d body=%s", status, redactedBody(body))
	}

	ready := httptest.NewRecorder()
	runtime.health.Readyz(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readiness before drain = %d body=%s", ready.Code, redactedBody(ready.Body.Bytes()))
	}
	runtime.health.BeginDrain()
	notReady := httptest.NewRecorder()
	runtime.health.Readyz(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness during drain = %d body=%s", notReady.Code, redactedBody(notReady.Body.Bytes()))
	}
	status, body = doJSON(t, runtime.client, http.MethodGet, runtime.server.URL+"/api/v1/users/me/profile", nil, profileHeaders)
	if status != http.StatusServiceUnavailable || !responseHasCode(body, "common.draining") {
		t.Fatalf("request during drain = %d body=%s", status, redactedBody(body))
	}
}

func newTestRuntime(t *testing.T, ctx context.Context) *testRuntime {
	t.Helper()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("users"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("postgres run: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}
	db, err := platformdb.OpenPostgres(ctx, platformdb.DefaultPostgresConfig(databaseURL))
	if err != nil {
		t.Fatalf("postgres open: %s", redactText(err.Error(), databaseURL))
	}
	t.Cleanup(db.Close)
	if err := user.Migrate(ctx, db); err != nil {
		t.Fatalf("user migrate: %s", redactText(err.Error(), databaseURL))
	}
	if err := user.CheckSchema(ctx, db); err != nil {
		t.Fatalf("schema check: %s", redactText(err.Error(), databaseURL))
	}

	authPublic, authPrivate := testKeyPair("integration-auth")
	mediaPublic, mediaPrivate := testKeyPair("integration-media")
	userPublic, userPrivate := testKeyPair("integration-user")
	authSigner, err := security.NewSigner("auth-service", "auth-test-1", authPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	authVerifier, err := security.NewVerifier("auth-service", "auth-test-1", authPublic, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	mediaSigner, err := security.NewSigner("media-service", "media-test-1", mediaPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	mediaVerifier, err := security.NewVerifier("media-service", "media-test-1", mediaPublic, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	userSigner, err := security.NewSigner("user-service", "user-test-1", userPrivate, nil)
	if err != nil {
		t.Fatal(err)
	}
	userVerifier, err := security.NewVerifier("user-service", "user-test-1", userPublic, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	encryptionKey := sha256.Sum256([]byte("integration-private-name-key"))
	sealer, err := security.NewSealer(base64.RawStdEncoding.EncodeToString(encryptionKey[:]), nil)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := user.NewUserRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := user.NewUserService(repository, sealer, authVerifier, mediaVerifier, userSigner, user.UserServiceConfig{
		RequiredAgreements: map[string]string{"TERMS_OF_SERVICE": "2026-07-01"},
		IdempotencyTTL:     24 * time.Hour, ProofTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := observability.NewMetrics("user-service-integration", "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = metrics.Shutdown(context.Background()) })
	health := operational.NewHandler(operational.Config{
		Service: "user-service-integration", ReadinessTimeout: time.Second,
		Checks: map[string]operational.Check{"database": func(ctx context.Context) error {
			if err := db.Ping(ctx); err != nil {
				return err
			}
			return user.CheckSchema(ctx, db)
		}}, Metrics: metrics.Handler(), SetReady: metrics.SetReady,
	})
	userHandler, err := user.NewUserHandler(service, metrics, user.UserHandlerConfig{
		AllowedOrigins: map[string]struct{}{"http://client.test": {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	router, err := userhttp.NewRouter(userhttp.RouterConfig{
		ServiceName: "user-service-integration", RequestTimeout: 5 * time.Second,
	}, userHandler, nil, health)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &testRuntime{
		client: server.Client(), server: server, databaseURL: databaseURL, pool: db,
		authSigner: authSigner, mediaSigner: mediaSigner, userVerifier: userVerifier, health: health,
	}
}

func concurrentProfileUpdates(t *testing.T, runtime *testRuntime, principalHeader string) []int {
	t.Helper()
	start := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for index, introduction := range []string{"first concurrent value", "second concurrent value"} {
		wg.Add(1)
		go func(index int, introduction string) {
			defer wg.Done()
			<-start
			body, _ := json.Marshal(map[string]any{"expectedUserVersion": 2, "introduction": introduction})
			request, _ := http.NewRequest(http.MethodPatch, runtime.server.URL+"/api/v1/users/me/profile", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "http://client.test")
			request.Header.Set(headers.Principal, principalHeader)
			request.Header.Set(headers.IdempotencyKey, fmt.Sprintf("concurrent-profile-%d", index))
			response, err := runtime.client.Do(request)
			if err != nil {
				results <- 0
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			results <- response.StatusCode
		}(index, introduction)
	}
	close(start)
	wg.Wait()
	close(results)
	statuses := make([]int, 0, 2)
	for status := range results {
		statuses = append(statuses, status)
	}
	return statuses
}

func doJSON(t *testing.T, client *http.Client, method, endpoint string, body any, requestHeaders map[string]string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range requestHeaders {
		request.Header.Set(key, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

func assertAPIError(t *testing.T, runtime *testRuntime, method, path string, requestBody any, requestHeaders map[string]string, wantStatus int, wantCode string) {
	t.Helper()
	status, body := doJSON(t, runtime.client, method, runtime.server.URL+path, requestBody, requestHeaders)
	if status != wantStatus || !responseHasCode(body, wantCode) {
		t.Fatalf("%s %s status = %d body=%s, want %d %s", method, path, status, redactedBody(body), wantStatus, wantCode)
	}
}

func responseHasCode(body []byte, code string) bool {
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &response) == nil && response.Error.Code == code
}

func decodeBody(t *testing.T, body []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(body, destination); err != nil {
		t.Fatalf("decode body: %v body=%s", err, redactedBody(body))
	}
}

func assertProfile(t *testing.T, body []byte, nickname string, version int64, status string) {
	t.Helper()
	var response struct {
		Data struct {
			Nickname      string `json:"nickname"`
			UserVersion   int64  `json:"userVersion"`
			AccountStatus string `json:"accountStatus"`
		} `json:"data"`
	}
	decodeBody(t, body, &response)
	if response.Data.Nickname != nickname || response.Data.UserVersion != version || response.Data.AccountStatus != status {
		t.Fatalf("unexpected profile: nickname match=%v version=%d status=%q", response.Data.Nickname == nickname, response.Data.UserVersion, response.Data.AccountStatus)
	}
}

func assertProfileVersion(t *testing.T, body []byte, version int64) {
	t.Helper()
	var response struct {
		Data struct {
			UserVersion int64 `json:"userVersion"`
		} `json:"data"`
	}
	decodeBody(t, body, &response)
	if response.Data.UserVersion != version {
		t.Fatalf("user version = %d, want %d", response.Data.UserVersion, version)
	}
}

func assertMutationVersion(t *testing.T, body []byte, version int64) {
	t.Helper()
	assertProfileVersion(t, body, version)
}

func encodePrincipal(t *testing.T, value principal.Principal) string {
	t.Helper()
	header, err := principal.EncodeHeader(value)
	if err != nil {
		t.Fatal(err)
	}
	return header
}

func testKeyPair(label string) (string, string) {
	seed := sha256.Sum256([]byte(label))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return base64.RawStdEncoding.EncodeToString(publicKey), base64.RawStdEncoding.EncodeToString(privateKey)
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func redactedBody(body []byte) string {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return "[non-json response omitted]"
	}
	redactJSON(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return "[response omitted]"
	}
	return string(encoded)
}

func redactJSON(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(key)
			if strings.Contains(normalized, "proof") || strings.Contains(normalized, "token") || strings.Contains(normalized, "credential") || strings.Contains(normalized, "cookie") || normalized == "privatename" || normalized == "nickname" || normalized == "introduction" || normalized == "userid" || normalized == "registrationid" {
				typed[key] = "[REDACTED]"
				continue
			}
			redactJSON(child)
		}
	case []any:
		for _, child := range typed {
			redactJSON(child)
		}
	}
}

func redactText(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}

func queryRow(runtime *testRuntime, ctx context.Context, query string, args ...any) interface{ Scan(...any) error } {
	return runtime.pool.QueryRow(ctx, query, args...)
}
