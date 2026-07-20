package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	"github.com/google/uuid"
)

func TestStatusServiceValidatesTokenAndProjection(t *testing.T) {
	t.Parallel()

	claims := AccessClaims{UserID: uuid.New(), SessionID: uuid.New(), TokenID: uuid.NewString()}
	service := NewStatusService(statusVerifier{claims: claims}, statusReader{allowed: true})
	identity, err := service.Validate(context.Background(), "access-token")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if identity.UserID != claims.UserID || identity.SessionID != claims.SessionID || identity.TokenID != claims.TokenID {
		t.Fatalf("Validate() identity = %#v", identity)
	}
}

func TestStatusServiceRejectsRevokedAndUnavailableState(t *testing.T) {
	t.Parallel()

	claims := AccessClaims{UserID: uuid.New(), SessionID: uuid.New(), TokenID: uuid.NewString()}
	tests := []struct {
		name   string
		reader StatusReader
		code   string
		kind   failure.Kind
	}{
		{name: "revoked", reader: statusReader{}, code: "AUTH_SESSION_REVOKED", kind: failure.KindUnauthenticated},
		{name: "unavailable", reader: statusReader{err: errors.New("storage unavailable")}, code: "AUTH_SERVICE_UNAVAILABLE", kind: failure.KindUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewStatusService(statusVerifier{claims: claims}, test.reader)
			_, err := service.Validate(context.Background(), "access-token")
			var typed *failure.Error
			if !errors.As(err, &typed) || typed.Code != test.code || typed.Kind != test.kind {
				t.Fatalf("Validate() error = %#v", err)
			}
		})
	}
}

func TestStatusServiceRejectsMalformedVerifiedClaims(t *testing.T) {
	t.Parallel()

	tests := []AccessClaims{
		{UserID: uuid.Nil, SessionID: uuid.New(), TokenID: uuid.NewString()},
		{UserID: uuid.New(), SessionID: uuid.Nil, TokenID: uuid.NewString()},
		{UserID: uuid.New(), SessionID: uuid.New(), TokenID: "not-a-uuid"},
	}
	for _, claims := range tests {
		service := NewStatusService(statusVerifier{claims: claims}, statusReader{allowed: true})
		_, err := service.Validate(context.Background(), "access-token")
		var typed *failure.Error
		if !errors.As(err, &typed) || typed.Code != "AUTH_SESSION_REQUIRED" || typed.Kind != failure.KindUnauthenticated {
			t.Fatalf("Validate() error = %#v", err)
		}
	}
}

func TestStatusServiceFailsClosedWhenReaderReturnsAfterDeadline(t *testing.T) {
	t.Parallel()

	claims := AccessClaims{UserID: uuid.New(), SessionID: uuid.New(), TokenID: uuid.NewString()}
	service := NewStatusService(statusVerifier{claims: claims}, deadlineReturningStatusReader{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, err := service.Validate(ctx, "access-token")

	var typed *failure.Error
	if !errors.As(err, &typed) || typed.Code != "AUTH_SERVICE_UNAVAILABLE" || typed.Kind != failure.KindUnavailable {
		t.Fatalf("Validate() error = %#v", err)
	}
}

type statusVerifier struct {
	claims AccessClaims
	err    error
}

func (v statusVerifier) VerifyAccessToken(string) (AccessClaims, error) {
	return v.claims, v.err
}

type statusReader struct {
	allowed bool
	err     error
}

func (r statusReader) Check(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return r.allowed, r.err
}

type deadlineReturningStatusReader struct{}

func (deadlineReturningStatusReader) Check(ctx context.Context, _, _ uuid.UUID) (bool, error) {
	<-ctx.Done()
	return true, nil
}
