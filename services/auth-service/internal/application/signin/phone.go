package signin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application"
	"github.com/Medikong/services/services/auth-service/internal/application/bootstrap"
	appsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PhoneService struct {
	pool         *pgxpool.Pool
	keys         security.Keys
	bootstrap    *bootstrap.Service
	intents      intent.Repository
	identities   identity.Repository
	challenges   challenge.Repository
	outbox       outbox.Repository
	sessions     *appsession.Service
	virtual      bool
	challengeTTL time.Duration
}

func NewPhoneService(pool *pgxpool.Pool, keys security.Keys, bootstrap *bootstrap.Service, intents intent.Repository, identities identity.Repository, challenges challenge.Repository, outbox outbox.Repository, sessions *appsession.Service, virtual bool, challengeTTL time.Duration) *PhoneService {
	return &PhoneService{pool: pool, keys: keys, bootstrap: bootstrap, intents: intents, identities: identities, challenges: challenges, outbox: outbox, sessions: sessions, virtual: virtual, challengeTTL: challengeTTL}
}

type PhoneIssueInput struct {
	IntentID, OwnerProof, CSRFToken, Phone, IdempotencyKey string
	RememberMe                                             bool
}
type PhoneIssueOutput struct {
	ChallengeID string
	ExpiresAt   time.Time
}

func (s *PhoneService) Issue(ctx context.Context, input PhoneIssueInput) (PhoneIssueOutput, error) {
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return PhoneIssueOutput{}, application.Problem(400, "AUTH_INPUT_INVALID", "휴대폰 로그인 요청이 올바르지 않습니다.")
	}
	phone, err := normalizePhone(input.Phone)
	if err != nil {
		return PhoneIssueOutput{}, application.Problem(400, "AUTH_INPUT_INVALID", "휴대폰 번호 형식이 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PhoneIssueOutput{}, application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	currentIntent, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, intentID, input.OwnerProof, input.CSRFToken, true)
	if err != nil {
		return PhoneIssueOutput{}, err
	}
	if err := s.intents.SetRememberMe(ctx, tx, intentID, input.RememberMe); err != nil {
		return PhoneIssueOutput{}, application.Unavailable()
	}
	currentIntent.RememberMe = &input.RememberMe
	var identityID *uuid.UUID
	destination, masked := "decoy:"+intentID.String(), "***"
	if currentIdentity, link, findErr := s.identities.FindActivePhoneLinkForUpdate(ctx, tx, phone); findErr == nil {
		identityID = &currentIdentity.ID
		destination, masked = currentIdentity.NormalizedValue, currentIdentity.MaskedValue
		_ = link
	} else if !errors.Is(findErr, identity.ErrNotFound) {
		return PhoneIssueOutput{}, application.Unavailable()
	}
	challengeID := uuid.New()
	now := time.Now().UTC()
	expires := minTime(now.Add(s.ttl()), currentIntent.ExpiresAt)
	code, err := s.keys.VerificationCode()
	if err != nil {
		return PhoneIssueOutput{}, application.Unavailable()
	}
	verification, err := challenge.New(challenge.NewInput{ID: challengeID, SubjectType: challenge.SubjectPhoneSignIn, SubjectID: intentID, Purpose: challenge.PurposePhoneSignIn, Method: challenge.MethodPhone, Channel: challenge.ChannelSMSCode, Destination: destination, DestinationLookupHash: s.keys.Hash("destination", destination), IdentityID: identityID, CodeHash: s.keys.Hash("challenge", challengeID.String(), code), VerifierKeyVersion: 1, MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(time.Minute), ExpiresAt: expires, CreatedAt: now})
	if err != nil {
		return PhoneIssueOutput{}, application.Unavailable()
	}
	if err := s.challenges.Issue(ctx, tx, verification); err != nil {
		return PhoneIssueOutput{}, application.Unavailable()
	}
	if identityID != nil {
		ciphertext, sealErr := s.keys.Seal(map[string]string{"code": code, "destination": destination})
		if sealErr != nil {
			return PhoneIssueOutput{}, application.Unavailable()
		}
		deliveryID := uuid.New()
		if err := s.challenges.StoreDeliveryPayload(ctx, tx, challenge.DeliveryPayload{ID: deliveryID, ChallengeID: challengeID, SendSequence: 1, Ciphertext: ciphertext, KeyID: "auth-replay-v1", AADHash: s.keys.Hash("delivery", challengeID.String()), ExpiresAt: expires}); err != nil {
			return PhoneIssueOutput{}, application.Unavailable()
		}
		if err := s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), Type: "Auth.PhoneSignInVerificationRequested", AggregateType: "AuthenticationIntent", AggregateID: intentID, Version: 0, Payload: payload(map[string]string{"challengeId": challengeID.String(), "deliveryId": deliveryID.String()}), CorrelationID: intentID}); err != nil {
			return PhoneIssueOutput{}, application.Unavailable()
		}
		if s.virtual {
			virtual, sealErr := s.keys.SealVirtual(map[string]string{"code": code})
			if sealErr != nil {
				return PhoneIssueOutput{}, application.Unavailable()
			}
			if err := s.challenges.StoreVirtualProjection(ctx, tx, challenge.VirtualProjection{ChallengeID: challengeID, Channel: challenge.ChannelSMSCode, ChallengeVersion: verification.Version, CodeCiphertext: virtual, CodeKeyID: "auth-virtual-v1", MaskedDestination: masked, Status: challenge.VirtualReady, ExpiresAt: expires, CreatedAt: now}); err != nil {
				return PhoneIssueOutput{}, application.Unavailable()
			}
		}
	}
	if err := application.AppendAudit(ctx, tx, "auth.phone_signin.challenge_issued", "authentication_intent", intentID, intentID, map[string]string{"status": "accepted"}, input.IdempotencyKey); err != nil {
		return PhoneIssueOutput{}, application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return PhoneIssueOutput{}, application.Unavailable()
	}
	return PhoneIssueOutput{ChallengeID: challengeID.String(), ExpiresAt: expires}, nil
}

type PhoneVerifyInput struct{ IntentID, ChallengeID, OwnerProof, CSRFToken, Code, IdempotencyKey string }

func (s *PhoneService) Verify(ctx context.Context, input PhoneVerifyInput) (Completed, error) {
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil || len(strings.TrimSpace(input.Code)) != 6 {
		return Completed{}, application.Problem(400, "AUTH_INPUT_INVALID", "확인 코드가 올바르지 않습니다.")
	}
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return Completed{}, application.Problem(400, "AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Completed{}, application.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	currentIntent, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, intentID, input.OwnerProof, input.CSRFToken, true)
	if err != nil {
		return Completed{}, err
	}
	verification, result, err := s.challenges.Consume(ctx, tx, challengeID, time.Now().UTC(), func(current challenge.Challenge) bool {
		return current.SubjectType == challenge.SubjectPhoneSignIn && current.SubjectID == intentID && s.keys.Equal(current.CodeHash, "challenge", current.ID.String(), input.Code)
	})
	if err != nil {
		return Completed{}, application.Unavailable()
	}
	if verification.SubjectType != challenge.SubjectPhoneSignIn || verification.SubjectID != intentID || verification.IdentityID == nil {
		return Completed{}, application.Problem(401, "AUTH_SIGNIN_FAILED", "휴대폰 로그인 정보를 확인할 수 없습니다.")
	}
	if !result.Verified {
		if err := tx.Commit(ctx); err != nil {
			return Completed{}, application.Unavailable()
		}
		if result.Failure == challenge.ConsumeFailureExpired {
			return Completed{}, application.Problem(410, "AUTH_CHALLENGE_EXPIRED", "확인 코드가 만료되었습니다.")
		}
		return Completed{}, application.Problem(400, "AUTH_CHALLENGE_FAILED", "확인 코드가 올바르지 않습니다.")
	}
	link, err := s.identities.FindActiveLinkForIdentity(ctx, tx, *verification.IdentityID)
	if errors.Is(err, identity.ErrNotFound) {
		return Completed{}, application.Problem(409, "AUTH_PHONE_IDENTITY_NOT_LINKED", "연결된 휴대폰 인증 수단이 없습니다.")
	}
	if err != nil {
		return Completed{}, application.Unavailable()
	}
	issued, err := s.sessions.IssueTx(ctx, tx, appsession.IssueInput{UserID: link.UserID, IdentityID: *verification.IdentityID, IdentityLink: link.ID, Method: "phone_otp", Channel: string(currentIntent.Channel), RememberMe: currentIntent.RememberMe != nil && *currentIntent.RememberMe})
	if err != nil {
		return Completed{}, err
	}
	sessionID, _ := uuid.Parse(issued.SessionID)
	if err := s.intents.Consume(ctx, tx, intentID, sessionID, "session_issued"); err != nil {
		return Completed{}, application.Unavailable()
	}
	if err := application.AppendAudit(ctx, tx, "auth.phone_signin.completed", "authentication_intent", intentID, intentID, map[string]string{"status": "completed"}, stableKey(input.IdempotencyKey, "phone-signin", challengeID)); err != nil {
		return Completed{}, application.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return Completed{}, application.Unavailable()
	}
	return Completed{Issued: issued, NextPath: currentIntent.ReturnPath, IntentID: currentIntent.ID.String()}, nil
}
func (s *PhoneService) ttl() time.Duration {
	if s.challengeTTL > 0 {
		return s.challengeTTL
	}
	return 10 * time.Minute
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func payload(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func stableKey(key, prefix string, id uuid.UUID) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return prefix + ":" + id.String()
}
func normalizePhone(value string) (string, error) {
	value = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), "-", "")
	if !strings.HasPrefix(value, "+") || len(value) < 8 {
		return "", errors.New("phone")
	}
	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return "", errors.New("phone")
		}
	}
	return value, nil
}
