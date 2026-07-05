package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Medikong/services/packages/go-platform/database"
	"github.com/Medikong/services/services/user-service/internal/model"
	"github.com/Medikong/services/services/user-service/internal/repository"
)

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	db, err := database.OpenPostgres(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	store := New(db)
	if err := store.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	return database.RunMigrations(ctx, s.db, migrations)
}

func (s *Store) Ensure(ctx context.Context, userID string) (model.User, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (user_id, real_name, nickname, profile_icon, status)
		VALUES ($1, $2, $1, '', 'active')
		ON CONFLICT (user_id) DO NOTHING`, userID, userID)
	if err != nil {
		return model.User{}, err
	}
	return s.Get(ctx, userID)
}

func (s *Store) Get(ctx context.Context, userID string) (model.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT user_id, real_name, nickname, profile_icon, status FROM users WHERE user_id = $1`, userID)
	var user model.User
	if err := row.Scan(&user.UserID, &user.RealName, &user.Nickname, &user.ProfileIcon, &user.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, repository.ErrUserNotFound
		}
		return model.User{}, err
	}
	return user, nil
}

func (s *Store) UpdateProfile(ctx context.Context, userID string, update model.ProfileUpdate) (model.User, error) {
	current, err := s.Get(ctx, userID)
	if err != nil {
		return model.User{}, err
	}
	realName := current.RealName
	nickname := current.Nickname
	profileIcon := current.ProfileIcon
	if update.RealName != nil {
		realName = strings.TrimSpace(*update.RealName)
	}
	if update.Nickname != nil {
		nickname = strings.TrimSpace(*update.Nickname)
	}
	if update.ProfileIcon != nil {
		profileIcon = strings.TrimSpace(*update.ProfileIcon)
	}
	row := s.db.QueryRowContext(ctx, `
		UPDATE users
		SET real_name = $2, nickname = $3, profile_icon = $4, updated_at = now()
		WHERE user_id = $1
		RETURNING user_id, real_name, nickname, profile_icon, status`, userID, realName, nickname, profileIcon)
	var user model.User
	if err := row.Scan(&user.UserID, &user.RealName, &user.Nickname, &user.ProfileIcon, &user.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, repository.ErrUserNotFound
		}
		return model.User{}, err
	}
	return user, nil
}

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS users (
		user_id TEXT PRIMARY KEY,
		real_name TEXT NOT NULL,
		nickname TEXT NOT NULL,
		profile_icon TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
}
