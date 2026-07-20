package identity

import (
	"context"
	"errors"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	domainidentity "github.com/Medikong/services/services/auth-service/internal/domain/identity"
	"github.com/google/uuid"
)

func (s *Service) StartLink(ctx context.Context, input StartLinkInput) (StartLinkOutput, error) {
	phone, err := domainidentity.NormalizePhone(input.Phone)
	if err != nil {
		return StartLinkOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "휴대폰 번호 형식이 올바르지 않습니다.")
	}
	if !validIdempotencyKey(input.IdempotencyKey) {
		return StartLinkOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "멱등성 키가 올바르지 않습니다.")
	}
	var output StartLinkOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		replayedOutput, replayed, err := s.claimOrReplayLinkStart(ctx, repositories.Idempotency, "start_method_link", input.Principal, phone, input.Proof, input.IdempotencyKey)
		if err != nil {
			return err
		}
		if replayed {
			output = replayedOutput
			return nil
		}
		if _, err := s.reauth.ConsumeProofID(ctx, repositories.Proofs, input.Proof, input.Principal, "link_identity"); err != nil {
			return err
		}
		existing, findErr := repositories.Identities.FindByValueForUpdate(ctx, domainidentity.TypePhone, phone)
		if findErr == nil {
			link, linkErr := repositories.Identities.FindActiveLinkForIdentity(ctx, existing.ID)
			if linkErr == nil && link.UserID == input.Principal.UserID {
				output = StartLinkOutput{LinkID: link.ID.String(), Status: "active", Existing: true}
				return s.storeLinkStartReplay(ctx, repositories.Idempotency, "start_method_link", input.Principal, phone, input.Proof, input.IdempotencyKey, output)
			}
			return failure.Conflict("AUTH_IDENTITY_LINK_CONFLICT", "이미 사용할 수 없는 휴대폰 인증 수단입니다.")
		}
		if !errors.Is(findErr, domainidentity.ErrNotFound) {
			return unavailable(findErr)
		}
		identityID, linkID := uuid.New(), uuid.New()
		if err := repositories.Identities.Reserve(ctx, domainidentity.Identity{
			ID: identityID, Type: domainidentity.TypePhone, NormalizedValue: phone, MaskedValue: domainidentity.MaskPhone(phone),
		}); err != nil {
			return mapIdentityError(err)
		}
		expiresAt := s.clock.Now().UTC().Add(s.linkTTL())
		if err := repositories.Identities.CreateRequestedLink(ctx, domainidentity.Link{
			ID: linkID, Identity: identityID, UserID: input.Principal.UserID, Type: domainidentity.TypePhone, ExpiresAt: &expiresAt,
		}); err != nil {
			return unavailable(err)
		}
		output = StartLinkOutput{LinkID: linkID.String(), Status: "requested", ExpiresAt: expiresAt}
		if err := s.storeLinkStartReplay(ctx, repositories.Idempotency, "start_method_link", input.Principal, phone, input.Proof, input.IdempotencyKey, output); err != nil {
			return err
		}
		if err := repositories.Audit.Append(ctx, "auth.identity_link.requested", "user", input.Principal.UserID, linkID, map[string]string{"method": "phone"}, input.IdempotencyKey); err != nil {
			return unavailable(err)
		}
		return nil
	})
	if err != nil {
		return StartLinkOutput{}, unavailable(err)
	}
	return output, nil
}

func (s *Service) StartReplacement(ctx context.Context, input ReplacementInput) (StartLinkOutput, error) {
	phone, err := domainidentity.NormalizePhone(input.Phone)
	if err != nil {
		return StartLinkOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "휴대폰 번호 형식이 올바르지 않습니다.")
	}
	if !validIdempotencyKey(input.IdempotencyKey) {
		return StartLinkOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "멱등성 키가 올바르지 않습니다.")
	}
	var output StartLinkOutput
	err = s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		replayedOutput, replayed, err := s.claimOrReplayLinkStart(ctx, repositories.Idempotency, "start_phone_replacement", input.Principal, phone, input.Proof, input.IdempotencyKey)
		if err != nil {
			return err
		}
		if replayed {
			output = replayedOutput
			return nil
		}
		proofID, err := s.reauth.ConsumeProofID(ctx, repositories.Proofs, input.Proof, input.Principal, "replace_phone")
		if err != nil {
			return err
		}
		previous, _, err := repositories.Identities.FindActiveLinkForUserType(ctx, input.Principal.UserID, domainidentity.TypePhone)
		if err != nil {
			return failure.Conflict("AUTH_IDENTITY_LINK_CONFLICT", "교체할 휴대폰 인증 수단이 없습니다.")
		}
		if _, err := repositories.Identities.FindByValueForUpdate(ctx, domainidentity.TypePhone, phone); err == nil {
			return failure.Conflict("AUTH_IDENTITY_LINK_CONFLICT", "이미 사용할 수 없는 휴대폰 인증 수단입니다.")
		} else if !errors.Is(err, domainidentity.ErrNotFound) {
			return unavailable(err)
		}
		identityID, linkID := uuid.New(), uuid.New()
		if err := repositories.Identities.Reserve(ctx, domainidentity.Identity{
			ID: identityID, Type: domainidentity.TypePhone, NormalizedValue: phone, MaskedValue: domainidentity.MaskPhone(phone),
		}); err != nil {
			return mapIdentityError(err)
		}
		expiresAt := s.clock.Now().UTC().Add(s.linkTTL())
		if err := repositories.Identities.CreatePhoneReplacementRequested(ctx, domainidentity.Link{
			ID: linkID, Identity: identityID, UserID: input.Principal.UserID, Type: domainidentity.TypePhone, ExpiresAt: &expiresAt,
		}, previous.ID, proofID); err != nil {
			return unavailable(err)
		}
		output = StartLinkOutput{LinkID: linkID.String(), Status: "requested", ExpiresAt: expiresAt}
		if err := s.storeLinkStartReplay(ctx, repositories.Idempotency, "start_phone_replacement", input.Principal, phone, input.Proof, input.IdempotencyKey, output); err != nil {
			return err
		}
		if err := repositories.Audit.Append(ctx, "auth.phone_replacement.requested", "user", input.Principal.UserID, linkID, map[string]string{"method": "phone"}, input.IdempotencyKey); err != nil {
			return unavailable(err)
		}
		return nil
	})
	if err != nil {
		return StartLinkOutput{}, unavailable(err)
	}
	return output, nil
}
