package user

import (
	"context"
	"errors"
	"strings"

	"github.com/Medikong/services/packages/go-platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	db *pgxpool.Pool
}

func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func OpenPostgresRepository(ctx context.Context, config database.PostgresConfig) (*PostgresRepository, error) {
	db, err := database.OpenPostgres(ctx, config)
	if err != nil {
		return nil, err
	}
	store := NewPostgresRepository(db)
	if err := store.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresRepository) Migrate(ctx context.Context) error {
	return database.RunMigrations(ctx, s.db, migrations)
}

func (s *PostgresRepository) Ensure(ctx context.Context, userID string) (User, error) {
	_, err := s.db.Exec(ctx, `
		INSERT INTO users (user_id, real_name, nickname, profile_icon, status)
		VALUES ($1, $2, $1, '', 'active')
		ON CONFLICT (user_id) DO NOTHING`, userID, userID)
	if err != nil {
		return User{}, err
	}
	return s.Get(ctx, userID)
}

func (s *PostgresRepository) Get(ctx context.Context, userID string) (User, error) {
	row := s.db.QueryRow(ctx, `SELECT user_id, real_name, nickname, profile_icon, status FROM users WHERE user_id = $1`, userID)
	var user User
	if err := row.Scan(&user.UserID, &user.RealName, &user.Nickname, &user.ProfileIcon, &user.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	return user, nil
}

func (s *PostgresRepository) UpdateProfile(ctx context.Context, userID string, update ProfileUpdate) (User, error) {
	current, err := s.Get(ctx, userID)
	if err != nil {
		return User{}, err
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
	row := s.db.QueryRow(ctx, `
		UPDATE users
		SET real_name = $2, nickname = $3, profile_icon = $4, updated_at = now()
		WHERE user_id = $1
		RETURNING user_id, real_name, nickname, profile_icon, status`, userID, realName, nickname, profileIcon)
	var user User
	if err := row.Scan(&user.UserID, &user.RealName, &user.Nickname, &user.ProfileIcon, &user.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
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
