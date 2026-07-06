package requestcontext

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/samber/oops"
)

const (
	RequestIDHeader      = "X-Request-Id"
	ClientActionIDHeader = "X-Client-Action-Id"
)

type contextKey string

const (
	requestIDKey      contextKey = "request_id"
	clientActionIDKey contextKey = "client_action_id"
)

func Ensure(r *http.Request) (*http.Request, string, error) {
	requestID := strings.TrimSpace(r.Header.Get(RequestIDHeader))
	if requestID == "" {
		generated, err := newRequestID()
		if err != nil {
			return r, "", err
		}
		requestID = generated
	}
	r.Header.Set(RequestIDHeader, requestID)
	ctx := context.WithValue(r.Context(), requestIDKey, requestID)
	if clientActionID := strings.TrimSpace(r.Header.Get(ClientActionIDHeader)); clientActionID != "" {
		ctx = context.WithValue(ctx, clientActionIDKey, clientActionID)
	}
	return r.WithContext(ctx), requestID, nil
}

func RequestID(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey).(string); ok {
		return value
	}
	return ""
}

func ClientActionID(ctx context.Context) string {
	if value, ok := ctx.Value(clientActionIDKey).(string); ok {
		return value
	}
	return ""
}

func newRequestID() (string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return "", oops.
			In("request_context").
			Code("request_context.entropy_failed").
			Wrap(err)
	}
	return id.String(), nil
}
