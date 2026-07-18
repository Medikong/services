package session

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const extAuthzTimeout = 200 * time.Millisecond

const (
	extAuthzUserIDHeader    = "X-User-Id"
	extAuthzSessionIDHeader = "X-Session-Id"
	extAuthzTokenIDHeader   = "X-Token-Id"
)

type StatusChecker interface {
	Check(context.Context, StatusCheck) StatusState
}

type ExtAuthz struct {
	verifier security.Keys
	statuses StatusChecker
}

func NewExtAuthz(verifier security.Keys, statuses StatusChecker) *ExtAuthz {
	return &ExtAuthz{verifier: verifier, statuses: statuses}
}

func RegisterExtAuthzRoutes(router chi.Router, controller *ExtAuthz) {
	router.Handle("/internal/ext-authz", http.HandlerFunc(controller.Check))
	router.Handle("/internal/ext-authz/*", http.HandlerFunc(controller.Check))
}

func (c *ExtAuthz) Check(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), extAuthzTimeout)
	defer cancel()

	raw, ok := extAuthzBearer(r)
	if !ok || c == nil {
		writeExtAuthzUnauthorized(w, r)
		return
	}
	claims, err := c.verifier.VerifyAccessToken(raw)
	if err != nil {
		writeExtAuthzUnauthorized(w, r)
		return
	}
	userID, userErr := uuid.Parse(claims.Subject)
	sessionID, sessionErr := uuid.Parse(claims.SessionID)
	tokenID, tokenErr := uuid.Parse(claims.TokenID)
	if userErr != nil || sessionErr != nil || tokenErr != nil {
		writeExtAuthzUnauthorized(w, r)
		return
	}
	if c.statuses == nil {
		writeExtAuthzUnavailable(w, r)
		return
	}
	state := c.statuses.Check(ctx, StatusCheck{UserID: userID, SessionID: sessionID, TokenID: tokenID})
	if ctx.Err() != nil {
		writeExtAuthzUnavailable(w, r)
		return
	}
	switch state {
	case StatusActive:
		w.Header().Set(extAuthzUserIDHeader, userID.String())
		w.Header().Set(extAuthzSessionIDHeader, sessionID.String())
		w.Header().Set(extAuthzTokenIDHeader, tokenID.String())
		w.WriteHeader(http.StatusOK)
	case StatusExpired, StatusRevoked:
		writeExtAuthzUnauthorized(w, r)
	case StatusUnavailable:
		writeExtAuthzUnavailable(w, r)
	default:
		writeExtAuthzUnavailable(w, r)
	}
}

func extAuthzBearer(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
