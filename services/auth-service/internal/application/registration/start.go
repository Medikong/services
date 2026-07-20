package registration

import (
	"context"
	"crypto/hmac"
	"errors"
	"strings"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidempotency "github.com/Medikong/services/services/auth-service/internal/domain/idempotency"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	domainoutbox "github.com/Medikong/services/services/auth-service/internal/domain/outbox"
	domainregistration "github.com/Medikong/services/services/auth-service/internal/domain/registration"
	"github.com/google/uuid"
)

func (s *Service) Start(ctx context.Context, input StartInput) (StartOutput, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" || strings.TrimSpace(input.ProfileRequestID) == "" || strings.TrimSpace(input.AgreementReceiptID) == "" {
		return StartOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "필수 회원가입 정보가 부족합니다.")
	}
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return StartOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "이메일 형식이 올바르지 않습니다.")
	}
	phone, err := normalizePhone(input.Phone)
	if err != nil {
		return StartOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "휴대폰 번호 형식이 올바르지 않습니다.")
	}
	if err := (domainidentity.PasswordPolicy{}).Validate(input.Password); err != nil {
		return StartOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "비밀번호 정책을 만족하지 않습니다.")
	}
	intentID, err := uuid.Parse(input.IntentID)
	if err != nil {
		return StartOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "인증 Intent 식별자가 올바르지 않습니다.")
	}
	passwordHash, err := s.passwords.HashPassword(input.Password)
	if err != nil {
		return StartOutput{}, unavailable(err)
	}
	requestHash := s.cryptography.Hash(email, phone, input.Password, input.ProfileRequestID, input.AgreementReceiptID, boolString(input.RememberMe))

	var output StartOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		currentIntent, verifyErr := s.verifyActiveIntent(ctx, repositories, intentID, input.OwnerProof, input.CSRFToken, true)
		if verifyErr != nil {
			return verifyErr
		}
		scopeHash := s.cryptography.Hash("start_registration", intentID.String())
		keyHash := s.cryptography.Hash(input.IdempotencyKey)
		record, findErr := repositories.Idempotency.FindForUpdate(ctx, "start_registration", scopeHash, keyHash)
		if findErr == nil {
			if !hmac.Equal(record.RequestHash, requestHash) || record.ResourceID == nil {
				return failure.Conflict("AUTH_IDEMPOTENCY_CONFLICT", "같은 멱등성 키를 다른 요청에 사용할 수 없습니다.")
			}
			registration, registrationErr := repositories.Registrations.FindForUpdate(ctx, *record.ResourceID)
			if errors.Is(registrationErr, domainregistration.ErrNotFound) {
				return failure.New(failure.KindConflict, "AUTH_REGISTRATION_EXPIRED", "회원가입 요청 시간이 만료되었습니다.")
			}
			if registrationErr != nil {
				return unavailable(registrationErr)
			}
			statusToken, tokenErr := s.cryptography.Opaque("rst_")
			if tokenErr != nil {
				return unavailable(tokenErr)
			}
			registration.StatusTokenHash = s.cryptography.Hash(registration.ID.String(), statusToken)
			if saveErr := repositories.Registrations.Save(ctx, &registration); saveErr != nil {
				return unavailable(saveErr)
			}
			output = startOutput(registration, statusToken)
			return nil
		}
		if !errors.Is(findErr, domainidempotency.ErrNotFound) {
			return unavailable(findErr)
		}

		registrationID, emailID, phoneID := uuid.New(), uuid.New(), uuid.New()
		statusToken, tokenErr := s.cryptography.Opaque("rst_")
		if tokenErr != nil {
			return unavailable(tokenErr)
		}
		now := s.clock.Now().UTC()
		expiresAt := minTime(now.Add(s.registrationTTL()), currentIntent.ExpiresAt)
		statusExpiresAt := expiresAt.Add(s.statusRetention())
		registration, newErr := domainregistration.New(domainregistration.NewInput{
			ID: registrationID, IntentID: currentIntent.ID, EmailIdentityID: emailID, PhoneIdentityID: phoneID,
			ProfileRequestID: input.ProfileRequestID, AgreementReceiptID: input.AgreementReceiptID,
			RememberMe: input.RememberMe, ClientChannel: string(currentIntent.Channel),
			StatusTokenHash: s.cryptography.Hash(registrationID.String(), statusToken), StatusTokenKeyVer: 1,
			StatusTokenExpires: statusExpiresAt, ExpiresAt: expiresAt, CreatedAt: now,
		})
		if newErr != nil {
			return failure.Invalid("AUTH_INPUT_INVALID", "회원가입 요청이 올바르지 않습니다.")
		}
		if reserveErr := repositories.Identities.Reserve(ctx, domainidentity.Identity{ID: emailID, Type: domainidentity.TypeEmail, NormalizedValue: email, MaskedValue: maskEmail(email)}); reserveErr != nil {
			return mapIdentityError(reserveErr)
		}
		if reserveErr := repositories.Identities.Reserve(ctx, domainidentity.Identity{ID: phoneID, Type: domainidentity.TypePhone, NormalizedValue: phone, MaskedValue: maskPhone(phone)}); reserveErr != nil {
			return mapIdentityError(reserveErr)
		}
		if credentialErr := repositories.Identities.CreatePasswordCredential(ctx, emailID, passwordHash); credentialErr != nil {
			return unavailable(credentialErr)
		}
		if createErr := repositories.Registrations.Create(ctx, registration); createErr != nil {
			return unavailable(createErr)
		}
		if createErr := repositories.Idempotency.CreateCompleted(ctx, domainidempotency.NewRecord(
			"start_registration", scopeHash, keyHash, requestHash, &registrationID, nil, statusExpiresAt,
		), "Registration", "created"); createErr != nil {
			return unavailable(createErr)
		}
		if appendErr := repositories.Outbox.Append(ctx, domainoutbox.Event{
			ID: uuid.New(), Type: "Auth.RegistrationStarted", AggregateType: "Registration", AggregateID: registrationID,
			Version: registration.Version, Payload: eventPayload(map[string]string{"registrationId": registrationID.String()}), CorrelationID: currentIntent.ID,
		}); appendErr != nil {
			return unavailable(appendErr)
		}
		if auditErr := repositories.Audit.Append(ctx, "auth.registration.started", "authentication_intent", currentIntent.ID, registrationID,
			map[string]string{"status": string(registration.Status)}, input.IdempotencyKey); auditErr != nil {
			return unavailable(auditErr)
		}
		output = startOutput(registration, statusToken)
		return nil
	})
	if err != nil {
		return StartOutput{}, preserveFailure(err)
	}
	return output, nil
}

func mapIdentityError(err error) error {
	if errors.Is(err, domainidentity.ErrConflict) {
		return failure.Conflict("AUTH_IDENTIFIER_UNAVAILABLE", "이미 사용할 수 없는 인증 수단입니다.")
	}
	return unavailable(err)
}
