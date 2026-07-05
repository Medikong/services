package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/Medikong/services/packages/go-authz/rbac"
	"github.com/Medikong/services/services/auth-service/internal/model"
	"github.com/Medikong/services/services/auth-service/internal/repository"
)

type Store struct {
	mu       sync.Mutex
	byEmail  map[string]model.AccountCredential
	sessions map[string]repository.Session
	next     int
}

func New() *Store {
	return &Store{
		byEmail:  map[string]model.AccountCredential{},
		sessions: map[string]repository.Session{},
	}
}

func (s *Store) CreateEmailAccount(_ context.Context, email string, passwordHash string) (model.AccountCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byEmail[email]; ok {
		return model.AccountCredential{}, repository.ErrAlreadyExists
	}
	s.next++
	account := model.AccountCredential{
		AuthAccountID: fmt.Sprintf("auth-%d", s.next),
		UserID:        fmt.Sprintf("user-%d", s.next),
		Email:         email,
		PasswordHash:  passwordHash,
		Roles:         []string{string(rbac.RoleCustomer)},
	}
	s.byEmail[email] = account
	return account, nil
}

func (s *Store) FindByEmail(_ context.Context, email string) (model.AccountCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.byEmail[email]
	if !ok {
		return model.AccountCredential{}, repository.ErrInvalidCredentials
	}
	return account, nil
}

func (s *Store) CreateSession(_ context.Context, input repository.SessionInput) (repository.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := model.AccountCredential{AuthAccountID: input.AuthAccountID, UserID: input.UserID, Roles: append([]string(nil), input.Roles...)}
	session := repository.Session{
		SessionID:    fmt.Sprintf("session-%d", len(s.sessions)+1),
		AccessToken:  fmt.Sprintf("access-%d", len(s.sessions)+1),
		RefreshToken: fmt.Sprintf("refresh-%d", len(s.sessions)+1),
		Principal:    account,
		AuthMethods:  append([]string(nil), input.AuthMethods...),
	}
	s.sessions[session.AccessToken] = session
	return session, nil
}

func (s *Store) FindSessionByAccessToken(_ context.Context, token string) (repository.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[token]
	if !ok {
		return repository.Session{}, repository.ErrSessionNotFound
	}
	return session, nil
}

func (s *Store) RefreshSession(_ context.Context, refreshToken string) (repository.SessionRotation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for accessToken, session := range s.sessions {
		if session.RefreshToken != refreshToken {
			continue
		}
		delete(s.sessions, accessToken)
		s.next++
		next := s.next
		session.AccessToken = fmt.Sprintf("access-%d", next)
		session.RefreshToken = fmt.Sprintf("refresh-%d", next)
		s.sessions[session.AccessToken] = session
		return repository.SessionRotation{PreviousAccessToken: accessToken, Session: session}, nil
	}
	return repository.SessionRotation{}, repository.ErrSessionNotFound
}

func (s *Store) RevokeSession(_ context.Context, sessionID string) (repository.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for accessToken, session := range s.sessions {
		if session.SessionID == sessionID {
			delete(s.sessions, accessToken)
			return session, nil
		}
	}
	return repository.Session{}, repository.ErrSessionNotFound
}

func (s *Store) RevokeByAccessToken(_ context.Context, token string) (repository.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[token]
	if !ok {
		return repository.Session{}, repository.ErrSessionNotFound
	}
	delete(s.sessions, token)
	return session, nil
}

func (s *Store) IssueTestToken(ctx context.Context, token string, userID string, roles []string) (repository.Session, error) {
	if userID == "" {
		userID = "test-" + token
	}
	return s.CreateSession(ctx, repository.SessionInput{
		AuthAccountID: "test-" + userID,
		UserID:        userID,
		Roles:         roles,
		AuthMethods:   []string{"test_token"},
	})
}
