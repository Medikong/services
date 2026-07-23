package development

import (
	"context"
	"fmt"
	"time"

	"github.com/Medikong/services/services/auth-service/internal/application/failure"
	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
	"github.com/google/uuid"
)

const (
	maxBulkTokenCount       = 10000
	defaultBulkTTLSeconds   = int64(24 * time.Hour / time.Second)
	minimumBulkTTLSeconds   = int64(time.Minute / time.Second)
	maximumBulkTTLSeconds   = int64(24 * time.Hour / time.Second)
	bulkSessionExpiryBuffer = time.Minute
)

type BulkTokenInput struct {
	Count      int
	TTLSeconds int64
}

type BulkToken struct {
	UserID               string    `json:"userId"`
	SessionID            string    `json:"sessionId"`
	AccessToken          string    `json:"accessToken"`
	AccessTokenExpiresAt time.Time `json:"accessTokenExpiresAt"`
}

type BulkTokenOutput struct {
	Tokens []BulkToken
}

func (s *Service) IssueBulkTokens(ctx context.Context, input BulkTokenInput) (BulkTokenOutput, error) {
	if input.Count < 1 || input.Count > maxBulkTokenCount {
		return BulkTokenOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "발급 수량은 1개 이상 10000개 이하여야 합니다.")
	}
	ttlSeconds := input.TTLSeconds
	if ttlSeconds == 0 {
		ttlSeconds = defaultBulkTTLSeconds
	}
	if ttlSeconds < minimumBulkTTLSeconds || ttlSeconds > maximumBulkTTLSeconds {
		return BulkTokenOutput{}, failure.Invalid("AUTH_INPUT_INVALID", "토큰 유효시간은 60초 이상 86400초 이하여야 합니다.")
	}
	if s == nil || s.transactions == nil || s.sessions == nil {
		return BulkTokenOutput{}, unavailable(nil)
	}
	tokenTTL := time.Duration(ttlSeconds) * time.Second

	output := BulkTokenOutput{Tokens: make([]BulkToken, input.Count)}
	err := s.transactions.WithinTransaction(ctx, func(repositories TxRepositories) error {
		if repositories.Fixtures == nil {
			return unavailable(nil)
		}
		principals := make([]PrincipalInput, input.Count)
		issueInputs := make([]applicationsession.IssueInput, input.Count)
		for index := 0; index < input.Count; index++ {
			userID, identityID, linkID := uuid.New(), uuid.New(), uuid.New()
			fixtureID := uuid.NewString()
			principals[index] = PrincipalInput{
				UserID: userID, IdentityID: identityID, LinkID: linkID,
				Email:    fmt.Sprintf("dev-token-%s@example.invalid", fixtureID),
				ChangeID: "development-bulk-token:" + fixtureID,
			}
			issueInputs[index] = applicationsession.IssueInput{
				UserID: userID, IdentityID: identityID, IdentityLink: linkID,
				Method: "registration_verified", Channel: "android",
				AccessTTLOverride: tokenTTL, SessionTTLOverride: tokenTTL + bulkSessionExpiryBuffer,
			}
		}
		if err := repositories.Fixtures.CreatePrincipalsBulk(ctx, principals); err != nil {
			return unavailable(err)
		}
		issued, err := s.sessions.IssueBulkTx(ctx, repositories.Fixtures.SessionBulkRepositories(), issueInputs)
		if err != nil {
			return err
		}
		for index := range issued {
			output.Tokens[index] = BulkToken{
				UserID: issueInputs[index].UserID.String(), SessionID: issued[index].SessionID,
				AccessToken: issued[index].AccessToken, AccessTokenExpiresAt: issued[index].AccessTokenExpiresAt.UTC(),
			}
		}
		return nil
	})
	if err != nil {
		return BulkTokenOutput{}, preserveFailure(err)
	}
	return output, nil
}
