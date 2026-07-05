package memory

import (
	"context"
	"sync"

	"github.com/Medikong/services/services/user-service/internal/model"
	"github.com/Medikong/services/services/user-service/internal/repository"
)

type Store struct {
	mu    sync.Mutex
	users map[string]model.User
}

func New() *Store {
	return &Store{users: map[string]model.User{}}
}

func (s *Store) Ensure(_ context.Context, userID string) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user, ok := s.users[userID]; ok {
		return user, nil
	}
	user := model.User{
		UserID:      userID,
		RealName:    userID,
		Nickname:    userID,
		ProfileIcon: "",
		Status:      "active",
	}
	s.users[userID] = user
	return user, nil
}

func (s *Store) UpdateProfile(_ context.Context, userID string, update model.ProfileUpdate) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return model.User{}, repository.ErrUserNotFound
	}
	if update.RealName != nil {
		user.RealName = *update.RealName
	}
	if update.Nickname != nil {
		user.Nickname = *update.Nickname
	}
	if update.ProfileIcon != nil {
		user.ProfileIcon = *update.ProfileIcon
	}
	s.users[userID] = user
	return user, nil
}

func (s *Store) Get(_ context.Context, userID string) (model.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return model.User{}, repository.ErrUserNotFound
	}
	return user, nil
}
