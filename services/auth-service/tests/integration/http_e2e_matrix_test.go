//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type httpAPICase struct {
	ID              string
	Environment     string
	Method          string
	Path            string
	SuccessStatuses []string
	ErrorStatus     string
	ErrorCode       string
}

var httpAPICases = []httpAPICase{
	{ID: "API.A.300-01", Environment: "production", Method: "POST", Path: "/api/v1/auth/intents", SuccessStatuses: []string{"201"}, ErrorStatus: "400", ErrorCode: "AUTH_REDIRECT_INVALID"},
	{ID: "API.A.300-02", Environment: "production", Method: "GET", Path: "/api/v1/auth/methods", SuccessStatuses: []string{"200"}, ErrorStatus: "404", ErrorCode: "AUTH_INTENT_NOT_FOUND"},
	{ID: "API.A.300-03", Environment: "production", Method: "POST", Path: "/api/v1/auth/registrations", SuccessStatuses: []string{"201"}, ErrorStatus: "409", ErrorCode: "AUTH_IDENTIFIER_UNAVAILABLE"},
	{ID: "API.A.300-04", Environment: "production", Method: "POST", Path: "/api/v1/auth/registrations/{registrationId}/challenges", SuccessStatuses: []string{"201"}, ErrorStatus: "400", ErrorCode: "AUTH_INPUT_INVALID"},
	{ID: "API.A.300-05", Environment: "production", Method: "POST", Path: "/api/v1/auth/registrations/{registrationId}/challenges/{challengeId}/verify", SuccessStatuses: []string{"200"}, ErrorStatus: "400", ErrorCode: "AUTH_CHALLENGE_FAILED"},
	{ID: "API.A.300-06", Environment: "production", Method: "POST", Path: "/api/v1/auth/registrations/{registrationId}/complete", SuccessStatuses: []string{"200"}, ErrorStatus: "409", ErrorCode: "AUTH_VERIFICATION_REQUIRED"},
	{ID: "API.A.300-07", Environment: "production", Method: "POST", Path: "/api/v1/auth/signins/email", SuccessStatuses: []string{"200"}, ErrorStatus: "401", ErrorCode: "AUTH_SIGNIN_FAILED"},
	{ID: "API.A.300-08", Environment: "production", Method: "POST", Path: "/api/v1/auth/signins/phone/challenges", SuccessStatuses: []string{"202"}, ErrorStatus: "400", ErrorCode: "AUTH_INPUT_INVALID"},
	{ID: "API.A.300-09", Environment: "production", Method: "POST", Path: "/api/v1/auth/signins/phone/challenges/{challengeId}/verify", SuccessStatuses: []string{"200"}, ErrorStatus: "400", ErrorCode: "AUTH_CHALLENGE_FAILED"},
	{ID: "API.A.300-10", Environment: "production", Method: "POST", Path: "/api/v1/auth/password-resets", SuccessStatuses: []string{"202"}, ErrorStatus: "400", ErrorCode: "AUTH_INPUT_INVALID"},
	{ID: "API.A.300-11", Environment: "production", Method: "POST", Path: "/api/v1/auth/password-resets/{passwordResetId}/challenges", SuccessStatuses: []string{"202"}, ErrorStatus: "400", ErrorCode: "AUTH_INPUT_INVALID"},
	{ID: "API.A.300-12", Environment: "production", Method: "POST", Path: "/api/v1/auth/password-resets/{passwordResetId}/challenges/{challengeId}/verify", SuccessStatuses: []string{"200"}, ErrorStatus: "400", ErrorCode: "AUTH_CHALLENGE_FAILED"},
	{ID: "API.A.300-13", Environment: "production", Method: "PUT", Path: "/api/v1/auth/password-resets/{passwordResetId}/password", SuccessStatuses: []string{"204"}, ErrorStatus: "422", ErrorCode: "AUTH_PASSWORD_POLICY_NOT_MET"},
	{ID: "API.A.300-14", Environment: "production", Method: "POST", Path: "/api/v1/auth/sessions/refresh", SuccessStatuses: []string{"200"}, ErrorStatus: "401", ErrorCode: "AUTH_SESSION_REVOKED"},
	{ID: "API.A.300-15", Environment: "production", Method: "POST", Path: "/api/v1/auth/sessions/logout", SuccessStatuses: []string{"204"}, ErrorStatus: "401", ErrorCode: "AUTH_SESSION_REQUIRED"},
	{ID: "API.A.300-16", Environment: "production", Method: "GET", Path: "/api/v1/auth/context", SuccessStatuses: []string{"200"}, ErrorStatus: "401", ErrorCode: "AUTH_SESSION_REQUIRED"},
	{ID: "API.A.300-17", Environment: "production", Method: "POST", Path: "/api/v1/auth/reauthentications/email", SuccessStatuses: []string{"200"}, ErrorStatus: "401", ErrorCode: "AUTH_SIGNIN_FAILED"},
	{ID: "API.A.300-18", Environment: "production", Method: "POST", Path: "/api/v1/auth/method-links", SuccessStatuses: []string{"200", "201"}, ErrorStatus: "410", ErrorCode: "AUTH_REAUTHENTICATION_PROOF_INVALID"},
	{ID: "API.A.300-19", Environment: "production", Method: "POST", Path: "/api/v1/auth/method-links/{linkIntentId}/challenges", SuccessStatuses: []string{"201"}, ErrorStatus: "404", ErrorCode: "AUTH_IDENTITY_LINK_NOT_FOUND"},
	{ID: "API.A.300-20", Environment: "production", Method: "POST", Path: "/api/v1/auth/method-links/{linkIntentId}/complete", SuccessStatuses: []string{"200"}, ErrorStatus: "400", ErrorCode: "AUTH_CHALLENGE_FAILED"},
	{ID: "API.A.300-21", Environment: "production", Method: "POST", Path: "/api/v1/auth/phone-replacements", SuccessStatuses: []string{"201"}, ErrorStatus: "410", ErrorCode: "AUTH_REAUTHENTICATION_PROOF_INVALID"},
	{ID: "API.A.300-22", Environment: "production", Method: "POST", Path: "/api/v1/auth/phone-replacements/{replacementId}/challenges", SuccessStatuses: []string{"201"}, ErrorStatus: "404", ErrorCode: "AUTH_IDENTITY_LINK_NOT_FOUND"},
	{ID: "API.A.300-23", Environment: "production", Method: "POST", Path: "/api/v1/auth/phone-replacements/{replacementId}/complete", SuccessStatuses: []string{"200"}, ErrorStatus: "410", ErrorCode: "AUTH_SESSION_DELIVERY_EXPIRED"},
	{ID: "API.A.300-24", Environment: "production", Method: "GET", Path: "/api/v1/operator/auth/users/{userId}", SuccessStatuses: []string{"200"}, ErrorStatus: "403", ErrorCode: "AUTH_FORBIDDEN"},
	{ID: "API.A.300-25", Environment: "production", Method: "GET", Path: "/api/v1/operator/auth/policies", SuccessStatuses: []string{"200"}, ErrorStatus: "403", ErrorCode: "AUTH_FORBIDDEN"},
	{ID: "API.A.300-26", Environment: "production", Method: "PATCH", Path: "/api/v1/operator/auth/policies/{policyName}", SuccessStatuses: []string{"200"}, ErrorStatus: "412", ErrorCode: "AUTH_POLICY_PRECONDITION_FAILED"},
	{ID: "API.A.300-27", Environment: "production", Method: "POST", Path: "/api/v1/operator/auth/manual-actions", SuccessStatuses: []string{"200"}, ErrorStatus: "409", ErrorCode: "AUTH_APPROVAL_REQUIRED"},
	{ID: "API.A.300-28", Environment: "production", Method: "GET", Path: "/api/v1/auth/registrations/{registrationId}", SuccessStatuses: []string{"200"}, ErrorStatus: "404", ErrorCode: "AUTH_REGISTRATION_NOT_FOUND"},
	{ID: "API.A.300-29", Environment: "production", Method: "POST", Path: "/api/v1/auth/intents/{intentId}/action-resume", SuccessStatuses: []string{"200"}, ErrorStatus: "410", ErrorCode: "AUTH_INTENT_EXPIRED"},
	{ID: "API.A.300-30", Environment: "development", Method: "GET", Path: "/api/v1/dev/auth/verification-messages/{challengeId}", SuccessStatuses: []string{"200"}, ErrorStatus: "404", ErrorCode: "AUTH_VIRTUAL_MESSAGE_NOT_FOUND"},
	{ID: "API.A.300-31", Environment: "production", Method: "PUT", Path: "/api/v1/operator/auth/users/{userId}/account-status", SuccessStatuses: []string{"200"}, ErrorStatus: "403", ErrorCode: "AUTH_FORBIDDEN"},
	{ID: "API.A.300-34", Environment: "development", Method: "POST", Path: "/api/v1/dev/auth/test-tokens/bulk", SuccessStatuses: []string{"201"}, ErrorStatus: "404", ErrorCode: "AUTH_DEVELOPMENT_ENDPOINT_NOT_FOUND"},
}

type openAPIDocument struct {
	Paths map[string]map[string]openAPIOperation `yaml:"paths"`
}

type openAPIOperation struct {
	APIID      string                    `yaml:"x-api-id"`
	ErrorCodes map[string]int            `yaml:"x-error-codes"`
	Responses  map[string]map[string]any `yaml:"responses"`
}

func TestHTTPAPIMatrixMatchesBundledOpenAPI(t *testing.T) {
	production := readOpenAPIOperations(t, "openapi.bundle.yaml")
	development := readOpenAPIOperations(t, "dev.openapi.bundle.yaml")
	if len(production) != 30 {
		t.Fatalf("production OpenAPI operation count = %d, want 30", len(production))
	}
	if len(development) != 2 {
		t.Fatalf("development OpenAPI operation count = %d, want 2", len(development))
	}
	seen := make(map[string]struct{}, len(httpAPICases))
	for _, testCase := range httpAPICases {
		t.Run(testCase.ID, func(t *testing.T) {
			if _, duplicate := seen[testCase.ID]; duplicate {
				t.Fatalf("duplicate API matrix entry %s", testCase.ID)
			}
			seen[testCase.ID] = struct{}{}
			operations := production
			if testCase.Environment == "development" {
				operations = development
			}
			operation, ok := operations[testCase.Method+" "+testCase.Path]
			if !ok {
				t.Fatalf("OpenAPI operation is missing for %s", testCase.ID)
			}
			if operation.APIID != testCase.ID {
				t.Fatalf("OpenAPI x-api-id = %q, want %q", operation.APIID, testCase.ID)
			}
			for _, status := range append(append([]string(nil), testCase.SuccessStatuses...), testCase.ErrorStatus) {
				if _, ok := operation.Responses[status]; !ok {
					t.Errorf("OpenAPI response %s is missing for %s", status, testCase.ID)
				}
			}
			wantErrorStatus, err := strconv.Atoi(testCase.ErrorStatus)
			if err != nil {
				t.Fatalf("invalid matrix error status %q", testCase.ErrorStatus)
			}
			if got, ok := operation.ErrorCodes[testCase.ErrorCode]; !ok || got != wantErrorStatus {
				t.Errorf("OpenAPI error %s = %d, want %d", testCase.ErrorCode, got, wantErrorStatus)
			}
		})
	}
	if len(seen) != 32 {
		t.Fatalf("API matrix entry count = %d, want 32", len(seen))
	}
	for key, operation := range production {
		if strings.Contains(key, "/api/v1/dev/") || operation.APIID == "API.A.300-30" || operation.APIID == "API.A.300-34" {
			t.Fatalf("production OpenAPI includes a development operation")
		}
	}
}

func readOpenAPIOperations(t *testing.T, name string) map[string]openAPIOperation {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate HTTP E2E matrix source")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "api", "openapi", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bundled OpenAPI %s: %v", name, err)
	}
	var document openAPIDocument
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse bundled OpenAPI %s: %v", name, err)
	}
	operations := make(map[string]openAPIOperation)
	for path, pathItem := range document.Paths {
		for method, operation := range pathItem {
			method = strings.ToUpper(method)
			if method != "GET" && method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
				continue
			}
			operations[method+" "+path] = operation
		}
	}
	return operations
}
