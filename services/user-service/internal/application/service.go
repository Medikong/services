package application

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/user-service/internal/domain/user"
	"github.com/Medikong/services/services/user-service/internal/security"
)

const (
	operationCreateUser   = "create_user"
	operationProfile      = "update_own_profile"
	operationProfileImage = "update_own_profile_image"
	operationStatus       = "change_user_account_status"
)

var (
	idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	reasonCodePattern     = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
)

type Config struct {
	RequiredAgreements map[string]string
	IdempotencyTTL     time.Duration
	ProofTTL           time.Duration
	Now                func() time.Time
}

type Service struct {
	store              *user.Store
	sealer             security.Sealer
	authProofs         security.Verifier
	mediaProofs        security.Verifier
	userProofs         security.Signer
	requiredAgreements map[string]string
	idempotencyTTL     time.Duration
	proofTTL           time.Duration
	now                func() time.Time
}

type CreateUserInput struct {
	RegistrationID              string
	RegistrationCompletionProof string
	PrivateName                 string
	Nickname                    string
	Introduction                *string
	RequiredAgreements          []user.AgreementAcceptance
	IdempotencyKey              string
}

type CreateUserOutput struct {
	UserID            uuid.UUID
	UserVersion       int64
	CreatedAt         time.Time
	UserCreationProof string
	Replayed          bool
}

type UpdateProfileInput struct {
	UserID              uuid.UUID
	ExpectedUserVersion int64
	Patch               user.ProfilePatch
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

type ChangeStatusInput struct {
	UserID              uuid.UUID
	TargetStatus        string
	ReasonCode          string
	ExpectedUserVersion int64
	ChangedBy           string
	IdempotencyKey      string
}

type ChangeStatusOutput struct {
	StatusChangeID        uuid.UUID
	UserID                uuid.UUID
	AccountStatus         user.AccountStatus
	UserVersion           int64
	ChangedAt             time.Time
	UserStatusChangeProof string
	Replayed              bool
}

func NewService(
	store *user.Store,
	sealer security.Sealer,
	authProofs security.Verifier,
	mediaProofs security.Verifier,
	userProofs security.Signer,
	cfg Config,
) (*Service, error) {
	if store == nil || len(cfg.RequiredAgreements) == 0 || cfg.IdempotencyTTL <= 0 || cfg.ProofTTL <= 0 {
		return nil, errors.New("store, required agreements, idempotency TTL, and proof TTL are required")
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	required := make(map[string]string, len(cfg.RequiredAgreements))
	for code, version := range cfg.RequiredAgreements {
		code = strings.TrimSpace(code)
		version = strings.TrimSpace(version)
		if code == "" || version == "" {
			return nil, errors.New("required agreement code and version cannot be empty")
		}
		required[code] = version
	}
	return &Service{
		store:              store,
		sealer:             sealer,
		authProofs:         authProofs,
		mediaProofs:        mediaProofs,
		userProofs:         userProofs,
		requiredAgreements: required,
		idempotencyTTL:     cfg.IdempotencyTTL,
		proofTTL:           cfg.ProofTTL,
		now:                func() time.Time { return now().UTC() },
	}, nil
}

func (s *Service) CreateUser(ctx context.Context, input CreateUserInput) (CreateUserOutput, error) {
	registrationID, err := user.NormalizeRegistrationID(input.RegistrationID)
	if err != nil {
		return CreateUserOutput{}, inputProblem(err)
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return CreateUserOutput{}, inputProblem(err)
	}
	privateName, err := user.NormalizePrivateName(input.PrivateName)
	if err != nil {
		return CreateUserOutput{}, profileProblem(err)
	}
	nickname, err := user.NormalizeNickname(input.Nickname)
	if err != nil {
		return CreateUserOutput{}, profileProblem(err)
	}
	introduction, err := user.NormalizeIntroduction(input.Introduction)
	if err != nil {
		return CreateUserOutput{}, profileProblem(err)
	}
	claims, err := s.authProofs.Verify(input.RegistrationCompletionProof, "user-service", "create_user")
	if err != nil || claims.RegistrationID != registrationID || !claims.EmailVerified || !claims.PhoneVerified {
		return CreateUserOutput{}, NewProblem(http.StatusForbidden, "USER_REGISTRATION_PROOF_INVALID", "가입 검증 증거가 유효하지 않습니다.", err)
	}
	agreements, err := s.validateAgreements(input.RequiredAgreements)
	if err != nil {
		return CreateUserOutput{}, NewProblem(http.StatusUnprocessableEntity, "USER_REQUIRED_AGREEMENT_INVALID", "필수 동의 항목이나 버전이 올바르지 않습니다.", err)
	}

	requestHash, err := hashRequest(struct {
		RegistrationID string                     `json:"registrationId"`
		PrivateName    string                     `json:"privateName"`
		Nickname       string                     `json:"nickname"`
		Introduction   *string                    `json:"introduction"`
		Agreements     []user.AgreementAcceptance `json:"agreements"`
	}{registrationID, privateName, nickname, introduction, agreements})
	if err != nil {
		return CreateUserOutput{}, internalProblem(err)
	}
	now := s.now()
	record := newIdempotencyRecord(operationCreateUser, registrationID, registrationID, requestHash, now, s.idempotencyTTL)
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return CreateUserOutput{}, internalProblem(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, replayed, err := s.store.ClaimIdempotency(ctx, tx, record)
	if errors.Is(err, user.ErrIdempotencyConflict) {
		return CreateUserOutput{}, NewProblem(http.StatusConflict, "USER_REGISTRATION_CONFLICT", "같은 registrationId에 다른 가입 요청이 이미 처리되었습니다.", err)
	}
	if err != nil {
		return CreateUserOutput{}, internalProblem(err)
	}
	if replayed {
		id, parseErr := uuid.Parse(*claimed.ResultID)
		if parseErr != nil {
			return CreateUserOutput{}, internalProblem(parseErr)
		}
		current, queryErr := s.store.GetByIDTx(ctx, tx, id)
		if queryErr != nil {
			return CreateUserOutput{}, internalProblem(queryErr)
		}
		_ = tx.Rollback(ctx)
		proof, proofErr := s.signCreationProof(current.ID, registrationID, *claimed.ResultVersion)
		if proofErr != nil {
			return CreateUserOutput{}, internalProblem(proofErr)
		}
		return CreateUserOutput{UserID: current.ID, UserVersion: *claimed.ResultVersion, CreatedAt: current.CreatedAt, UserCreationProof: proof, Replayed: true}, nil
	}
	ciphertext, err := s.sealer.Seal(privateName)
	if err != nil {
		return CreateUserOutput{}, internalProblem(err)
	}
	created, err := s.store.Create(ctx, tx, user.CreateInput{
		UserID:                uuid.New(),
		RegistrationID:        registrationID,
		PrivateNameCiphertext: ciphertext,
		Nickname:              nickname,
		Introduction:          introduction,
		Agreements:            agreements,
		Now:                   now,
	})
	if err != nil {
		return CreateUserOutput{}, internalProblem(err)
	}
	if err := s.store.CompleteIdempotency(ctx, tx, record, "user", created.ID.String(), created.Version); err != nil {
		return CreateUserOutput{}, internalProblem(err)
	}
	if err := commit(ctx, tx); err != nil {
		return CreateUserOutput{}, internalProblem(err)
	}
	proof, err := s.signCreationProof(created.ID, registrationID, created.Version)
	if err != nil {
		return CreateUserOutput{}, internalProblem(err)
	}
	return CreateUserOutput{UserID: created.ID, UserVersion: created.Version, CreatedAt: created.CreatedAt, UserCreationProof: proof}, nil
}

func (s *Service) GetOwnProfile(ctx context.Context, id uuid.UUID) (user.User, error) {
	current, err := s.store.GetByID(ctx, id)
	if errors.Is(err, user.ErrNotFound) {
		return user.User{}, NewProblem(http.StatusNotFound, "USER_NOT_FOUND", "사용자를 찾을 수 없습니다.", err)
	}
	if err != nil {
		return user.User{}, internalProblem(err)
	}
	return current, nil
}

func (s *Service) GetAccountStatus(ctx context.Context, id uuid.UUID) (user.User, error) {
	return s.GetOwnProfile(ctx, id)
}

func (s *Service) UpdateOwnProfile(ctx context.Context, input UpdateProfileInput) (UpdateProfileOutput, error) {
	if input.ExpectedUserVersion < 1 {
		return UpdateProfileOutput{}, inputProblem(errors.New("expectedUserVersion must be at least 1"))
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return UpdateProfileOutput{}, inputProblem(err)
	}
	if !input.Patch.NicknameSet && !input.Patch.IntroductionSet {
		return UpdateProfileOutput{}, profileProblem(errors.New("at least one profile field is required"))
	}
	patch := input.Patch
	if patch.NicknameSet {
		if strings.TrimSpace(patch.Nickname) == "" {
			return UpdateProfileOutput{}, profileProblem(errors.New("nickname cannot be null or empty"))
		}
		nickname, err := user.NormalizeNickname(patch.Nickname)
		if err != nil {
			return UpdateProfileOutput{}, profileProblem(err)
		}
		patch.Nickname = nickname
	}
	if patch.IntroductionSet {
		introduction, err := user.NormalizeIntroduction(patch.Introduction)
		if err != nil {
			return UpdateProfileOutput{}, profileProblem(err)
		}
		patch.Introduction = introduction
	}
	requestHash, err := hashRequest(struct {
		Expected int64             `json:"expectedUserVersion"`
		Patch    user.ProfilePatch `json:"patch"`
	}{input.ExpectedUserVersion, patch})
	if err != nil {
		return UpdateProfileOutput{}, internalProblem(err)
	}
	now := s.now()
	record := newIdempotencyRecord(operationProfile, input.UserID.String(), input.IdempotencyKey, requestHash, now, s.idempotencyTTL)
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return UpdateProfileOutput{}, internalProblem(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, replayed, err := s.store.ClaimIdempotency(ctx, tx, record)
	if err != nil {
		return UpdateProfileOutput{}, mutationProblem(err)
	}
	if replayed {
		_ = tx.Rollback(ctx)
		return UpdateProfileOutput{UserID: input.UserID, UserVersion: *claimed.ResultVersion, ChangedFields: patch.ChangedFields(), UpdatedAt: claimed.CreatedAt, Replayed: true}, nil
	}
	result, err := s.store.UpdateProfile(ctx, tx, input.UserID, input.ExpectedUserVersion, patch, now)
	if err != nil {
		return UpdateProfileOutput{}, mutationProblem(err)
	}
	if err := s.store.CompleteIdempotency(ctx, tx, record, "user", result.UserID.String(), result.Version); err != nil {
		return UpdateProfileOutput{}, internalProblem(err)
	}
	if err := commit(ctx, tx); err != nil {
		return UpdateProfileOutput{}, internalProblem(err)
	}
	return UpdateProfileOutput{UserID: result.UserID, UserVersion: result.Version, ChangedFields: patch.ChangedFields(), UpdatedAt: result.UpdatedAt}, nil
}

func (s *Service) UpdateOwnProfileImage(ctx context.Context, input UpdateProfileImageInput) (UpdateProfileImageOutput, error) {
	if input.ExpectedUserVersion < 1 {
		return UpdateProfileImageOutput{}, inputProblem(errors.New("expectedUserVersion must be at least 1"))
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return UpdateProfileImageOutput{}, inputProblem(err)
	}
	assetID, err := user.NormalizeMediaAssetID(input.MediaAssetID)
	if err != nil {
		return UpdateProfileImageOutput{}, inputProblem(err)
	}
	claims, err := s.mediaProofs.Verify(input.MediaAssetProof, "user-service", "user_profile")
	if err != nil || claims.UserID != input.UserID.String() || claims.MediaAssetID != assetID || !claims.ScanCompleted {
		return UpdateProfileImageOutput{}, NewProblem(http.StatusForbidden, "USER_PROFILE_MEDIA_PROOF_INVALID", "프로필 이미지 자산 증거가 유효하지 않습니다.", err)
	}
	requestHash, err := hashRequest(struct {
		AssetID  string `json:"mediaAssetId"`
		Expected int64  `json:"expectedUserVersion"`
	}{assetID, input.ExpectedUserVersion})
	if err != nil {
		return UpdateProfileImageOutput{}, internalProblem(err)
	}
	now := s.now()
	record := newIdempotencyRecord(operationProfileImage, input.UserID.String(), input.IdempotencyKey, requestHash, now, s.idempotencyTTL)
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return UpdateProfileImageOutput{}, internalProblem(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, replayed, err := s.store.ClaimIdempotency(ctx, tx, record)
	if err != nil {
		return UpdateProfileImageOutput{}, mutationProblem(err)
	}
	if replayed {
		_ = tx.Rollback(ctx)
		return UpdateProfileImageOutput{UserID: input.UserID, ProfileMediaAssetID: assetID, UserVersion: *claimed.ResultVersion, UpdatedAt: claimed.CreatedAt, Replayed: true}, nil
	}
	result, err := s.store.UpdateProfileImage(ctx, tx, input.UserID, assetID, input.ExpectedUserVersion, now)
	if err != nil {
		return UpdateProfileImageOutput{}, mutationProblem(err)
	}
	if err := s.store.CompleteIdempotency(ctx, tx, record, "user", result.UserID.String(), result.Version); err != nil {
		return UpdateProfileImageOutput{}, internalProblem(err)
	}
	if err := commit(ctx, tx); err != nil {
		return UpdateProfileImageOutput{}, internalProblem(err)
	}
	return UpdateProfileImageOutput{UserID: result.UserID, ProfileMediaAssetID: assetID, UserVersion: result.Version, UpdatedAt: result.UpdatedAt}, nil
}

func (s *Service) ChangeUserAccountStatus(ctx context.Context, input ChangeStatusInput) (ChangeStatusOutput, error) {
	if input.ExpectedUserVersion < 1 {
		return ChangeStatusOutput{}, inputProblem(errors.New("expectedUserVersion must be at least 1"))
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		return ChangeStatusOutput{}, inputProblem(err)
	}
	target, err := user.ParseStatus(input.TargetStatus)
	if err != nil {
		return ChangeStatusOutput{}, NewProblem(http.StatusConflict, "USER_ACCOUNT_STATUS_TRANSITION_INVALID", "허용되지 않은 계정 상태 전이입니다.", err)
	}
	reasonCode := strings.TrimSpace(input.ReasonCode)
	if !reasonCodePattern.MatchString(reasonCode) || strings.TrimSpace(input.ChangedBy) == "" || len(input.ChangedBy) > 128 {
		return ChangeStatusOutput{}, inputProblem(errors.New("reasonCode or operator principal is invalid"))
	}
	requestHash, err := hashRequest(struct {
		Target   user.AccountStatus `json:"targetStatus"`
		Reason   string             `json:"reasonCode"`
		Expected int64              `json:"expectedUserVersion"`
	}{target, reasonCode, input.ExpectedUserVersion})
	if err != nil {
		return ChangeStatusOutput{}, internalProblem(err)
	}
	now := s.now()
	record := newIdempotencyRecord(operationStatus, input.UserID.String(), input.IdempotencyKey, requestHash, now, s.idempotencyTTL)
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return ChangeStatusOutput{}, internalProblem(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	claimed, replayed, err := s.store.ClaimIdempotency(ctx, tx, record)
	if err != nil {
		return ChangeStatusOutput{}, statusProblem(err)
	}
	if replayed {
		changeID, parseErr := uuid.Parse(*claimed.ResultID)
		if parseErr != nil {
			return ChangeStatusOutput{}, internalProblem(parseErr)
		}
		history, queryErr := s.store.GetStatusHistory(ctx, tx, changeID)
		if queryErr != nil {
			return ChangeStatusOutput{}, internalProblem(queryErr)
		}
		_ = tx.Rollback(ctx)
		proof, proofErr := s.signStatusProof(history.ID, history.UserID, history.ChangedStatus, *claimed.ResultVersion, history.ChangedAt)
		if proofErr != nil {
			return ChangeStatusOutput{}, internalProblem(proofErr)
		}
		return ChangeStatusOutput{StatusChangeID: history.ID, UserID: history.UserID, AccountStatus: history.ChangedStatus, UserVersion: *claimed.ResultVersion, ChangedAt: history.ChangedAt, UserStatusChangeProof: proof, Replayed: true}, nil
	}
	result, err := s.store.ChangeStatus(ctx, tx, input.UserID, target, input.ExpectedUserVersion, uuid.New(), reasonCode, input.ChangedBy, now)
	if err != nil {
		return ChangeStatusOutput{}, statusProblem(err)
	}
	if err := s.store.CompleteIdempotency(ctx, tx, record, "status_change", result.StatusChangeID.String(), result.Version); err != nil {
		return ChangeStatusOutput{}, internalProblem(err)
	}
	if err := commit(ctx, tx); err != nil {
		return ChangeStatusOutput{}, internalProblem(err)
	}
	proof, err := s.signStatusProof(result.StatusChangeID, result.UserID, result.ChangedStatus, result.Version, result.ChangedAt)
	if err != nil {
		return ChangeStatusOutput{}, internalProblem(err)
	}
	return ChangeStatusOutput{StatusChangeID: result.StatusChangeID, UserID: result.UserID, AccountStatus: result.ChangedStatus, UserVersion: result.Version, ChangedAt: result.ChangedAt, UserStatusChangeProof: proof}, nil
}

func (s *Service) validateAgreements(input []user.AgreementAcceptance) ([]user.AgreementAcceptance, error) {
	if len(input) != len(s.requiredAgreements) {
		return nil, errors.New("required agreement count does not match")
	}
	seen := make(map[string]struct{}, len(input))
	now := s.now()
	result := append([]user.AgreementAcceptance(nil), input...)
	for i := range result {
		result[i].Code = strings.TrimSpace(result[i].Code)
		result[i].Version = strings.TrimSpace(result[i].Version)
		expected, ok := s.requiredAgreements[result[i].Code]
		if !ok || expected != result[i].Version || result[i].AcceptedAt.IsZero() || result[i].AcceptedAt.After(now.Add(5*time.Minute)) {
			return nil, fmt.Errorf("agreement %q is missing, outdated, or has an invalid acceptance time", result[i].Code)
		}
		if _, duplicate := seen[result[i].Code]; duplicate {
			return nil, fmt.Errorf("agreement %q is duplicated", result[i].Code)
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

func (s *Service) signCreationProof(userID uuid.UUID, registrationID string, version int64) (string, error) {
	return s.userProofs.Sign(security.ProofClaims{
		Audience:       "auth-service",
		Purpose:        "complete_registration",
		RegistrationID: registrationID,
		UserID:         userID.String(),
		UserVersion:    version,
		Nonce:          uuid.NewString(),
	}, s.proofTTL)
}

func (s *Service) signStatusProof(changeID, userID uuid.UUID, status user.AccountStatus, version int64, changedAt time.Time) (string, error) {
	return s.userProofs.Sign(security.ProofClaims{
		Audience:       "auth-service",
		Purpose:        "apply_user_status",
		StatusChangeID: changeID.String(),
		UserID:         userID.String(),
		AccountStatus:  string(status),
		UserVersion:    version,
		ChangedAt:      changedAt.UTC().Unix(),
		Nonce:          uuid.NewString(),
	}, s.proofTTL)
}

func newIdempotencyRecord(operation, scopeID, key string, requestHash []byte, now time.Time, ttl time.Duration) user.IdempotencyRecord {
	return user.IdempotencyRecord{
		Operation:   operation,
		ScopeID:     operationScope(scopeID),
		Key:         key,
		RequestHash: requestHash,
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
}

func operationScope(value string) string {
	if len(value) <= 128 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func hashRequest(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}

func validateIdempotencyKey(value string) error {
	if !idempotencyKeyPattern.MatchString(strings.TrimSpace(value)) {
		return errors.New("Idempotency-Key must contain 1 to 128 safe characters")
	}
	return nil
}

func commit(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return oops.In("user_application").Code("user.transaction_commit_failed").Wrap(err)
	}
	return nil
}

func inputProblem(err error) error {
	return NewProblem(http.StatusUnprocessableEntity, "USER_INPUT_INVALID", "요청 값이 올바르지 않습니다.", err)
}

func profileProblem(err error) error {
	return NewProblem(http.StatusUnprocessableEntity, "USER_PROFILE_POLICY_VIOLATION", "프로필 정책을 만족하지 않습니다.", err)
}

func mutationProblem(err error) error {
	switch {
	case errors.Is(err, user.ErrNotFound):
		return NewProblem(http.StatusNotFound, "USER_NOT_FOUND", "사용자를 찾을 수 없습니다.", err)
	case errors.Is(err, user.ErrAccountNotActive):
		return NewProblem(http.StatusConflict, "USER_ACCOUNT_NOT_ACTIVE", "활성 상태의 사용자만 프로필을 변경할 수 있습니다.", err)
	case errors.Is(err, user.ErrVersionConflict):
		return NewProblem(http.StatusConflict, "USER_VERSION_CONFLICT", "사용자 정보가 다른 요청에서 먼저 변경되었습니다.", err)
	case errors.Is(err, user.ErrIdempotencyConflict):
		return NewProblem(http.StatusConflict, "USER_IDEMPOTENCY_CONFLICT", "같은 멱등 키에 다른 요청이 사용되었습니다.", err)
	default:
		return internalProblem(err)
	}
}

func statusProblem(err error) error {
	if errors.Is(err, user.ErrTransitionInvalid) {
		return NewProblem(http.StatusConflict, "USER_ACCOUNT_STATUS_TRANSITION_INVALID", "허용되지 않은 계정 상태 전이입니다.", err)
	}
	return mutationProblem(err)
}

func internalProblem(err error) error {
	return NewProblem(http.StatusInternalServerError, "common.internal", "요청 처리 중 오류가 발생했습니다.", err)
}
