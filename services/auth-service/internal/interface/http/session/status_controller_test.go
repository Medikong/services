package session

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type statusVerifierStub struct {
	claims applicationsession.AccessClaims
	err    error
	raw    string
	calls  int
}

func (s *statusVerifierStub) VerifyAccessToken(raw string) (applicationsession.AccessClaims, error) {
	s.calls++
	s.raw = raw
	return s.claims, s.err
}

type statusReaderStub struct {
	allowed   bool
	err       error
	userID    uuid.UUID
	sessionID uuid.UUID
	deadline  time.Time
	calls     int
}

func (s *statusReaderStub) Check(ctx context.Context, userID, sessionID uuid.UUID) (bool, error) {
	s.calls++
	s.userID = userID
	s.sessionID = sessionID
	s.deadline, _ = ctx.Deadline()
	return s.allowed, s.err
}

type deadlineStatusReader struct{}

func (deadlineStatusReader) Check(ctx context.Context, _, _ uuid.UUID) (bool, error) {
	<-ctx.Done()
	return true, nil
}

type trackingBody struct {
	reader *strings.Reader
	read   bool
}

func (b *trackingBody) Read(payload []byte) (int, error) {
	b.read = true
	return b.reader.Read(payload)
}

func (*trackingBody) Close() error { return nil }

func TestStatusControllerReturnsOnlyVerifiedIdentityHeaders(t *testing.T) {
	now := time.Now()
	claims := applicationsession.AccessClaims{
		UserID: uuid.New(), SessionID: uuid.New(), TokenID: uuid.NewString(),
	}
	verifier := &statusVerifierStub{claims: claims}
	reader := &statusReaderStub{allowed: true}
	router := statusTestRouter(applicationsession.NewStatusService(verifier, reader))
	body := &trackingBody{reader: strings.NewReader("must not be consumed")}
	request := httptest.NewRequest(http.MethodPost, "/internal/session/status/orders/ord-1", body)
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("X-User-Id", uuid.NewString())
	request.Header.Set("X-Session-Id", uuid.NewString())
	request.Header.Set("X-Token-Id", uuid.NewString())
	request.Header.Set("X-User-Role", "admin")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("X-User-Id") != claims.UserID.String() ||
		response.Header().Get("X-Session-Id") != claims.SessionID.String() ||
		response.Header().Get("X-Token-Id") != claims.TokenID {
		t.Fatalf("identity headers = %#v", response.Header())
	}
	if got := identityHeaders(response.Header()); len(got) != 3 {
		t.Fatalf("identity headers = %v", got)
	}
	if verifier.raw != "access-token" || verifier.calls != 1 || reader.calls != 1 ||
		reader.userID != claims.UserID || reader.sessionID != claims.SessionID {
		t.Fatal("status request was not validated with the expected credential and identifiers")
	}
	if reader.deadline.Before(now.Add(150*time.Millisecond)) || reader.deadline.After(now.Add(350*time.Millisecond)) {
		t.Fatalf("status deadline = %s", reader.deadline)
	}
	if body.read {
		t.Fatal("status endpoint consumed the upstream request body")
	}
	if response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response body=%q cache-control=%q", response.Body.String(), response.Header().Get("Cache-Control"))
	}
}

func TestStatusControllerFailsClosedAtRequestBudget(t *testing.T) {
	claims := applicationsession.AccessClaims{
		UserID: uuid.New(), SessionID: uuid.New(), TokenID: uuid.NewString(),
	}
	service := applicationsession.NewStatusService(&statusVerifierStub{claims: claims}, deadlineStatusReader{})
	request := httptest.NewRequest(http.MethodGet, "/internal/session/status", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	started := time.Now()

	statusTestRouter(service).ServeHTTP(response, request)

	elapsed := time.Since(started)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if elapsed < 180*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %s, want request budget near 200ms", elapsed)
	}
	assertStatusProblem(t, response, "AUTH_SERVICE_UNAVAILABLE", "인증 서비스를 일시적으로 사용할 수 없습니다.")
	if got := identityHeaders(response.Header()); len(got) != 0 {
		t.Fatalf("identity headers = %v", got)
	}
}

func TestStatusControllerRejectsInvalidOrIndeterminateCredentials(t *testing.T) {
	claims := applicationsession.AccessClaims{
		UserID: uuid.New(), SessionID: uuid.New(), TokenID: uuid.NewString(),
	}
	tests := []struct {
		name          string
		authorization []string
		verifierError error
		allowed       bool
		readerError   error
		status        int
		code          string
		message       string
	}{
		{name: "missing bearer", status: http.StatusUnauthorized, code: "AUTH_SESSION_REQUIRED", message: "인증 정보를 확인한 뒤 다시 시도해주세요."},
		{name: "basic credential", authorization: []string{"Basic abc"}, status: http.StatusUnauthorized, code: "AUTH_SESSION_REQUIRED", message: "인증 정보를 확인한 뒤 다시 시도해주세요."},
		{name: "duplicate authorization", authorization: []string{"Bearer access-token", "Bearer access-token"}, allowed: true, status: http.StatusUnauthorized, code: "AUTH_SESSION_REQUIRED", message: "인증 정보를 확인한 뒤 다시 시도해주세요."},
		{name: "invalid token", authorization: []string{"Bearer secret-invalid-token"}, verifierError: errors.New("invalid signature"), status: http.StatusUnauthorized, code: "AUTH_SESSION_REQUIRED", message: "인증 정보를 확인한 뒤 다시 시도해주세요."},
		{name: "revoked session", authorization: []string{"Bearer access-token"}, status: http.StatusUnauthorized, code: "AUTH_SESSION_REVOKED", message: "Session을 사용할 수 없습니다."},
		{name: "status unavailable", authorization: []string{"Bearer access-token"}, readerError: errors.New("redis unavailable"), status: http.StatusServiceUnavailable, code: "AUTH_SERVICE_UNAVAILABLE", message: "인증 서비스를 일시적으로 사용할 수 없습니다."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := &statusVerifierStub{claims: claims, err: test.verifierError}
			reader := &statusReaderStub{allowed: test.allowed, err: test.readerError}
			request := httptest.NewRequest(http.MethodPatch, "/internal/session/status/payments/pay-1", nil)
			request.Header.Set("X-Request-Id", "30d9fa85-0a18-4263-98b6-231dca5a6fb8")
			for _, value := range test.authorization {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()

			statusTestRouter(applicationsession.NewStatusService(verifier, reader)).ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			assertStatusProblem(t, response, test.code, test.message)
			if got := identityHeaders(response.Header()); len(got) != 0 {
				t.Fatalf("identity headers = %v", got)
			}
			if strings.Contains(response.Body.String(), "access-token") || strings.Contains(response.Body.String(), "secret-invalid-token") {
				t.Fatal("response exposed a credential")
			}
		})
	}
}

func statusTestRouter(service *applicationsession.StatusService) http.Handler {
	router := chi.NewRouter()
	RegisterRoutes(router, &SessionController{}, NewStatusController(service))
	return router
}

func identityHeaders(header http.Header) []string {
	result := make([]string, 0, 4)
	for _, name := range []string{"X-User-Id", "X-Session-Id", "X-Token-Id", "X-User-Role"} {
		if header.Get(name) != "" {
			result = append(result, name)
		}
	}
	return result
}

func assertStatusProblem(t *testing.T, response *httptest.ResponseRecorder, code, message string) {
	t.Helper()
	var problem struct {
		Status    int    `json:"status"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Status != response.Code || problem.Code != code || problem.Message != message ||
		problem.RequestID != response.Header().Get("X-Request-Id") {
		t.Fatalf("problem = %#v", problem)
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("problem headers = %#v", response.Header())
	}
}
