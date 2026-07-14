package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"

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

type UserServiceConfig struct {
	RequiredAgreements map[string]string
	IdempotencyTTL     time.Duration
	ProofTTL           time.Duration
	Now                func() time.Time
}

type UserService struct {
	repository         *UserRepository
	sealer             security.Sealer
	authProofs         security.Verifier
	mediaProofs        security.Verifier
	userProofs         security.Signer
	requiredAgreements map[string]string
	idempotencyTTL     time.Duration
	proofTTL           time.Duration
	now                func() time.Time
}

func NewUserService(
	repository *UserRepository,
	sealer security.Sealer,
	authProofs security.Verifier,
	mediaProofs security.Verifier,
	userProofs security.Signer,
	cfg UserServiceConfig,
) (*UserService, error) {
	if repository == nil || len(cfg.RequiredAgreements) == 0 || cfg.IdempotencyTTL <= 0 || cfg.ProofTTL <= 0 {
		return nil, oops.In("user_service").Code("user.config_invalid").
			New("repository, required agreements, idempotency TTL, and proof TTL are required")
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
			return nil, oops.In("user_service").Code("user.config_invalid").
				New("required agreement code and version cannot be empty")
		}
		required[code] = version
	}
	return &UserService{
		repository:         repository,
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

func newIdempotencyRecord(operation, scopeID, key string, requestHash []byte, now time.Time, ttl time.Duration) IdempotencyRecord {
	return IdempotencyRecord{
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
	return "sha256:" + hex.EncodeToString(sum[:])
}

func hashRequest(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, oops.In("user_service").Code("user.request_hash_failed").Wrap(err)
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}

func validateIdempotencyKey(value string) error {
	if !idempotencyKeyPattern.MatchString(strings.TrimSpace(value)) {
		return oops.New("Idempotency-Key must contain 1 to 128 safe characters")
	}
	return nil
}

func commit(ctx context.Context, tx pgx.Tx) error {
	if err := tx.Commit(ctx); err != nil {
		return oops.In("user_service").Code("user.transaction_commit_failed").Wrap(err)
	}
	return nil
}

func serviceOperationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return oops.In("user_service").With("operation", operation).Wrap(err)
}
