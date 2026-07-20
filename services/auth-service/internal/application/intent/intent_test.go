package intent

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/google/uuid"
)

func TestBootstrapCreatePersistsIntentAndIdempotencyTogether(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	repositories := newFakeTxRepositories()
	crypto := &fakeCryptography{}
	service := NewBootstrapService(repositories, crypto, fixedClock{now: now}, BootstrapConfig{IntentTTL: 10 * time.Minute})

	result, err := service.Create(context.Background(), CreateInput{
		Channel: "web", ReturnPath: "/drops/one", IntentType: "purchase",
		ActionContext:  map[string]any{"dropId": "drop", "optionId": "option", "quantity": float64(1)},
		IdempotencyKey: "intent-create-key",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if repositories.intents.created == nil || repositories.intents.payload == nil || repositories.intents.boundPayload == uuid.Nil {
		t.Fatal("intent and action payload were not persisted")
	}
	if repositories.idempotency.created == nil || repositories.idempotency.created.ResourceID == nil || repositories.idempotency.created.ResourceID.String() != result.IntentID {
		t.Fatal("idempotency record was not linked to the created intent")
	}
	if result.Channel != "web" || !result.ExpiresAt.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("result channel or expiry is invalid: channel=%q expiry=%s", result.Channel, result.ExpiresAt)
	}
}

func TestActionResumeDeliversOnceInsideTransaction(t *testing.T) {
	now := time.Date(2026, 7, 20, 2, 3, 4, 0, time.UTC)
	intentID, sessionID, userID := uuid.New(), uuid.New(), uuid.New()
	crypto := &fakeCryptography{}
	ciphertext, err := crypto.Seal(map[string]any{"dropId": "drop", "optionId": "option", "quantity": float64(2)})
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	repositories := newFakeTxRepositories()
	repositories.intents.consumed = domainintent.Intent{ID: intentID, ReturnPath: "/drops/one"}
	repositories.intents.consumedPayload = domainintent.ActionPayload{ID: uuid.New(), IntentID: intentID, ActionName: "purchase", Ciphertext: ciphertext}
	service := NewActionResumeService(repositories, crypto, fixedClock{now: now})

	result, err := service.Resume(context.Background(), ResumeInput{
		Principal: Principal{Authenticated: true, SessionID: sessionID, UserID: userID},
		IntentID:  intentID.String(), IdempotencyKey: "resume-key",
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if repositories.intents.deliveredPayload != repositories.intents.consumedPayload.ID {
		t.Fatal("action payload was not marked delivered")
	}
	if repositories.idempotency.created == nil || repositories.audit.appended != 1 {
		t.Fatal("idempotency record and audit event were not persisted")
	}
	if result.IntentID != intentID.String() || result.Action != "purchase" || result.ReturnPath != "/drops/one" {
		t.Fatalf("Resume() result = %#v", result)
	}
}

func TestBootstrapRejectsExternalReturnPathBeforeTransaction(t *testing.T) {
	repositories := newFakeTxRepositories()
	service := NewBootstrapService(repositories, &fakeCryptography{}, fixedClock{}, BootstrapConfig{IntentTTL: time.Minute})

	_, err := service.Create(context.Background(), CreateInput{
		Channel: "web", ReturnPath: "https://outside.example.test", IntentType: "navigation", IdempotencyKey: "key",
	})
	var failureErr *failure.Error
	if !errors.As(err, &failureErr) || failureErr.Code != "AUTH_REDIRECT_INVALID" || repositories.calls != 0 {
		t.Fatalf("Create() error = %#v, transaction calls = %d", err, repositories.calls)
	}
}

type fakeTransactor struct {
	intents     *fakeIntentRepository
	idempotency *fakeIdempotencyRepository
	audit       *fakeAuditAppender
	calls       int
}

func newFakeTxRepositories() *fakeTransactor {
	return &fakeTransactor{
		intents:     &fakeIntentRepository{},
		idempotency: &fakeIdempotencyRepository{findErr: domainidempotency.ErrNotFound},
		audit:       &fakeAuditAppender{},
	}
}

func (f *fakeTransactor) WithinTransaction(ctx context.Context, run func(TxRepositories) error) error {
	f.calls++
	return run(TxRepositories{Intents: f.intents, Idempotency: f.idempotency, Audit: f.audit})
}

type fakeIntentRepository struct {
	active           domainintent.Intent
	consumed         domainintent.Intent
	consumedPayload  domainintent.ActionPayload
	created          *domainintent.CreateParams
	payload          *domainintent.ActionPayload
	boundPayload     uuid.UUID
	deliveredPayload uuid.UUID
}

func (f *fakeIntentRepository) Create(_ context.Context, value domainintent.CreateParams) error {
	f.created = &value
	return nil
}

func (f *fakeIntentRepository) FindActiveForUpdate(context.Context, uuid.UUID) (domainintent.Intent, error) {
	if f.active.ID == uuid.Nil {
		return domainintent.Intent{}, domainintent.ErrNotFound
	}
	return f.active, nil
}

func (f *fakeIntentRepository) RotateOwnerProof(context.Context, uuid.UUID, []byte, []byte) error {
	return nil
}

func (f *fakeIntentRepository) CreateActionPayload(_ context.Context, value domainintent.ActionPayload) error {
	f.payload = &value
	return nil
}

func (f *fakeIntentRepository) BindActionPayload(_ context.Context, _ uuid.UUID, payloadID uuid.UUID) error {
	f.boundPayload = payloadID
	return nil
}

func (f *fakeIntentRepository) FindConsumedActionForUpdate(context.Context, uuid.UUID, uuid.UUID) (domainintent.Intent, domainintent.ActionPayload, error) {
	if f.consumed.ID == uuid.Nil {
		return domainintent.Intent{}, domainintent.ActionPayload{}, domainintent.ErrNotFound
	}
	return f.consumed, f.consumedPayload, nil
}

func (f *fakeIntentRepository) MarkActionDelivered(_ context.Context, payloadID uuid.UUID) error {
	f.deliveredPayload = payloadID
	return nil
}

type fakeIdempotencyRepository struct {
	record  domainidempotency.Record
	findErr error
	created *domainidempotency.Record
}

func (f *fakeIdempotencyRepository) FindForUpdate(context.Context, string, []byte, []byte) (domainidempotency.Record, error) {
	return f.record, f.findErr
}

func (f *fakeIdempotencyRepository) CreateCompleted(_ context.Context, value domainidempotency.Record, _, _ string) error {
	f.created = &value
	return nil
}

type fakeAuditAppender struct {
	appended int
}

func (f *fakeAuditAppender) Append(context.Context, string, string, uuid.UUID, uuid.UUID, map[string]string, string) error {
	f.appended++
	return nil
}

type fakeCryptography struct {
	opaqueCalls int
}

func (*fakeCryptography) Hash(values ...string) []byte {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hash.Sum(nil)
}

func (f *fakeCryptography) Equal(expected []byte, values ...string) bool {
	return hmac.Equal(expected, f.Hash(values...))
}

func (*fakeCryptography) EqualHash(expected, actual []byte) bool {
	return hmac.Equal(expected, actual)
}

func (f *fakeCryptography) Opaque(prefix string) (string, error) {
	f.opaqueCalls++
	return prefix + string(rune('a'+f.opaqueCalls)), nil
}

func (*fakeCryptography) Seal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func (*fakeCryptography) Open(ciphertext []byte, target any) error {
	return json.Unmarshal(ciphertext, target)
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }
