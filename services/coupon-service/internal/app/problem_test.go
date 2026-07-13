package app

import (
	"net/http"
	"testing"

	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

func TestTransportErrorMapsStableCouponCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not found", err: oops.Code("coupon.read_model_not_found").New("missing"), wantStatus: http.StatusNotFound, wantCode: "COUPON_NOT_FOUND"},
		{name: "version", err: oops.Code("coupon.version_conflict").New("stale"), wantStatus: http.StatusConflict, wantCode: "COUPON_VERSION_CONFLICT"},
		{name: "idempotency", err: oops.Code("coupon.idempotency_conflict").New("different"), wantStatus: http.StatusConflict, wantCode: "COUPON_IDEMPOTENCY_CONFLICT"},
		{name: "operation stop", err: oops.Code("coupon.redemption_operational_stop").New("stopped"), wantStatus: http.StatusConflict, wantCode: "COUPON_OPERATION_STOPPED"},
		{name: "invalid query", err: oops.Code("coupon.read_model_query_invalid").New("bad range"), wantStatus: http.StatusBadRequest, wantCode: "COUPON_INPUT_INVALID"},
		{name: "invalid cursor", err: oops.Code("coupon.read_model_cursor_invalid").New("bad cursor"), wantStatus: http.StatusBadRequest, wantCode: "COUPON_INPUT_INVALID"},
		{name: "wrapped approval rejection", err: oops.Code("coupon.operation_approval_verification_failed").Wrap(oops.Code("approval_rejected").New("denied")), wantStatus: http.StatusForbidden, wantCode: "COUPON_FORBIDDEN"},
		{name: "dependency", err: oops.Code("coupon.database_failed").New("down"), wantStatus: http.StatusServiceUnavailable, wantCode: "COUPON_DEPENDENCY_UNAVAILABLE"},
		{name: "business rule", err: oops.Code("campaign.quantity_unavailable").New("sold out"), wantStatus: http.StatusUnprocessableEntity, wantCode: "COUPON_BUSINESS_RULE_REJECTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped := transportError(test.err)
			problem, ok := mapped.(*httpcontract.Error)
			if !ok {
				t.Fatalf("transportError() type = %T", mapped)
			}
			if problem.Status != test.wantStatus || problem.Code != test.wantCode {
				t.Fatalf("problem = (%d, %s), want (%d, %s)", problem.Status, problem.Code, test.wantStatus, test.wantCode)
			}
		})
	}
}
