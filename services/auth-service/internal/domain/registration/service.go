// Package registration contains application commands for the signup
// lifecycle. It coordinates domain repositories but does not contain SQL,
// HTTP concerns, or an in-memory persistence substitute.
package registration

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Medikong/services/packages/go-platform/logger"
	"github.com/Medikong/services/services/auth-service/internal/domain"
	"github.com/Medikong/services/services/auth-service/internal/domain/challenge"
	"github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	"github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/Medikong/services/services/auth-service/internal/domain/intent"
	"github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	"github.com/Medikong/services/services/auth-service/internal/domain/session"
	statedomain "github.com/Medikong/services/services/auth-service/internal/domain/userauthstate"
	"github.com/Medikong/services/services/auth-service/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	RegistrationTTL       time.Duration
	StatusTokenRetention  time.Duration
	ChallengeTTL          time.Duration
	ChallengeResendDelay  time.Duration
	LinkAcceptanceWindow  time.Duration
	SessionDeliveryWindow time.Duration
	VirtualAdapterEnabled bool
}

type Service struct {
	pool          *pgxpool.Pool
	keys          security.Keys
	config        Config
	bootstrap     *intent.BootstrapService
	registrations Repository
	challenges    challenge.Repository
	identities    identity.Repository
	idempotency   idempotency.Repository
	outbox        outbox.Repository
	states        statedomain.Repository
	intents       intent.Repository
	sessions      *session.Service
	proofSigner   security.UserProofSigner
	proofVerifier security.UserProofVerifier
}

type ProofConfig struct {
	Signer   security.UserProofSigner
	Verifier security.UserProofVerifier
}

func NewService(
	pool *pgxpool.Pool,
	keys security.Keys,
	config Config,
	bootstrap *intent.BootstrapService,
	registrations Repository,
	challenges challenge.Repository,
	identities identity.Repository,
	idempotency idempotency.Repository,
	outbox outbox.Repository,
	states statedomain.Repository,
	intents intent.Repository,
	sessions *session.Service,
	proofs ...ProofConfig,
) *Service {
	service := &Service{
		pool: pool, keys: keys, config: config, bootstrap: bootstrap,
		registrations: registrations, challenges: challenges, identities: identities,
		idempotency: idempotency, outbox: outbox, states: states, intents: intents, sessions: sessions,
	}
	if len(proofs) > 0 {
		service.proofSigner = proofs[0].Signer
		service.proofVerifier = proofs[0].Verifier
	}
	return service
}

type StartInput struct {
	IntentID           string
	OwnerProof         string
	CSRFToken          string
	Email              string
	Password           string
	Phone              string
	ProfileRequestID   string
	AgreementReceiptID string
	RememberMe         bool
	IdempotencyKey     string
}

type StartOutput struct {
	RegistrationID          string
	Status                  Status
	RequiredVerifications   []string
	VerifiedMethods         []string
	ExpiresAt               time.Time
	RegistrationStatusToken string
	StatusTokenExpiresAt    time.Time
}

func (s *Service) Start(ctx context.Context, input StartInput) (StartOutput, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" || strings.TrimSpace(input.ProfileRequestID) == "" || strings.TrimSpace(input.AgreementReceiptID) == "" {
		return StartOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "필수 회원가입 정보가 부족합니다.")
	}
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return StartOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "이메일 형식이 올바르지 않습니다.")
	}
	phone, err := normalizePhone(input.Phone)
	if err != nil {
		return StartOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "휴대폰 번호 형식이 올바르지 않습니다.")
	}
	if err := (security.PasswordPolicy{}).Validate(input.Password); err != nil {
		return StartOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "비밀번호 정책을 만족하지 않습니다.")
	}
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil {
		return StartOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "인증 Intent 식별자가 올바르지 않습니다.")
	}
	passwordHash, err := security.HashPassword(input.Password)
	if err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	requestHash := s.keys.Hash(email, phone, input.Password, input.ProfileRequestID, input.AgreementReceiptID, boolString(input.RememberMe))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	currentIntent, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, intentID, input.OwnerProof, input.CSRFToken, true)
	if err != nil {
		return StartOutput{}, err
	}
	scopeHash := s.keys.Hash("start_registration", intentID.String())
	keyHash := s.keys.Hash(input.IdempotencyKey)
	record, err := s.idempotency.FindForUpdate(ctx, tx, "start_registration", scopeHash, keyHash)
	if err == nil {
		if !hmac.Equal(record.RequestHash, requestHash) || record.ResourceID == nil {
			return StartOutput{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
		}
		registration, findErr := s.registrations.FindForUpdate(ctx, tx, *record.ResourceID)
		if errors.Is(findErr, ErrNotFound) {
			return StartOutput{}, domain.Problem(410, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청 시간이 만료되었습니다.")
		}
		if findErr != nil {
			return StartOutput{}, domain.Unavailable()
		}
		statusToken, tokenErr := s.keys.Opaque("rst_")
		if tokenErr != nil {
			return StartOutput{}, domain.Unavailable()
		}
		registration.StatusTokenHash = s.keys.Hash(registration.ID.String(), statusToken)
		if err := s.registrations.Save(ctx, tx, &registration); err != nil {
			return StartOutput{}, domain.Unavailable()
		}
		if err := tx.Commit(ctx); err != nil {
			return StartOutput{}, domain.Unavailable()
		}
		return startOutput(registration, statusToken), nil
	}
	if !errors.Is(err, idempotency.ErrNotFound) {
		return StartOutput{}, domain.Unavailable()
	}
	registrationID, emailID, phoneID := uuid.New(), uuid.New(), uuid.New()
	statusToken, err := s.keys.Opaque("rst_")
	if err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	now := time.Now().UTC()
	expiresAt := minTime(now.Add(s.registrationTTL()), currentIntent.ExpiresAt)
	statusExpiresAt := expiresAt.Add(s.statusRetention())
	registration, err := New(NewInput{
		ID: registrationID, IntentID: currentIntent.ID, EmailIdentityID: emailID, PhoneIdentityID: phoneID,
		ProfileRequestID: input.ProfileRequestID, AgreementReceiptID: input.AgreementReceiptID,
		RememberMe: input.RememberMe, ClientChannel: string(currentIntent.Channel),
		StatusTokenHash: s.keys.Hash(registrationID.String(), statusToken), StatusTokenKeyVer: 1,
		StatusTokenExpires: statusExpiresAt, ExpiresAt: expiresAt, CreatedAt: now,
	})
	if err != nil {
		return StartOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "회원가입 요청이 올바르지 않습니다.")
	}
	if err := s.identities.Reserve(ctx, tx, identity.Identity{ID: emailID, Type: identity.TypeEmail, NormalizedValue: email, MaskedValue: maskEmail(email)}); err != nil {
		return StartOutput{}, mapIdentityError(err)
	}
	if err := s.identities.Reserve(ctx, tx, identity.Identity{ID: phoneID, Type: identity.TypePhone, NormalizedValue: phone, MaskedValue: maskPhone(phone)}); err != nil {
		return StartOutput{}, mapIdentityError(err)
	}
	if err := s.identities.CreatePasswordCredential(ctx, tx, emailID, passwordHash); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := s.registrations.Create(ctx, tx, registration); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := s.idempotency.CreateCompleted(ctx, tx, idempotency.NewRecord(
		"start_registration", scopeHash, keyHash, requestHash, &registrationID, nil, statusExpiresAt,
	), "Registration", "created"); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := s.appendEvent(ctx, tx, outbox.Event{
		ID: uuid.New(), Type: "Auth.RegistrationStarted", AggregateType: "Registration", AggregateID: registrationID,
		Version: registration.Version, Payload: eventPayload(map[string]string{"registrationId": registrationID.String()}), CorrelationID: currentIntent.ID,
	}); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := domain.AppendAudit(ctx, tx, "auth.registration.started", "authentication_intent", currentIntent.ID, registrationID,
		map[string]string{"status": string(registration.Status)}, input.IdempotencyKey); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return StartOutput{}, domain.Unavailable()
	}
	return startOutput(registration, statusToken), nil
}

type IssueChallengeInput struct {
	RegistrationID string
	OwnerProof     string
	CSRFToken      string
	Method         string
	IdempotencyKey string
}

type IssueChallengeOutput struct {
	ChallengeID       string
	Method            string
	MaskedDestination string
	ExpiresAt         time.Time
	ResendAvailableAt time.Time
}

func (s *Service) IssueChallenge(ctx context.Context, input IssueChallengeInput) (IssueChallengeOutput, error) {
	registrationID, err := uuid.Parse(input.RegistrationID)
	if err != nil || strings.TrimSpace(input.IdempotencyKey) == "" {
		return IssueChallengeOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "가입 Challenge 요청이 올바르지 않습니다.")
	}
	method := Method(strings.ToLower(strings.TrimSpace(input.Method)))
	if method != MethodEmail && method != MethodPhone {
		return IssueChallengeOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "확인 수단이 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	registration, err := s.registrations.FindForUpdate(ctx, tx, registrationID)
	if errors.Is(err, ErrNotFound) {
		return IssueChallengeOutput{}, domain.Problem(404, "AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	if _, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, registration.IntentID, input.OwnerProof, input.CSRFToken, true); err != nil {
		return IssueChallengeOutput{}, err
	}
	if registration.Status != StatusPendingVerification || !registration.ExpiresAt.After(time.Now()) {
		return IssueChallengeOutput{}, domain.Problem(410, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청 시간이 만료되었습니다.")
	}
	identityID := registration.EmailIdentityID
	purpose, channel := challenge.PurposeSignupEmail, challenge.ChannelEmailCode
	if method == MethodPhone {
		identityID, purpose, channel = registration.PhoneIdentityID, challenge.PurposeSignupPhone, challenge.ChannelSMSCode
	}
	target, err := s.identities.FindByIDForUpdate(ctx, tx, identityID)
	if err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	code, err := s.keys.VerificationCode()
	if err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	challengeID := uuid.New()
	now := time.Now().UTC()
	verification, err := challenge.New(challenge.NewInput{
		ID: challengeID, SubjectType: challenge.SubjectRegistration, SubjectID: registrationID, Purpose: purpose,
		Method: challenge.Method(method), Channel: channel, Destination: target.NormalizedValue,
		DestinationLookupHash: s.keys.Hash("destination", target.NormalizedValue), IdentityID: &identityID,
		CodeHash: s.keys.Hash("challenge", challengeID.String(), code), VerifierKeyVersion: 1,
		MaxAttempts: 5, MaxSends: 5, NextSendAt: now.Add(s.resendDelay()), ExpiresAt: minTime(now.Add(s.challengeTTL()), registration.ExpiresAt), CreatedAt: now,
	})
	if err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	if err := s.challenges.Issue(ctx, tx, verification); err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	if err := registration.AttachChallenge(method, challengeID); err != nil {
		return IssueChallengeOutput{}, domain.Problem(409, "AUTH_METHOD_UNAVAILABLE", "현재 상태에서는 해당 확인 수단을 사용할 수 없습니다.")
	}
	if err := s.registrations.Save(ctx, tx, &registration); err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	deliveryCiphertext, err := s.keys.Seal(map[string]string{"code": code, "destination": target.NormalizedValue})
	if err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	deliveryID := uuid.New()
	if err := s.challenges.StoreDeliveryPayload(ctx, tx, challenge.DeliveryPayload{
		ID: deliveryID, ChallengeID: challengeID, SendSequence: 1, Ciphertext: deliveryCiphertext,
		KeyID: "auth-replay-v1", AADHash: s.keys.Hash("delivery", challengeID.String()), ExpiresAt: verification.ExpiresAt,
	}); err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	if s.config.VirtualAdapterEnabled {
		ciphertext, err := s.keys.SealVirtual(map[string]string{"code": code})
		if err != nil {
			return IssueChallengeOutput{}, domain.Unavailable()
		}
		if err := s.challenges.StoreVirtualProjection(ctx, tx, challenge.VirtualProjection{
			ChallengeID: challengeID, Channel: channel, ChallengeVersion: verification.Version, CodeCiphertext: ciphertext,
			CodeKeyID: "auth-virtual-v1", MaskedDestination: target.MaskedValue, Status: challenge.VirtualReady,
			ExpiresAt: verification.ExpiresAt, CreatedAt: now,
		}); err != nil {
			return IssueChallengeOutput{}, domain.Unavailable()
		}
	}
	if err := s.appendEvent(ctx, tx, outbox.Event{
		ID: uuid.New(), Type: "Auth.VerificationRequested", AggregateType: "Registration", AggregateID: registrationID,
		Version: registration.Version, Payload: eventPayload(map[string]string{"challengeId": challengeID.String(), "deliveryId": deliveryID.String(), "channel": string(channel)}), CorrelationID: registration.IntentID,
	}); err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	if err := domain.AppendAudit(ctx, tx, "auth.registration.challenge_issued", "authentication_intent", registration.IntentID, registrationID,
		map[string]string{"method": string(method)}, input.IdempotencyKey); err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	if err := tx.Commit(ctx); err != nil {
		return IssueChallengeOutput{}, domain.Unavailable()
	}
	return IssueChallengeOutput{ChallengeID: challengeID.String(), Method: string(method), MaskedDestination: target.MaskedValue, ExpiresAt: verification.ExpiresAt, ResendAvailableAt: verification.NextSendAt}, nil
}

type VerifyChallengeInput struct {
	RegistrationID string
	ChallengeID    string
	OwnerProof     string
	CSRFToken      string
	Code           string
	IdempotencyKey string
}

type VerifyChallengeOutput struct {
	ChallengeID                 string
	Status                      string
	RegistrationStatus          string
	VerifiedMethods             []string
	RegistrationCompletionProof string
}

func (s *Service) VerifyChallenge(ctx context.Context, input VerifyChallengeInput) (VerifyChallengeOutput, error) {
	registrationID, err := uuid.Parse(input.RegistrationID)
	if err != nil || len(strings.TrimSpace(input.Code)) != 6 {
		return VerifyChallengeOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "확인 코드는 여섯 자리여야 합니다.")
	}
	challengeID, err := uuid.Parse(input.ChallengeID)
	if err != nil {
		return VerifyChallengeOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "Challenge 식별자가 올바르지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VerifyChallengeOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	registration, err := s.registrations.FindForUpdate(ctx, tx, registrationID)
	if errors.Is(err, ErrNotFound) {
		return VerifyChallengeOutput{}, domain.Problem(404, "AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return VerifyChallengeOutput{}, domain.Unavailable()
	}
	if _, err := s.bootstrap.VerifyOwnershipTx(ctx, tx, registration.IntentID, input.OwnerProof, input.CSRFToken, true); err != nil {
		return VerifyChallengeOutput{}, err
	}
	verification, result, err := s.challenges.Consume(ctx, tx, challengeID, time.Now().UTC(), func(current challenge.Challenge) bool {
		return current.SubjectType == challenge.SubjectRegistration && current.SubjectID == registrationID && s.keys.Equal(current.CodeHash, "challenge", current.ID.String(), input.Code)
	})
	if err != nil {
		return VerifyChallengeOutput{}, domain.Unavailable()
	}
	method, expectedID := Method(verification.Method), registration.EmailChallengeID
	if method == MethodPhone {
		expectedID = registration.PhoneChallengeID
	}
	if expectedID == nil || *expectedID != challengeID {
		return VerifyChallengeOutput{}, domain.Problem(404, "AUTH_REGISTRATION_NOT_FOUND", "가입 Challenge를 찾을 수 없습니다.")
	}
	if result.Verified {
		if result.Changed {
			if verification.IdentityID == nil {
				return VerifyChallengeOutput{}, domain.Unavailable()
			}
			if err := s.identities.MarkVerified(ctx, tx, *verification.IdentityID); err != nil {
				return VerifyChallengeOutput{}, domain.Unavailable()
			}
			if err := registration.MarkMethodVerified(method); err != nil {
				return VerifyChallengeOutput{}, domain.Unavailable()
			}
			if err := s.registrations.Save(ctx, tx, &registration); err != nil {
				return VerifyChallengeOutput{}, domain.Unavailable()
			}
			if err := domain.AppendAudit(ctx, tx, "auth.registration.challenge_verified", "authentication_intent", registration.IntentID, registrationID,
				map[string]string{"method": string(method)}, stableIdempotency(input.IdempotencyKey, "verify-registration", challengeID)); err != nil {
				return VerifyChallengeOutput{}, domain.Unavailable()
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return VerifyChallengeOutput{}, domain.Unavailable()
		}
		registrationStatus := string(registration.Status)
		completionProof := ""
		if registration.MethodVerified(MethodEmail) && registration.MethodVerified(MethodPhone) {
			registrationStatus = "verified"
			completionProof, err = s.proofSigner.SignRegistrationCompletion(registration.ID.String(), s.config.StatusTokenRetention)
			if err != nil {
				return VerifyChallengeOutput{}, domain.Unavailable()
			}
		}
		return VerifyChallengeOutput{
			ChallengeID:                 challengeID.String(),
			Status:                      "verified",
			RegistrationStatus:          registrationStatus,
			VerifiedMethods:             registrationVerifiedMethods(registration),
			RegistrationCompletionProof: completionProof,
		}, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return VerifyChallengeOutput{}, domain.Unavailable()
	}
	switch result.Failure {
	case challenge.ConsumeFailureExpired:
		return VerifyChallengeOutput{}, domain.Problem(410, "AUTH_CHALLENGE_EXPIRED", "확인 코드가 만료되었습니다.")
	case challenge.ConsumeFailureMismatch, challenge.ConsumeFailureInvalid:
		return VerifyChallengeOutput{}, domain.Problem(400, "AUTH_CHALLENGE_FAILED", "확인 코드가 올바르지 않습니다.")
	default:
		return VerifyChallengeOutput{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "현재 Challenge를 다시 사용할 수 없습니다.")
	}
}

type StatusInput struct {
	RegistrationID string
	OwnerProof     string
	CSRFToken      string
	StatusToken    string
}

type StatusOutput struct {
	RegistrationID  string
	Status          Status
	VerifiedMethods []string
	Retryable       bool
	ExpiresAt       time.Time
}

func (s *Service) Status(ctx context.Context, input StatusInput) (StatusOutput, error) {
	registrationID, err := uuid.Parse(input.RegistrationID)
	if err != nil {
		return StatusOutput{}, domain.Problem(404, "AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StatusOutput{}, domain.Unavailable()
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	registration, err := s.registrations.Find(ctx, tx, registrationID)
	if errors.Is(err, ErrNotFound) {
		return StatusOutput{}, domain.Problem(404, "AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return StatusOutput{}, domain.Unavailable()
	}
	authorized := false
	if strings.TrimSpace(input.OwnerProof) != "" {
		_, verifyErr := s.bootstrap.VerifyOwnershipTx(ctx, tx, registration.IntentID, input.OwnerProof, input.CSRFToken, false)
		authorized = verifyErr == nil
	} else if strings.TrimSpace(input.StatusToken) != "" && registration.StatusTokenExpires.After(time.Now()) {
		authorized = s.keys.Equal(registration.StatusTokenHash, registration.ID.String(), input.StatusToken)
	}
	if !authorized {
		return StatusOutput{}, domain.Problem(404, "AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
	}
	if err := tx.Commit(ctx); err != nil {
		return StatusOutput{}, domain.Unavailable()
	}
	return StatusOutput{RegistrationID: registration.ID.String(), Status: registration.Status, VerifiedMethods: registrationVerifiedMethods(registration), Retryable: registrationRetryable(registration.Status), ExpiresAt: registration.ExpiresAt}, nil
}

func registrationVerifiedMethods(registration Registration) []string {
	methods := make([]string, len(registration.VerifiedMethods))
	for index, method := range registration.VerifiedMethods {
		methods[index] = string(method)
	}
	return methods
}

type CompleteInput struct {
	RegistrationID    string
	UserID            string
	UserCreationProof string
	OwnerProof        string
	CSRFToken         string
	IdempotencyKey    string
}

type CompleteOutput struct {
	RegistrationID string
	Status         Status
	Issued         session.Issued
	NextPath       string
	IntentID       string
}

func (s *Service) Complete(ctx context.Context, input CompleteInput) (CompleteOutput, error) {
	registrationID, err := uuid.Parse(input.RegistrationID)
	userID, userIDErr := uuid.Parse(input.UserID)
	if err != nil || userIDErr != nil || strings.TrimSpace(input.UserCreationProof) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return CompleteOutput{}, domain.Problem(400, "AUTH_INPUT_INVALID", "회원가입 완료 요청이 올바르지 않습니다.")
	}
	proof, err := s.proofVerifier.VerifyUserCreation(input.UserCreationProof)
	if err != nil || proof.RegistrationID != registrationID.String() || proof.UserID != userID.String() || proof.UserVersion < 1 {
		return CompleteOutput{}, domain.Problem(403, "AUTH_USER_CREATION_PROOF_INVALID", "사용자 생성 증거가 유효하지 않습니다.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "begin", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	registration, err := s.registrations.FindForUpdate(ctx, tx, registrationID)
	if errors.Is(err, ErrNotFound) {
		return CompleteOutput{}, domain.Problem(404, "AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
	}
	if err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "load_registration", err)
	}
	record, first, err := s.completionRecord(ctx, tx, registrationID, userID, proof.UserVersion, input.IdempotencyKey)
	if err != nil {
		return CompleteOutput{}, err
	}
	var currentIntent intent.Intent
	if !first {
		if registration.Status == StatusCompleted && registration.SessionID != nil {
			currentIntent, err = s.bootstrap.VerifyCompletionReplayOwnershipTx(ctx, tx, registration.IntentID, *registration.SessionID, input.OwnerProof, input.CSRFToken)
		} else {
			currentIntent, err = s.bootstrap.VerifyOwnershipTx(ctx, tx, registration.IntentID, input.OwnerProof, input.CSRFToken, true)
		}
		if err != nil {
			return CompleteOutput{}, err
		}
		if registration.Status != StatusCompleted || registration.UserID == nil || *registration.UserID != userID || record.Status != "completed" || record.ReplayID == nil {
			return CompleteOutput{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 가입 완료 요청에 사용할 수 없습니다.")
		}
		issued, replayErr := s.replayCompletion(ctx, tx, record)
		if replayErr != nil {
			return CompleteOutput{}, replayErr
		}
		if err := tx.Commit(ctx); err != nil {
			return CompleteOutput{}, completeUnavailable(ctx, "commit_replay", err)
		}
		return CompleteOutput{RegistrationID: registrationID.String(), Status: registration.Status, Issued: issued, NextPath: currentIntent.ReturnPath, IntentID: currentIntent.ID.String()}, nil
	}
	currentIntent, err = s.bootstrap.VerifyOwnershipTx(ctx, tx, registration.IntentID, input.OwnerProof, input.CSRFToken, true)
	if err != nil {
		return CompleteOutput{}, err
	}
	if registration.Status != StatusPendingVerification || !registration.MethodVerified(MethodEmail) || !registration.MethodVerified(MethodPhone) || registration.EmailChallengeID == nil || registration.PhoneChallengeID == nil {
		return CompleteOutput{}, domain.Problem(409, "AUTH_VERIFICATION_REQUIRED", "이메일과 휴대폰 확인을 모두 완료해야 합니다.")
	}
	now := time.Now().UTC()
	if !now.Before(registration.ExpiresAt) {
		return CompleteOutput{}, domain.Problem(410, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청이 만료되었습니다.")
	}
	eventID := uuid.New()
	if err := registration.MarkVerificationCompleted(VerificationCompletion{
		EmailChallengeID: *registration.EmailChallengeID, PhoneChallengeID: *registration.PhoneChallengeID,
		EmailVerified: true, PhoneVerified: true, BindingID: uuid.New(), RegistrationVersion: registration.Version + 1,
		SnapshotHash:               s.keys.Hash(registration.ID.String(), registration.EmailChallengeID.String(), registration.PhoneChallengeID.String()),
		VerificationCompletedEvent: eventID, CompletionIdempotencyID: record.ID,
		LinkAcceptUntil: minTime(now.Add(s.linkWindow()), registration.ExpiresAt),
	}); err != nil {
		return CompleteOutput{}, domain.Problem(409, "AUTH_VERIFICATION_REQUIRED", "가입 확인 상태를 완료할 수 없습니다.")
	}
	if err := registration.Link(UserLink{UserID: userID, LinkRequestID: record.ID, LinkedAt: now, SessionIssueUntil: minTime(now.Add(s.sessionWindow()), registration.ExpiresAt)}); err != nil {
		return CompleteOutput{}, domain.Problem(410, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청이 만료되었습니다.")
	}
	if err := s.identities.CreateActiveLink(ctx, tx, identity.Link{ID: uuid.New(), Identity: registration.EmailIdentityID, UserID: userID, Type: identity.TypeEmail}); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "create_email_link", err)
	}
	if err := s.identities.CreateActiveLink(ctx, tx, identity.Link{ID: uuid.New(), Identity: registration.PhoneIdentityID, UserID: userID, Type: identity.TypePhone}); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "create_phone_link", err)
	}
	if err := s.states.CreateActiveForRegistration(ctx, tx, userID, proof.UserVersion, record.ID.String()); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "create_user_auth_state", err)
	}
	if err := registration.BeginSessionIssuance(time.Now().UTC()); err != nil {
		return CompleteOutput{}, domain.Problem(410, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청이 만료되었습니다.")
	}
	link, err := s.identities.FindActiveLinkForIdentityUser(ctx, tx, registration.EmailIdentityID, userID)
	if err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "load_email_link", err)
	}
	if err := s.registrations.Save(ctx, tx, &registration); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "save_issuing_registration", err)
	}
	issued, err := s.sessions.IssueTx(ctx, tx, session.IssueInput{UserID: userID, IdentityID: registration.EmailIdentityID, IdentityLink: link.ID, Method: "registration_verified", Channel: registration.ClientChannel, RememberMe: registration.RememberMe, WebCSRFToken: input.CSRFToken})
	if err != nil {
		return CompleteOutput{}, err
	}
	sessionID, _ := uuid.Parse(issued.SessionID)
	if err := registration.Complete(sessionID, time.Now().UTC()); err != nil {
		return CompleteOutput{}, domain.Problem(410, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청이 만료되었습니다.")
	}
	if err := s.registrations.Save(ctx, tx, &registration); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "save_completed_registration", err)
	}
	if err := s.intents.Consume(ctx, tx, registration.IntentID, sessionID, "session_issued"); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "consume_intent", err)
	}
	if err := domain.AppendAudit(ctx, tx, "auth.registration.completed", "authentication_intent", currentIntent.ID, registrationID,
		map[string]string{"status": string(registration.Status)}, input.IdempotencyKey); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "append_audit", err)
	}
	ciphertext, err := s.keys.Seal(issued)
	if err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "seal_replay", err)
	}
	replayID := uuid.New()
	if err := s.idempotency.CreateReplayPayload(ctx, tx, idempotency.ReplayPayload{
		ID: replayID, Kind: "registration_completion", Ciphertext: ciphertext,
		BindingHash: record.RequestHash, ExpiresAt: minTime(issued.ExpiresAt, now.Add(s.config.StatusTokenRetention)),
	}); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "create_replay", err)
	}
	if err := s.idempotency.AttachReplayPayload(ctx, tx, record.ID, replayID); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "attach_replay", err)
	}
	if err := s.idempotency.Complete(ctx, tx, record.ID, "registration_completed"); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "complete_idempotency", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CompleteOutput{}, completeUnavailable(ctx, "commit", err)
	}
	return CompleteOutput{RegistrationID: registrationID.String(), Status: registration.Status, Issued: issued, NextPath: currentIntent.ReturnPath, IntentID: currentIntent.ID.String()}, nil
}

func completeUnavailable(ctx context.Context, stage string, err error) error {
	logger.Error(ctx, "auth.registration.complete_failed", "stage", stage, logger.Err(err))
	return domain.Unavailable()
}

func (s *Service) completionRecord(ctx context.Context, tx pgx.Tx, registrationID, userID uuid.UUID, userVersion int64, key string) (idempotency.Record, bool, error) {
	scope := s.keys.Hash("complete_registration", registrationID.String())
	requestHash := s.keys.Hash(registrationID.String(), userID.String(), fmt.Sprint(userVersion))
	record := idempotency.NewRecord("complete_registration", scope, s.keys.Hash(key), requestHash, &registrationID, nil, time.Now().UTC().Add(s.statusRetention()))
	claimed, first, err := s.idempotency.ClaimProcessing(ctx, tx, record, "Registration")
	if err != nil {
		return idempotency.Record{}, false, domain.Unavailable()
	}
	if !hmac.Equal(claimed.RequestHash, requestHash) {
		return idempotency.Record{}, false, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	return claimed, first, nil
}

func (s *Service) replayCompletion(ctx context.Context, tx pgx.Tx, record idempotency.Record) (session.Issued, error) {
	payload, err := s.idempotency.FindReplayPayloadForUpdate(ctx, tx, *record.ReplayID)
	if err != nil || payload.Kind != "registration_completion" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(time.Now()) || !hmac.Equal(payload.BindingHash, record.RequestHash) {
		return session.Issued{}, domain.Problem(409, "AUTH_IDEMPOTENCY_CONFLICT", "가입 완료 credential 재전달 기간이 끝났습니다.")
	}
	var issued session.Issued
	if err := s.keys.Open(payload.Ciphertext, &issued); err != nil || issued.SessionID == "" {
		return session.Issued{}, domain.Unavailable()
	}
	if err := s.idempotency.RecordReplay(ctx, tx, payload.ID); err != nil {
		return session.Issued{}, domain.Unavailable()
	}
	return issued, nil
}

func (s *Service) appendEvent(ctx context.Context, tx pgx.Tx, event outbox.Event) error {
	return s.outbox.Append(ctx, tx, event)
}
func (s *Service) registrationTTL() time.Duration {
	if s.config.RegistrationTTL > 0 {
		return s.config.RegistrationTTL
	}
	return 30 * time.Minute
}
func (s *Service) statusRetention() time.Duration {
	if s.config.StatusTokenRetention > 0 {
		return s.config.StatusTokenRetention
	}
	return 30 * time.Minute
}
func (s *Service) challengeTTL() time.Duration {
	if s.config.ChallengeTTL > 0 {
		return s.config.ChallengeTTL
	}
	return 10 * time.Minute
}
func (s *Service) resendDelay() time.Duration {
	if s.config.ChallengeResendDelay > 0 {
		return s.config.ChallengeResendDelay
	}
	return time.Minute
}
func (s *Service) linkWindow() time.Duration {
	if s.config.LinkAcceptanceWindow > 0 {
		return s.config.LinkAcceptanceWindow
	}
	return 10 * time.Minute
}
func (s *Service) sessionWindow() time.Duration {
	if s.config.SessionDeliveryWindow > 0 {
		return s.config.SessionDeliveryWindow
	}
	return 10 * time.Minute
}

func startOutput(registration Registration, token string) StartOutput {
	methods := make([]string, len(registration.VerifiedMethods))
	for index, method := range registration.VerifiedMethods {
		methods[index] = string(method)
	}
	return StartOutput{RegistrationID: registration.ID.String(), Status: registration.Status, RequiredVerifications: []string{"email", "phone"}, VerifiedMethods: methods, ExpiresAt: registration.ExpiresAt, RegistrationStatusToken: token, StatusTokenExpiresAt: registration.StatusTokenExpires}
}
func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
func stableIdempotency(key, prefix string, id uuid.UUID) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return prefix + ":" + id.String()
}
func eventPayload(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func registrationRetryable(status Status) bool {
	return status == StatusPendingVerification || status == StatusAwaitingUserLink || status == StatusLinked || status == StatusIssuingSession
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 254 || strings.Count(value, "@") != 1 || strings.HasPrefix(value, "@") || strings.HasSuffix(value, "@") {
		return "", errors.New("invalid email")
	}
	return value, nil
}
func normalizePhone(value string) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), " ", "")
	value = strings.ReplaceAll(value, "-", "")
	if len(value) < 8 || len(value) > 20 || !strings.HasPrefix(value, "+") {
		return "", errors.New("invalid phone")
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			return "", errors.New("invalid phone")
		}
	}
	return value, nil
}
func maskEmail(value string) string {
	parts := strings.Split(value, "@")
	if len(parts) != 2 {
		return "***"
	}
	local := parts[0]
	if len(local) <= 1 {
		local = "*"
	} else {
		local = local[:1] + "***"
	}
	return local + "@" + parts[1]
}
func maskPhone(value string) string {
	if len(value) <= 4 {
		return "****"
	}
	return value[:3] + strings.Repeat("*", len(value)-5) + value[len(value)-2:]
}
func mapIdentityError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return domain.Problem(409, "AUTH_IDENTIFIER_UNAVAILABLE", "이미 사용할 수 없는 인증 수단입니다.")
	}
	return domain.Unavailable()
}
