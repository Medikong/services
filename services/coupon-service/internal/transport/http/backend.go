package http

import (
	"context"
	"net/url"

	"github.com/Medikong/services/packages/go-authz/principal"
	"github.com/Medikong/services/services/coupon-service/internal/transport/httpcontract"
)

type Group string

const (
	GroupCampaign   Group = "campaign"
	GroupIssuance   Group = "issuance"
	GroupRedemption Group = "redemption"
	GroupOperations Group = "operations"
)

type Call struct {
	OperationID string
	Principal   principal.Principal
	Headers     httpcontract.Headers
	Path        map[string]string
	Query       url.Values
	Body        any
}

type Result struct {
	Data              any
	AsOf              string
	Location          string
	RetryAfterSeconds int
}

// Backend is the transport-facing boundary of the four application services.
// Each method receives only decoded and validated HTTP contract values.
type Backend interface {
	Campaign(context.Context, Call) (Result, error)
	Issuance(context.Context, Call) (Result, error)
	Redemption(context.Context, Call) (Result, error)
	Operations(context.Context, Call) (Result, error)
}
