package development

import (
	"context"
	"testing"
	"time"

	applicationsession "github.com/Medikong/services/services/auth-service/internal/application/session"
)

func TestIssueBulkTokensCreatesFreshPrincipalAndSessionPerToken(t *testing.T) {
	fixtures := &fixtureRepositoryFake{}
	issuer := &sessionIssuerFake{expiresAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)}
	service := NewService(bulkTransactorFake{fixtures: fixtures}, nil, nil, nil, issuer)

	result, err := service.IssueBulkTokens(context.Background(), BulkTokenInput{Count: 3})
	if err != nil {
		t.Fatalf("issue bulk tokens: %v", err)
	}
	if len(result.Tokens) != 3 || len(fixtures.principals) != 3 || issuer.calls != 3 {
		t.Fatalf("bulk result=%d principals=%d sessions=%d", len(result.Tokens), len(fixtures.principals), issuer.calls)
	}
	seen := map[string]struct{}{}
	for _, token := range result.Tokens {
		if token.UserID == "" || token.SessionID == "" || token.AccessToken == "" || token.AccessTokenExpiresAt.IsZero() {
			t.Fatalf("incomplete token result: %#v", token)
		}
		if _, duplicate := seen[token.UserID]; duplicate {
			t.Fatalf("duplicate user id: %s", token.UserID)
		}
		seen[token.UserID] = struct{}{}
	}
	for _, input := range issuer.inputs {
		if input.AccessTTLOverride != 24*time.Hour || input.SessionTTLOverride != 24*time.Hour+bulkSessionExpiryBuffer {
			t.Fatalf("default TTL overrides = %s/%s", input.AccessTTLOverride, input.SessionTTLOverride)
		}
	}
}

func TestIssueBulkTokensRejectsInvalidCount(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil)
	for _, count := range []int{0, maxBulkTokenCount + 1} {
		if _, err := service.IssueBulkTokens(context.Background(), BulkTokenInput{Count: count}); err == nil {
			t.Fatalf("count %d unexpectedly succeeded", count)
		}
	}
}

func TestIssueBulkTokensUsesRequestedTTL(t *testing.T) {
	fixtures := &fixtureRepositoryFake{}
	issuer := &sessionIssuerFake{expiresAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)}
	service := NewService(bulkTransactorFake{fixtures: fixtures}, nil, nil, nil, issuer)

	if _, err := service.IssueBulkTokens(context.Background(), BulkTokenInput{Count: 1, TTLSeconds: 7200}); err != nil {
		t.Fatalf("issue tokens with requested TTL: %v", err)
	}
	if len(issuer.inputs) != 1 || issuer.inputs[0].AccessTTLOverride != 2*time.Hour || issuer.inputs[0].SessionTTLOverride != 2*time.Hour+bulkSessionExpiryBuffer {
		t.Fatalf("TTL overrides = %#v", issuer.inputs)
	}
}

func TestIssueBulkTokensRejectsInvalidTTL(t *testing.T) {
	service := NewService(nil, nil, nil, nil, nil)
	for _, ttlSeconds := range []int64{-1, minimumBulkTTLSeconds - 1, maximumBulkTTLSeconds + 1} {
		if _, err := service.IssueBulkTokens(context.Background(), BulkTokenInput{Count: 1, TTLSeconds: ttlSeconds}); err == nil {
			t.Fatalf("TTL %d unexpectedly succeeded", ttlSeconds)
		}
	}
}

func TestIssueBulkTokensAcceptsTenThousand(t *testing.T) {
	fixtures := &fixtureRepositoryFake{}
	issuer := &sessionIssuerFake{expiresAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)}
	service := NewService(bulkTransactorFake{fixtures: fixtures}, nil, nil, nil, issuer)

	result, err := service.IssueBulkTokens(context.Background(), BulkTokenInput{Count: 10000})
	if err != nil {
		t.Fatalf("issue maximum bulk tokens: %v", err)
	}
	if len(result.Tokens) != 10000 || len(fixtures.principals) != 10000 || issuer.calls != 10000 {
		t.Fatalf("bulk result=%d principals=%d sessions=%d", len(result.Tokens), len(fixtures.principals), issuer.calls)
	}
}

type bulkTransactorFake struct {
	fixtures FixtureRepository
}

func (f bulkTransactorFake) WithinTransaction(ctx context.Context, run func(TxRepositories) error) error {
	return run(TxRepositories{Fixtures: f.fixtures})
}

type fixtureRepositoryFake struct {
	principals []PrincipalInput
}

func (f *fixtureRepositoryFake) CreatePrincipalsBulk(_ context.Context, inputs []PrincipalInput) error {
	f.principals = append(f.principals, inputs...)
	return nil
}

func (f *fixtureRepositoryFake) SessionBulkRepositories() applicationsession.BulkTxRepositories {
	return applicationsession.BulkTxRepositories{}
}

type sessionIssuerFake struct {
	calls     int
	expiresAt time.Time
	inputs    []applicationsession.IssueInput
}

func (f *sessionIssuerFake) IssueBulkTx(_ context.Context, _ applicationsession.BulkTxRepositories, inputs []applicationsession.IssueInput) ([]applicationsession.Issued, error) {
	f.calls += len(inputs)
	f.inputs = append(f.inputs, inputs...)
	issued := make([]applicationsession.Issued, len(inputs))
	for index, input := range inputs {
		issued[index] = applicationsession.Issued{TokenSet: applicationsession.TokenSet{
			SessionID: input.UserID.String() + "-session", UserID: input.UserID.String(),
			AccessToken: input.UserID.String() + "-token", AccessTokenExpiresAt: f.expiresAt,
		}}
	}
	return issued, nil
}
