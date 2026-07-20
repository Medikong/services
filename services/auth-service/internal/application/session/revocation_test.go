package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

func TestLogoutFencesBeforeRevocationAndResolvesAfterTransaction(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)

	for _, operation := range []struct {
		name string
		run  func(context.Context, *Service) error
	}{
		{name: "web", run: func(ctx context.Context, service *Service) error {
			return service.LogoutByWeb(ctx, "web-token", "csrf-token", uuid.NewString())
		}},
		{name: "refresh", run: func(ctx context.Context, service *Service) error {
			return service.LogoutByRefresh(ctx, "refresh-token", uuid.NewString())
		}},
	} {
		for _, scenario := range []struct {
			name        string
			mutationErr error
			fenceErr    error
			wantEvents  []string
			wantKind    failure.Kind
		}{
			{
				name:       "success",
				wantEvents: []string{"snapshot", "claim", "fence", "revoke", "complete", "commit", "resolve"},
			},
			{
				name:        "rollback",
				mutationErr: errors.New("revoke failed"),
				wantEvents:  []string{"snapshot", "claim", "fence", "revoke", "rollback", "resolve"},
				wantKind:    failure.KindUnavailable,
			},
			{
				name:       "partial_fence_failure",
				fenceErr:   errors.New("second fence write failed"),
				wantEvents: []string{"snapshot", "claim", "fence", "rollback", "resolve"},
				wantKind:   failure.KindUnavailable,
			},
		} {
			t.Run(operation.name+"/"+scenario.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				events := make([]string, 0, len(scenario.wantEvents))
				familyID := uuid.New()
				current := activeRevocationSession(now)
				repository := &revocationRepository{
					events:  &events,
					current: current,
					credential: domainsession.Credential{
						ID: uuid.New(), SessionID: current.ID, Type: "web_refresh_cookie", Status: "active",
						CSRFHash: []byte("csrf"), FamilyID: &familyID, ExpiresAt: now.Add(time.Hour),
					},
					mutationErr: scenario.mutationErr,
				}
				transaction := &revocationTransactor{
					events: &events, cancel: cancel,
					repositories: TxRepositories{
						Sessions: repository,
						Idempotency: &revocationIdempotencyRepository{
							events: &events,
						},
					},
				}
				projection := &revocationProjection{events: &events}
				fencer := &revocationFencer{
					events: &events, transactionDone: &transaction.finished, err: scenario.fenceErr,
				}
				service := newRevocationTestService(transaction, projection, now)
				service.UseSessionRevocation(fencer)

				err := operation.run(ctx, service)

				assertRevocationFailure(t, err, scenario.wantKind)
				assertRevocationEvents(t, events, scenario.wantEvents)
				assertResolvedFence(t, fencer, current.ID)
				if projection.called {
					t.Fatal("direct projection ran even though the fence resolved cache state")
				}
			})
		}
	}
}

func TestRefreshReuseFencesBeforeDetectionAndResolvesAfterTransaction(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)

	for _, scenario := range []struct {
		name        string
		mutationErr error
		fenceErr    error
		wantEvents  []string
		wantKind    failure.Kind
		wantCode    string
	}{
		{
			name:       "success",
			wantEvents: []string{"snapshot", "idempotency-find", "fence", "reuse", "commit", "resolve"},
			wantKind:   failure.KindUnauthenticated,
			wantCode:   "AUTH_SESSION_REVOKED",
		},
		{
			name:        "rollback",
			mutationErr: errors.New("reuse update failed"),
			wantEvents:  []string{"snapshot", "idempotency-find", "fence", "reuse", "rollback", "resolve"},
			wantKind:    failure.KindUnavailable,
			wantCode:    "AUTH_SERVICE_UNAVAILABLE",
		},
		{
			name:       "partial_fence_failure",
			fenceErr:   errors.New("second fence write failed"),
			wantEvents: []string{"snapshot", "idempotency-find", "fence", "rollback", "resolve"},
			wantKind:   failure.KindUnavailable,
			wantCode:   "AUTH_SERVICE_UNAVAILABLE",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events := make([]string, 0, len(scenario.wantEvents))
			familyID := uuid.New()
			current := activeRevocationSession(now)
			repository := &revocationRepository{
				events:  &events,
				current: current,
				credential: domainsession.Credential{
					ID: uuid.New(), SessionID: current.ID, Type: "mobile_refresh_token", Status: "rotated",
					FamilyID: &familyID, ExpiresAt: now.Add(time.Hour),
				},
				mutationErr: scenario.mutationErr,
			}
			transaction := &revocationTransactor{
				events: &events, cancel: cancel,
				repositories: TxRepositories{
					Sessions: repository,
					Idempotency: &revocationIdempotencyRepository{
						events: &events,
					},
				},
			}
			projection := &revocationProjection{events: &events}
			fencer := &revocationFencer{
				events: &events, transactionDone: &transaction.finished, err: scenario.fenceErr,
			}
			service := newRevocationTestService(transaction, projection, now)
			service.UseSessionRevocation(fencer)

			_, err := service.Refresh(ctx, "refresh-token", "", uuid.NewString())

			assertFailureKindAndCode(t, err, scenario.wantKind, scenario.wantCode)
			assertRevocationEvents(t, events, scenario.wantEvents)
			assertResolvedFence(t, fencer, current.ID)
			if projection.called {
				t.Fatal("direct projection ran even though the fence resolved cache state")
			}
		})
	}
}

func newRevocationTestService(transactor Transactor, projection StatusProjectionWriter, now time.Time) *Service {
	return NewService(
		transactor,
		revocationCryptography{},
		revocationClock{now: now},
		Config{SessionTTL: time.Hour, RememberMeSessionTTL: time.Hour, RefreshTTL: time.Hour},
		nil,
		projection,
	)
}

func activeRevocationSession(now time.Time) domainsession.Session {
	return domainsession.Session{
		ID: uuid.New(), UserID: uuid.New(), IdentityID: uuid.New(), IdentityLink: uuid.New(),
		Method: "email_password", Channel: domainsession.ChannelWeb,
		ExpiresAt: now.Add(time.Hour), Status: "active", Version: 1,
	}
}

func assertRevocationFailure(t *testing.T, err error, wantKind failure.Kind) {
	t.Helper()
	if wantKind == "" {
		if err != nil {
			t.Fatalf("operation error = %v", err)
		}
		return
	}
	assertFailureKindAndCode(t, err, wantKind, "AUTH_SERVICE_UNAVAILABLE")
}

func assertFailureKindAndCode(t *testing.T, err error, wantKind failure.Kind, wantCode string) {
	t.Helper()
	var typed *failure.Error
	if !errors.As(err, &typed) || typed.Kind != wantKind || typed.Code != wantCode {
		t.Fatalf("operation error = %#v, want kind=%q code=%q", err, wantKind, wantCode)
	}
}

func assertRevocationEvents(t *testing.T, events, want []string) {
	t.Helper()
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func assertResolvedFence(t *testing.T, fencer *revocationFencer, sessionID uuid.UUID) {
	t.Helper()
	if len(fencer.targets) != 1 || fencer.targets[0].ID != sessionID {
		t.Fatalf("fence targets = %#v, want session %s", fencer.targets, sessionID)
	}
	if fencer.fence == nil || !fencer.fence.contextLive || !fencer.fence.resolvedAfterTransaction {
		t.Fatalf("fence resolution = %#v", fencer.fence)
	}
}

type revocationTransactor struct {
	repositories TxRepositories
	events       *[]string
	cancel       context.CancelFunc
	finished     bool
}

func (t *revocationTransactor) WithinTransaction(ctx context.Context, run func(TxRepositories) error) error {
	if err := run(t.repositories); err != nil {
		t.finished = true
		*t.events = append(*t.events, "rollback")
		if t.cancel != nil {
			t.cancel()
		}
		return err
	}
	t.finished = true
	*t.events = append(*t.events, "commit")
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

type revocationRepository struct {
	Repository
	events      *[]string
	current     domainsession.Session
	credential  domainsession.Credential
	mutationErr error
}

func (r *revocationRepository) FindByWebSecretForUpdate(context.Context, []byte) (domainsession.Session, domainsession.Credential, error) {
	*r.events = append(*r.events, "snapshot")
	return r.current, r.credential, nil
}

func (r *revocationRepository) FindByRefreshSecretForUpdate(context.Context, []byte) (domainsession.Session, domainsession.Credential, error) {
	*r.events = append(*r.events, "snapshot")
	return r.current, r.credential, nil
}

func (r *revocationRepository) Revoke(context.Context, uuid.UUID, string) error {
	*r.events = append(*r.events, "revoke")
	return r.mutationErr
}

func (r *revocationRepository) MarkReuseDetected(context.Context, uuid.UUID, uuid.UUID) error {
	*r.events = append(*r.events, "reuse")
	return r.mutationErr
}

type revocationIdempotencyRepository struct {
	IdempotencyRepository
	events *[]string
}

func (r *revocationIdempotencyRepository) ClaimProcessing(_ context.Context, record domainidempotency.Record, _ string) (domainidempotency.Record, bool, error) {
	*r.events = append(*r.events, "claim")
	return record, true, nil
}

func (r *revocationIdempotencyRepository) Complete(context.Context, uuid.UUID, string) error {
	*r.events = append(*r.events, "complete")
	return nil
}

func (r *revocationIdempotencyRepository) FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error) {
	*r.events = append(*r.events, "idempotency-find")
	return domainidempotency.Record{}, domainidempotency.ErrNotFound
}

type revocationFencer struct {
	events          *[]string
	transactionDone *bool
	err             error
	targets         []domainsession.Session
	fence           *revocationFence
}

func (f *revocationFencer) Fence(_ context.Context, targets []domainsession.Session) (domainsession.RevocationFence, error) {
	*f.events = append(*f.events, "fence")
	f.targets = append([]domainsession.Session(nil), targets...)
	f.fence = &revocationFence{events: f.events, transactionDone: f.transactionDone}
	return f.fence, f.err
}

type revocationFence struct {
	events                   *[]string
	transactionDone          *bool
	contextLive              bool
	resolvedAfterTransaction bool
}

func (f *revocationFence) Resolve(ctx context.Context) error {
	f.contextLive = ctx.Err() == nil
	f.resolvedAfterTransaction = f.transactionDone != nil && *f.transactionDone
	*f.events = append(*f.events, "resolve")
	return nil
}

type revocationProjection struct {
	events *[]string
	called bool
}

func (p *revocationProjection) RevokeSession(context.Context, uuid.UUID) error {
	p.called = true
	*p.events = append(*p.events, "projection")
	return nil
}

func (*revocationProjection) RevokeUser(context.Context, uuid.UUID) error { return nil }

type revocationCryptography struct{ Cryptography }

func (revocationCryptography) Hash(values ...string) []byte {
	return []byte(strings.Join(values, "\x00"))
}

func (revocationCryptography) Equal([]byte, ...string) bool { return true }

type revocationClock struct{ now time.Time }

func (c revocationClock) Now() time.Time { return c.now }
