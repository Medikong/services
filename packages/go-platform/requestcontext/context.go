package requestcontext

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"

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
		r.Header.Set(RequestIDHeader, requestID)
	}
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
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", oops.
			In("request_context").
			Code("request_context.entropy_failed").
			Wrap(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
