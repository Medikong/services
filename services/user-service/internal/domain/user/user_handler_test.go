package user

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samber/oops"

	apperrors "github.com/Medikong/services/packages/go-platform/errors"
	"github.com/Medikong/services/packages/go-platform/httpapi"
)

func TestUserErrorsCarryHTTPContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "registration conflict", err: ErrRegistrationConflict, wantStatus: http.StatusConflict, wantCode: "USER_REGISTRATION_CONFLICT"},
		{name: "idempotency conflict", err: ErrIdempotencyConflict, wantStatus: http.StatusConflict, wantCode: "USER_IDEMPOTENCY_CONFLICT"},
		{name: "version conflict", err: ErrVersionConflict, wantStatus: http.StatusConflict, wantCode: "USER_VERSION_CONFLICT"},
		{name: "not found", err: ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "USER_NOT_FOUND"},
		{name: "account inactive", err: ErrAccountNotActive, wantStatus: http.StatusConflict, wantCode: "USER_ACCOUNT_NOT_ACTIVE"},
		{name: "invalid transition", err: ErrTransitionInvalid, wantStatus: http.StatusConflict, wantCode: "USER_ACCOUNT_STATUS_TRANSITION_INVALID"},
		{name: "invalid input", err: ErrInputInvalid, wantStatus: http.StatusUnprocessableEntity, wantCode: "USER_INPUT_INVALID"},
		{name: "profile policy", err: ErrProfilePolicyViolation, wantStatus: http.StatusUnprocessableEntity, wantCode: "USER_PROFILE_POLICY_VIOLATION"},
		{name: "registration proof", err: ErrRegistrationProofInvalid, wantStatus: http.StatusForbidden, wantCode: "USER_REGISTRATION_PROOF_INVALID"},
		{name: "required agreement", err: ErrRequiredAgreementInvalid, wantStatus: http.StatusUnprocessableEntity, wantCode: "USER_REQUIRED_AGREEMENT_INVALID"},
		{name: "profile media proof", err: ErrProfileMediaProofInvalid, wantStatus: http.StatusForbidden, wantCode: "USER_PROFILE_MEDIA_PROOF_INVALID"},
		{name: "internal repository error", err: oops.In("user_repository").Code("user.query_failed").New("query failed"), wantStatus: http.StatusInternalServerError, wantCode: "common.internal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			response := httptest.NewRecorder()

			httpapi.WriteError(response, request, serviceOperationError("test", test.err))

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			var body apperrors.ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("json decode: %v", err)
			}
			if body.Error.Code != test.wantCode {
				t.Fatalf("error code = %q, want %q", body.Error.Code, test.wantCode)
			}
		})
	}
}
