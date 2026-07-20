package userauthstate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	domainuserauthstate "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/google/uuid"
)

func TestApplyCommitsStateAndSessionRevocationBeforeProjection(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()
	transactor := &recordingTransactor{
		state: domainuserauthstate.State{
			UserID: userID, Status: domainuserauthstate.StatusActive,
			UserVersion: 1, StatusChangeID: "change-1", EffectiveAt: now.Add(-time.Hour), RowVersion: 1,
		},
	}
	projection := &recordingProjection{committed: &transactor.committed}
	service := NewService(
		transactor,
		staticProofVerifier{proof: StatusProof{StatusChangeID: "change-2", UserID: userID.String(), AccountStatus: "restricted", UserVersion: 2, ChangedAt: now.Unix()}},
		allowDecision{},
		Config{StrongAuthTTL: 5 * time.Minute},
		fixedClock{now: now},
		projection,
	)

	result, err := service.Apply(context.Background(), ApplyInput{
		Principal: domainsession.Principal{
			Authenticated: true, UserID: uuid.New(), SessionID: uuid.New(), Method: "email_password", AuthenticatedAt: now.Add(-time.Minute),
		},
		PathUserID: userID.String(), UserStatusChangeProof: string(make([]byte, 32)), AuthorizationDecision: "allow",
	})
	if err != nil {
		t.Fatalf("apply user auth state: %v", err)
	}
	if !result.Applied || result.AccountStatus != domainuserauthstate.StatusRestricted || result.UserVersion != 2 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !transactor.revoked || !transactor.committed || !projection.called {
		t.Fatalf("atomic state update flags: revoked=%v committed=%v projected=%v", transactor.revoked, transactor.committed, projection.called)
	}
}

func TestApplyPreservesVersionConflictFailure(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()
	transactor := &recordingTransactor{state: domainuserauthstate.State{
		UserID: userID, Status: domainuserauthstate.StatusActive,
		UserVersion: 2, StatusChangeID: "change-current", EffectiveAt: now, RowVersion: 2,
	}}
	service := NewService(
		transactor,
		staticProofVerifier{proof: StatusProof{StatusChangeID: "change-other", UserID: userID.String(), AccountStatus: "restricted", UserVersion: 2, ChangedAt: now.Unix()}},
		allowDecision{}, Config{}, fixedClock{now: now},
	)
	_, err := service.Apply(context.Background(), ApplyInput{
		Principal:  domainsession.Principal{Authenticated: true, UserID: uuid.New(), SessionID: uuid.New(), Method: "passkey", AuthenticatedAt: now},
		PathUserID: userID.String(), UserStatusChangeProof: string(make([]byte, 32)), AuthorizationDecision: "allow",
	})
	var typed *failure.Error
	if !errors.As(err, &typed) || typed.Code != "AUTH_RESOURCE_PRECONDITION_FAILED" || typed.Kind != failure.KindConflict {
		t.Fatalf("conflict error = %#v", err)
	}
	if transactor.committed {
		t.Fatal("version conflict committed the transaction")
	}
}

func TestApplyRollsBackWhenSessionRevocationFails(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	userID := uuid.New()
	transactor := &recordingTransactor{
		state: domainuserauthstate.State{
			UserID: userID, Status: domainuserauthstate.StatusActive,
			UserVersion: 1, StatusChangeID: "change-1", EffectiveAt: now.Add(-time.Hour), RowVersion: 1,
		},
		revokeErr: errors.New("session revocation failed"),
	}
	projection := &recordingProjection{committed: &transactor.committed}
	service := NewService(
		transactor,
		staticProofVerifier{proof: StatusProof{StatusChangeID: "change-2", UserID: userID.String(), AccountStatus: "deactivated", UserVersion: 2, ChangedAt: now.Unix()}},
		allowDecision{}, Config{}, fixedClock{now: now}, projection,
	)
	_, err := service.Apply(context.Background(), ApplyInput{
		Principal:  domainsession.Principal{Authenticated: true, UserID: uuid.New(), SessionID: uuid.New(), Method: "email_password", AuthenticatedAt: now},
		PathUserID: userID.String(), UserStatusChangeProof: string(make([]byte, 32)), AuthorizationDecision: "allow",
	})
	var typed *failure.Error
	if !errors.As(err, &typed) || typed.Code != "AUTH_SERVICE_UNAVAILABLE" || typed.Kind != failure.KindUnavailable || typed.PublicMessage != unavailableMessage {
		t.Fatalf("unavailable failure = %#v", err)
	}
	if transactor.committed || projection.called || transactor.state.Status != domainuserauthstate.StatusActive {
		t.Fatalf("failed transaction leaked state: committed=%v projected=%v state=%q", transactor.committed, projection.called, transactor.state.Status)
	}
}

type recordingTransactor struct {
	state     domainuserauthstate.State
	committed bool
	revoked   bool
	revokeErr error
}

func (t *recordingTransactor) WithinTransaction(ctx context.Context, run func(TxRepositories) error) error {
	workingState := t.state
	repository := &recordingStateRepository{state: &workingState}
	revoker := recordingSessionRevoker{revoked: &t.revoked, err: t.revokeErr}
	if err := run(TxRepositories{States: repository, Sessions: revoker}); err != nil {
		return err
	}
	t.state = workingState
	t.committed = true
	return nil
}

type recordingStateRepository struct {
	state *domainuserauthstate.State
}

func (r *recordingStateRepository) FindForUpdate(context.Context, uuid.UUID) (domainuserauthstate.State, error) {
	return *r.state, nil
}

func (r *recordingStateRepository) Apply(_ context.Context, current domainuserauthstate.State, change domainuserauthstate.Change) (domainuserauthstate.State, error) {
	current.Status = change.Status
	current.UserVersion = change.UserVersion
	current.StatusChangeID = change.StatusChangeID
	current.EffectiveAt = change.ChangedAt
	current.RowVersion++
	*r.state = current
	return current, nil
}

type recordingSessionRevoker struct {
	revoked *bool
	err     error
}

func (r recordingSessionRevoker) RevokeForUser(context.Context, uuid.UUID, string) error {
	*r.revoked = true
	return r.err
}

type staticProofVerifier struct {
	proof StatusProof
	err   error
}

func (v staticProofVerifier) VerifyUserStatus(string) (StatusProof, error) {
	return v.proof, v.err
}

type allowDecision struct{}

func (allowDecision) Verify(context.Context, string, string, string, string) error { return nil }

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

type recordingProjection struct {
	committed *bool
	called    bool
}

func (p *recordingProjection) RevokeUser(context.Context, uuid.UUID) error {
	if !*p.committed {
		return errors.New("projection called before commit")
	}
	p.called = true
	return nil
}
