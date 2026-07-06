package user

import (
	"context"
	"sync"
)

type MemoryRepository struct {
	mu    sync.Mutex
	users map[string]User
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{users: map[string]User{}}
}

func (s *MemoryRepository) Ensure(_ context.Context, userID string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user, ok := s.users[userID]; ok {
		return user, nil
	}
	user := User{
		UserID:      userID,
		RealName:    userID,
		Nickname:    userID,
		ProfileIcon: "",
		Status:      "active",
	}
	s.users[userID] = user
	return user, nil
}

func (s *MemoryRepository) UpdateProfile(_ context.Context, userID string, update ProfileUpdate) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return User{}, ErrUserNotFound
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

func (s *MemoryRepository) Get(_ context.Context, userID string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[userID]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return user, nil
}
