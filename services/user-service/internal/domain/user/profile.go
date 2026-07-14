package user

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/samber/oops"
)

type UpdateProfileInput struct {
	UserID              uuid.UUID
	ExpectedUserVersion int64
	Patch               ProfilePatch
	IdempotencyKey      string
}

type UpdateProfileOutput struct {
	UserID        uuid.UUID
	UserVersion   int64
	ChangedFields []string
	UpdatedAt     time.Time
	Replayed      bool
}

type UpdateProfileImageInput struct {
	UserID              uuid.UUID
	MediaAssetID        string
	MediaAssetProof     string
	ExpectedUserVersion int64
	IdempotencyKey      string
}

type UpdateProfileImageOutput struct {
	UserID              uuid.UUID
	ProfileMediaAssetID string
	UserVersion         int64
	UpdatedAt           time.Time
	Replayed            bool
}

func (s *UserService) GetOwnProfile(ctx context.Context, id uuid.UUID) (User, error) {
	current, err := s.repository.GetByID(ctx, id)
	if err != nil {
		return User{}, serviceOperationError("get_own_profile", err)
	}
	return current, nil
}

func (s *UserService) UpdateOwnProfile(ctx context.Context, input UpdateProfileInput) (UpdateProfileOutput, error) {
	if input.ExpectedUserVersion < 1 {
		return UpdateProfileOutput{}, inputError(oops.New("expectedUserVersion must be at least 1"))
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return UpdateProfileOutput{}, inputError(err)
	}
	if !input.Patch.NicknameSet && !input.Patch.IntroductionSet {
		return UpdateProfileOutput{}, profilePolicyError(oops.New("at least one profile field is required"))
	}
	patch := input.Patch
	if patch.NicknameSet {
		if strings.TrimSpace(patch.Nickname) == "" {
			return UpdateProfileOutput{}, profilePolicyError(oops.New("nickname cannot be null or empty"))
		}
		nickname, err := NormalizeNickname(patch.Nickname)
		if err != nil {
			return UpdateProfileOutput{}, profilePolicyError(err)
		}
		patch.Nickname = nickname
	}
	if patch.IntroductionSet {
		introduction, err := NormalizeIntroduction(patch.Introduction)
		if err != nil {
			return UpdateProfileOutput{}, profilePolicyError(err)
		}
		patch.Introduction = introduction
	}
	requestHash, err := hashRequest(struct {
		Expected int64        `json:"expectedUserVersion"`
		Patch    ProfilePatch `json:"patch"`
	}{input.ExpectedUserVersion, patch})
	if err != nil {
		return UpdateProfileOutput{}, serviceOperationError(operationProfile, err)
	}
	now := s.now()
	record := newIdempotencyRecord(operationProfile, input.UserID.String(), input.IdempotencyKey, requestHash, now, s.idempotencyTTL)
	tx, err := s.repository.Begin(ctx)
	if err != nil {
		return UpdateProfileOutput{}, serviceOperationError(operationProfile, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, replayed, err := s.repository.ClaimIdempotency(ctx, tx, record)
	if err != nil {
		return UpdateProfileOutput{}, serviceOperationError(operationProfile, err)
	}
	if replayed {
		_ = tx.Rollback(ctx)
		return UpdateProfileOutput{UserID: input.UserID, UserVersion: *claimed.ResultVersion, ChangedFields: patch.ChangedFields(), UpdatedAt: claimed.CreatedAt, Replayed: true}, nil
	}
	result, err := s.repository.UpdateProfile(ctx, tx, input.UserID, input.ExpectedUserVersion, patch, now)
	if err != nil {
		return UpdateProfileOutput{}, serviceOperationError(operationProfile, err)
	}
	if err := s.repository.CompleteIdempotency(ctx, tx, record, "user", result.UserID.String(), result.Version); err != nil {
		return UpdateProfileOutput{}, serviceOperationError(operationProfile, err)
	}
	if err := commit(ctx, tx); err != nil {
		return UpdateProfileOutput{}, serviceOperationError(operationProfile, err)
	}
	return UpdateProfileOutput{UserID: result.UserID, UserVersion: result.Version, ChangedFields: patch.ChangedFields(), UpdatedAt: result.UpdatedAt}, nil
}

func (s *UserService) UpdateOwnProfileImage(ctx context.Context, input UpdateProfileImageInput) (UpdateProfileImageOutput, error) {
	if input.ExpectedUserVersion < 1 {
		return UpdateProfileImageOutput{}, inputError(oops.New("expectedUserVersion must be at least 1"))
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return UpdateProfileImageOutput{}, inputError(err)
	}
	assetID, err := NormalizeMediaAssetID(input.MediaAssetID)
	if err != nil {
		return UpdateProfileImageOutput{}, inputError(err)
	}
	claims, err := s.mediaProofs.Verify(input.MediaAssetProof, "user-service", "user_profile")
	if err != nil || claims.UserID != input.UserID.String() || claims.MediaAssetID != assetID || !claims.ScanCompleted {
		return UpdateProfileImageOutput{}, categorizedError(ErrProfileMediaProofInvalid, err)
	}
	requestHash, err := hashRequest(struct {
		AssetID  string `json:"mediaAssetId"`
		Expected int64  `json:"expectedUserVersion"`
	}{assetID, input.ExpectedUserVersion})
	if err != nil {
		return UpdateProfileImageOutput{}, serviceOperationError(operationProfileImage, err)
	}
	now := s.now()
	record := newIdempotencyRecord(operationProfileImage, input.UserID.String(), input.IdempotencyKey, requestHash, now, s.idempotencyTTL)
	tx, err := s.repository.Begin(ctx)
	if err != nil {
		return UpdateProfileImageOutput{}, serviceOperationError(operationProfileImage, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, replayed, err := s.repository.ClaimIdempotency(ctx, tx, record)
	if err != nil {
		return UpdateProfileImageOutput{}, serviceOperationError(operationProfileImage, err)
	}
	if replayed {
		_ = tx.Rollback(ctx)
		return UpdateProfileImageOutput{UserID: input.UserID, ProfileMediaAssetID: assetID, UserVersion: *claimed.ResultVersion, UpdatedAt: claimed.CreatedAt, Replayed: true}, nil
	}
	result, err := s.repository.UpdateProfileImage(ctx, tx, input.UserID, assetID, input.ExpectedUserVersion, now)
	if err != nil {
		return UpdateProfileImageOutput{}, serviceOperationError(operationProfileImage, err)
	}
	if err := s.repository.CompleteIdempotency(ctx, tx, record, "user", result.UserID.String(), result.Version); err != nil {
		return UpdateProfileImageOutput{}, serviceOperationError(operationProfileImage, err)
	}
	if err := commit(ctx, tx); err != nil {
		return UpdateProfileImageOutput{}, serviceOperationError(operationProfileImage, err)
	}
	return UpdateProfileImageOutput{UserID: result.UserID, ProfileMediaAssetID: assetID, UserVersion: result.Version, UpdatedAt: result.UpdatedAt}, nil
}
