package session

import (
	"context"
	"runtime"
	"sync"
	"time"

	domainsession "github.com/Medikong/services/services/auth-service/internal/domain/session"
	"github.com/google/uuid"
)

// IssueBulkTx issues access-only sessions and persists them through one bulk repository call.
func (s *Service) IssueBulkTx(ctx context.Context, repositories BulkTxRepositories, inputs []IssueInput) ([]Issued, error) {
	if len(inputs) == 0 {
		return nil, invalid("AUTH_INPUT_INVALID", "발급할 Session이 필요합니다.")
	}
	if repositories.Sessions == nil || repositories.UserAuthState == nil {
		return nil, unavailable(nil)
	}

	userIDs := make([]uuid.UUID, len(inputs))
	seenUsers := make(map[uuid.UUID]struct{}, len(inputs))
	for index, input := range inputs {
		if input.UserID == uuid.Nil || input.IdentityID == uuid.Nil || input.IdentityLink == uuid.Nil {
			return nil, invalid("AUTH_INPUT_INVALID", "Session 식별자가 올바르지 않습니다.")
		}
		if _, duplicate := seenUsers[input.UserID]; duplicate {
			return nil, invalid("AUTH_INPUT_INVALID", "중복된 사용자는 발급할 수 없습니다.")
		}
		channel := domainsession.Channel(input.Channel)
		if channel != domainsession.ChannelWeb && channel != domainsession.ChannelIOS && channel != domainsession.ChannelAndroid {
			return nil, invalid("AUTH_INPUT_INVALID", "클라이언트 채널이 올바르지 않습니다.")
		}
		seenUsers[input.UserID] = struct{}{}
		userIDs[index] = input.UserID
	}
	activeUsers, err := repositories.UserAuthState.FindActiveForUpdate(ctx, userIDs)
	if err != nil {
		return nil, unavailable(err)
	}
	for _, userID := range userIDs {
		if _, active := activeUsers[userID]; !active {
			return nil, forbidden("AUTH_USER_RESTRICTED", "현재 사용자 상태에서는 인증을 완료할 수 없습니다.")
		}
	}

	issued := make([]Issued, len(inputs))
	sessions := make([]domainsession.Session, len(inputs))
	issueContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	workerCount := min(len(inputs), runtime.GOMAXPROCS(0))
	var workers sync.WaitGroup
	var issueErr error
	var issueErrOnce sync.Once
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if issueContext.Err() != nil {
					continue
				}
				currentIssued, currentSession, err := s.issueBulkAccessSession(inputs[index])
				if err != nil {
					issueErrOnce.Do(func() {
						issueErr = err
						cancel()
					})
					continue
				}
				issued[index], sessions[index] = currentIssued, currentSession
			}
		}()
	}

produce:
	for index := range inputs {
		select {
		case jobs <- index:
		case <-issueContext.Done():
			break produce
		}
	}
	close(jobs)
	workers.Wait()
	if issueErr != nil {
		return nil, issueErr
	}
	if err := ctx.Err(); err != nil {
		return nil, unavailable(err)
	}
	if err := repositories.Sessions.CreateAccessSessionsBulk(ctx, sessions); err != nil {
		return nil, unavailable(err)
	}
	return issued, nil
}

func (s *Service) issueBulkAccessSession(input IssueInput) (Issued, domainsession.Session, error) {
	channel := domainsession.Channel(input.Channel)
	sessionTTL := s.config.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	if input.SessionTTLOverride > 0 {
		sessionTTL = input.SessionTTLOverride
	}
	accessTTL := s.config.AccessTTL
	if input.AccessTTLOverride > 0 {
		accessTTL = input.AccessTTLOverride
	}

	issuedAt := s.clock.Now().UTC()
	sessionExpiresAt := issuedAt.Add(sessionTTL)
	sessionID := uuid.New()
	accessToken, accessExpiresAt, err := s.cryptography.SignAccessToken(input.UserID, sessionID, accessTTL)
	if err != nil {
		return Issued{}, domainsession.Session{}, unavailable(err)
	}
	if sessionExpiresAt.Before(accessExpiresAt) {
		return Issued{}, domainsession.Session{}, unavailable(nil)
	}
	current := domainsession.Session{
		ID: sessionID, UserID: input.UserID, IdentityID: input.IdentityID, IdentityLink: input.IdentityLink,
		Method: input.Method, Channel: channel, RememberMe: false, ExpiresAt: sessionExpiresAt,
	}
	return Issued{
		TokenSet: TokenSet{
			SessionID: sessionID.String(), UserID: input.UserID.String(), Channel: string(channel),
			AccessToken: accessToken, AccessTokenExpiresAt: accessExpiresAt, SessionExpiresAt: sessionExpiresAt,
		},
		ExpiresAt: sessionExpiresAt,
	}, current, nil
}
