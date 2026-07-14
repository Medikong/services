package user

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/user-service/internal/security"
)

type CreateUserInput struct {
	RegistrationID              string
	RegistrationCompletionProof string
	PrivateName                 string
	Nickname                    string
	Introduction                *string
	RequiredAgreements          []AgreementAcceptance
	IdempotencyKey              string
}

type CreateUserOutput struct {
	UserID            uuid.UUID
	UserVersion       int64
	CreatedAt         time.Time
	UserCreationProof string
	Replayed          bool
}

func (s *UserService) CreateUser(ctx context.Context, input CreateUserInput) (CreateUserOutput, error) {
	registrationID, err := NormalizeRegistrationID(input.RegistrationID)
	if err != nil {
		return CreateUserOutput{}, inputError(err)
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return CreateUserOutput{}, inputError(err)
	}
	privateName, err := NormalizePrivateName(input.PrivateName)
	if err != nil {
		return CreateUserOutput{}, profilePolicyError(err)
	}
	nickname, err := NormalizeNickname(input.Nickname)
	if err != nil {
		return CreateUserOutput{}, profilePolicyError(err)
	}
	introduction, err := NormalizeIntroduction(input.Introduction)
	if err != nil {
		return CreateUserOutput{}, profilePolicyError(err)
	}
	claims, err := s.authProofs.Verify(input.RegistrationCompletionProof, "user-service", "create_user")
	if err != nil || claims.RegistrationID != registrationID || !claims.EmailVerified || !claims.PhoneVerified {
		return CreateUserOutput{}, categorizedError(ErrRegistrationProofInvalid, err)
	}
	agreements, err := s.validateAgreements(input.RequiredAgreements)
	if err != nil {
		return CreateUserOutput{}, categorizedError(ErrRequiredAgreementInvalid, err)
	}

	requestHash, err := hashRequest(struct {
		RegistrationID string                `json:"registrationId"`
		PrivateName    string                `json:"privateName"`
		Nickname       string                `json:"nickname"`
		Introduction   *string               `json:"introduction"`
		Agreements     []AgreementAcceptance `json:"agreements"`
	}{registrationID, privateName, nickname, introduction, agreements})
	if err != nil {
		return CreateUserOutput{}, serviceOperationError(operationCreateUser, err)
	}
	now := s.now()
	record := newIdempotencyRecord(operationCreateUser, registrationID, registrationID, requestHash, now, s.idempotencyTTL)
	tx, err := s.repository.Begin(ctx)
	if err != nil {
		return CreateUserOutput{}, serviceOperationError(operationCreateUser, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, replayed, err := s.repository.ClaimIdempotency(ctx, tx, record)
	if err != nil {
		if errors.Is(err, ErrIdempotencyConflict) {
			err = categorizedError(ErrRegistrationConflict, err)
		}
		return CreateUserOutput{}, serviceOperationError(operationCreateUser, err)
	}
	if replayed {
		id, parseErr := uuid.Parse(*claimed.ResultID)
		if parseErr != nil {
			return CreateUserOutput{}, serviceOperationError(operationCreateUser, parseErr)
		}
		current, queryErr := s.repository.GetByIDTx(ctx, tx, id)
		if queryErr != nil {
			return CreateUserOutput{}, serviceOperationError(operationCreateUser, queryErr)
		}
		_ = tx.Rollback(ctx)
		proof, proofErr := s.signCreationProof(current.ID, registrationID, *claimed.ResultVersion)
		if proofErr != nil {
			return CreateUserOutput{}, serviceOperationError(operationCreateUser, proofErr)
		}
		return CreateUserOutput{UserID: current.ID, UserVersion: *claimed.ResultVersion, CreatedAt: current.CreatedAt, UserCreationProof: proof, Replayed: true}, nil
	}
	ciphertext, err := s.sealer.Seal(privateName)
	if err != nil {
		return CreateUserOutput{}, serviceOperationError(operationCreateUser, err)
	}
	created, err := s.repository.Create(ctx, tx, CreateInput{
		UserID:                uuid.New(),
		RegistrationID:        registrationID,
		PrivateNameCiphertext: ciphertext,
		Nickname:              nickname,
		Introduction:          introduction,
		Agreements:            agreements,
		Now:                   now,
	})
	if err != nil {
		return CreateUserOutput{}, serviceOperationError(operationCreateUser, err)
	}
	if err := s.repository.CompleteIdempotency(ctx, tx, record, "user", created.ID.String(), created.Version); err != nil {
		return CreateUserOutput{}, serviceOperationError(operationCreateUser, err)
	}
	if err := commit(ctx, tx); err != nil {
		return CreateUserOutput{}, serviceOperationError(operationCreateUser, err)
	}
	proof, err := s.signCreationProof(created.ID, registrationID, created.Version)
	if err != nil {
		return CreateUserOutput{}, serviceOperationError(operationCreateUser, err)
	}
	return CreateUserOutput{UserID: created.ID, UserVersion: created.Version, CreatedAt: created.CreatedAt, UserCreationProof: proof}, nil
}

func (s *UserService) validateAgreements(input []AgreementAcceptance) ([]AgreementAcceptance, error) {
	if len(input) != len(s.requiredAgreements) {
		return nil, oops.New("required agreement count does not match")
	}
	seen := make(map[string]struct{}, len(input))
	now := s.now()
	result := append([]AgreementAcceptance(nil), input...)
	for i := range result {
		result[i].Code = strings.TrimSpace(result[i].Code)
		result[i].Version = strings.TrimSpace(result[i].Version)
		expected, ok := s.requiredAgreements[result[i].Code]
		if !ok || expected != result[i].Version || result[i].AcceptedAt.IsZero() || result[i].AcceptedAt.After(now.Add(5*time.Minute)) {
			return nil, oops.Errorf("agreement %q is missing, outdated, or has an invalid acceptance time", result[i].Code)
		}
		if _, duplicate := seen[result[i].Code]; duplicate {
			return nil, oops.Errorf("agreement %q is duplicated", result[i].Code)
		}
		seen[result[i].Code] = struct{}{}
		result[i].AcceptedAt = result[i].AcceptedAt.UTC()
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Code == result[j].Code {
			return result[i].Version < result[j].Version
		}
		return result[i].Code < result[j].Code
	})
	return result, nil
}

func (s *UserService) signCreationProof(userID uuid.UUID, registrationID string, version int64) (string, error) {
	return s.userProofs.Sign(security.ProofClaims{
		Audience:       "auth-service",
		Purpose:        "complete_registration",
		RegistrationID: registrationID,
		UserID:         userID.String(),
		UserVersion:    version,
		Nonce:          uuid.NewString(),
	}, s.proofTTL)
}
