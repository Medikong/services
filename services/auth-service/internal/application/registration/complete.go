package registration

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainintent "github.com/Medikong/services/services/auth-service/internal/domain/intent"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
)

func (s *Service) Complete(ctx context.Context, input CompleteInput) (CompleteOutput, error) {
	registrationID, err := uuid.Parse(input.RegistrationID)
	userID, userIDErr := uuid.Parse(input.UserID)
	if err != nil || userIDErr != nil || strings.TrimSpace(input.UserCreationProof) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return CompleteOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "회원가입 완료 요청이 올바르지 않습니다.")
	}
	proofRegistrationID, proofUserID, proofUserVersion, err := s.proofVerifier.VerifyUserCreation(input.UserCreationProof)
	if err != nil || proofRegistrationID != registrationID.String() || proofUserID != userID.String() || proofUserVersion < 1 {
		return CompleteOutput{}, failure.Forbidden("AUTH_USER_CREATION_PROOF_INVALID", "사용자 생성 증거가 유효하지 않습니다.")
	}

	var output CompleteOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		registration, findErr := repositories.Registrations.FindForUpdate(ctx, registrationID)
		if errors.Is(findErr, domainregistration.ErrNotFound) {
			return failure.NotFound("AUTH_REGISTRATION_NOT_FOUND", "회원가입 요청을 찾을 수 없습니다.")
		}
		if findErr != nil {
			return unavailable(findErr)
		}
		record, first, recordErr := s.completionRecord(ctx, repositories, registrationID, userID, proofUserVersion, input.IdempotencyKey)
		if recordErr != nil {
			return recordErr
		}

		var currentIntent domainintent.Intent
		if !first {
			if registration.Status == domainregistration.StatusCompleted && registration.SessionID != nil {
				currentIntent, err = s.verifyReplayIntent(ctx, repositories, registration.IntentID, *registration.SessionID, input.OwnerProof, input.CSRFToken)
			} else {
				currentIntent, err = s.verifyActiveIntent(ctx, repositories, registration.IntentID, input.OwnerProof, input.CSRFToken, true)
			}
			if err != nil {
				return err
			}
			if registration.Status != domainregistration.StatusCompleted || registration.UserID == nil || *registration.UserID != userID || record.Status != "completed" || record.ReplayID == nil {
				return failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 가입 완료 요청에 사용할 수 없습니다.")
			}
			issued, replayErr := s.replayCompletion(ctx, repositories, record)
			if replayErr != nil {
				return replayErr
			}
			output = CompleteOutput{
				RegistrationID: registrationID.String(), Status: registration.Status, Issued: issued,
				NextPath: currentIntent.ReturnPath, IntentID: currentIntent.ID.String(),
			}
			return nil
		}

		currentIntent, err = s.verifyActiveIntent(ctx, repositories, registration.IntentID, input.OwnerProof, input.CSRFToken, true)
		if err != nil {
			return err
		}
		if registration.Status != domainregistration.StatusPendingVerification || !registration.MethodVerified(domainregistration.MethodEmail) || !registration.MethodVerified(domainregistration.MethodPhone) || registration.EmailChallengeID == nil || registration.PhoneChallengeID == nil {
			return failure.Conflict("AUTH_VERIFICATION_REQUIRED", "이메일과 휴대폰 확인을 모두 완료해야 합니다.")
		}
		now := s.clock.Now().UTC()
		if !now.Before(registration.ExpiresAt) {
			return failure.New(failure.KindConflict, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청이 만료되었습니다.")
		}
		eventID := uuid.New()
		if transitionErr := registration.MarkVerificationCompleted(domainregistration.VerificationCompletion{
			EmailChallengeID: *registration.EmailChallengeID, PhoneChallengeID: *registration.PhoneChallengeID,
			EmailVerified: true, PhoneVerified: true, BindingID: uuid.New(), RegistrationVersion: registration.Version + 1,
			SnapshotHash:               s.cryptography.Hash(registration.ID.String(), registration.EmailChallengeID.String(), registration.PhoneChallengeID.String()),
			VerificationCompletedEvent: eventID, CompletionIdempotencyID: record.ID,
			LinkAcceptUntil: minTime(now.Add(s.linkWindow()), registration.ExpiresAt),
		}); transitionErr != nil {
			return failure.Conflict("AUTH_VERIFICATION_REQUIRED", "가입 확인 상태를 완료할 수 없습니다.")
		}
		if linkErr := registration.Link(domainregistration.UserLink{
			UserID: userID, LinkRequestID: record.ID, LinkedAt: now,
			SessionIssueUntil: minTime(now.Add(s.sessionWindow()), registration.ExpiresAt),
		}); linkErr != nil {
			return failure.New(failure.KindConflict, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청이 만료되었습니다.")
		}
		if linkErr := repositories.Identities.CreateActiveLink(ctx, domainidentity.Link{ID: uuid.New(), Identity: registration.EmailIdentityID, UserID: userID, Type: domainidentity.TypeEmail}); linkErr != nil {
			return unavailable(linkErr)
		}
		if linkErr := repositories.Identities.CreateActiveLink(ctx, domainidentity.Link{ID: uuid.New(), Identity: registration.PhoneIdentityID, UserID: userID, Type: domainidentity.TypePhone}); linkErr != nil {
			return unavailable(linkErr)
		}
		if stateErr := repositories.UserAuthState.CreateActiveForRegistration(ctx, userID, proofUserVersion, record.ID.String()); stateErr != nil {
			return unavailable(stateErr)
		}
		if transitionErr := registration.BeginSessionIssuance(s.clock.Now().UTC()); transitionErr != nil {
			return failure.New(failure.KindConflict, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청이 만료되었습니다.")
		}
		link, linkErr := repositories.Identities.FindActiveLinkForIdentityUser(ctx, registration.EmailIdentityID, userID)
		if linkErr != nil {
			return unavailable(linkErr)
		}
		if saveErr := repositories.Registrations.Save(ctx, &registration); saveErr != nil {
			return unavailable(saveErr)
		}
		issued, issueErr := s.sessions.IssueTx(ctx, repositories.Session, applicationsession.IssueInput{
			UserID: userID, IdentityID: registration.EmailIdentityID, IdentityLink: link.ID,
			Method: "registration_verified", Channel: registration.ClientChannel,
			RememberMe: registration.RememberMe, WebCSRFToken: input.CSRFToken,
		})
		if issueErr != nil {
			return issueErr
		}
		sessionID, parseErr := uuid.Parse(issued.SessionID)
		if parseErr != nil {
			return unavailable(parseErr)
		}
		if transitionErr := registration.Complete(sessionID, s.clock.Now().UTC()); transitionErr != nil {
			return failure.New(failure.KindConflict, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청이 만료되었습니다.")
		}
		if saveErr := repositories.Registrations.Save(ctx, &registration); saveErr != nil {
			return unavailable(saveErr)
		}
		if consumeErr := repositories.Intents.Consume(ctx, registration.IntentID, sessionID, "session_issued"); consumeErr != nil {
			return unavailable(consumeErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.registration.completed", "authentication_intent", currentIntent.ID, registrationID,
			map[string]string{"status": string(registration.Status)}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		ciphertext, sealErr := s.cryptography.Seal(issued)
		if sealErr != nil {
			return unavailable(sealErr)
		}
		replayID := uuid.New()
		if replayErr := repositories.Idempotency.CreateReplayPayload(ctx, domainidempotency.ReplayPayload{
			ID: replayID, Kind: "registration_completion", Ciphertext: ciphertext,
			BindingHash: record.RequestHash, ExpiresAt: minTime(issued.ExpiresAt, now.Add(s.statusRetention())),
		}); replayErr != nil {
			return unavailable(replayErr)
		}
		if attachErr := repositories.Idempotency.AttachReplayPayload(ctx, record.ID, replayID); attachErr != nil {
			return unavailable(attachErr)
		}
		if completeErr := repositories.Idempotency.Complete(ctx, record.ID, "registration_completed"); completeErr != nil {
			return unavailable(completeErr)
		}
		output = CompleteOutput{
			RegistrationID: registrationID.String(), Status: registration.Status, Issued: issued,
			NextPath: currentIntent.ReturnPath, IntentID: currentIntent.ID.String(),
		}
		return nil
	})
	if err != nil {
		return CompleteOutput{}, preserveFailure(err)
	}
	return output, nil
}

func (s *Service) completionRecord(ctx context.Context, repositories TxRepositories, registrationID, userID uuid.UUID, userVersion int64, key string) (domainidempotency.Record, bool, error) {
	scope := s.cryptography.Hash("complete_registration", registrationID.String())
	requestHash := s.cryptography.Hash(registrationID.String(), userID.String(), fmt.Sprint(userVersion))
	record := domainidempotency.NewRecord(
		"complete_registration", scope, s.cryptography.Hash(key), requestHash, &registrationID, nil,
		s.clock.Now().UTC().Add(s.statusRetention()),
	)
	claimed, first, err := repositories.Idempotency.ClaimProcessing(ctx, record, "Registration")
	if err != nil {
		return domainidempotency.Record{}, false, unavailable(err)
	}
	if !hmac.Equal(claimed.RequestHash, requestHash) {
		return domainidempotency.Record{}, false, failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
	}
	return claimed, first, nil
}

func (s *Service) replayCompletion(ctx context.Context, repositories TxRepositories, record domainidempotency.Record) (applicationsession.Issued, error) {
	payload, err := repositories.Idempotency.FindReplayPayloadForUpdate(ctx, *record.ReplayID)
	if err != nil || payload.Kind != "registration_completion" || payload.DestroyedAt != nil || !payload.ExpiresAt.After(s.clock.Now().UTC()) || !hmac.Equal(payload.BindingHash, record.RequestHash) {
		return applicationsession.Issued{}, failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "가입 완료 credential 재전달 기간이 끝났습니다.")
	}
	var issued applicationsession.Issued
	if openErr := s.cryptography.Open(payload.Ciphertext, &issued); openErr != nil || issued.SessionID == "" {
		return applicationsession.Issued{}, unavailable(openErr)
	}
	if replayErr := repositories.Idempotency.RecordReplay(ctx, payload.ID); replayErr != nil {
		return applicationsession.Issued{}, unavailable(replayErr)
	}
	return issued, nil
}
