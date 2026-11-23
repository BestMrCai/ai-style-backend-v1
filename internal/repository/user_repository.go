package repository

import (
	"context"
	"errors"

	"ai-style-backend/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserRepository 负责访问 users 用户表。
type UserRepository struct {
	db *pgxpool.Pool
}

// NewUserRepository 创建用户数据访问对象。
func NewUserRepository(db *pgxpool.Pool) *UserRepository {
	return &UserRepository{db: db}
}

// UsersTableExists 检查 users 表是否存在。
func (r *UserRepository) UsersTableExists(
	ctx context.Context,
) (bool, error) {
	var exists bool

	err := r.db.QueryRow(
		ctx,
		`SELECT to_regclass('public.users') IS NOT NULL`,
	).Scan(&exists)

	if err != nil {
		return false, err
	}

	return exists, nil
}

// FindByOpenID 根据微信 OpenID 查询用户。
// 如果用户不存在，返回 pgx.ErrNoRows。
func (r *UserRepository) FindByOpenID(
	ctx context.Context,
	openID string,
) (*model.User, error) {
	var user model.User

	err := r.db.QueryRow(ctx, `
		SELECT id, openid, nickname, avatar_url,
		       status, created_at, updated_at
		FROM users
		WHERE openid = $1
	`, openID).Scan(
		&user.ID,
		&user.OpenID,
		&user.Nickname,
		&user.AvatarURL,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}

	return &user, nil
}

// Create 创建新用户。
// OpenID 由数据库唯一约束保证不能重复。
func (r *UserRepository) Create(
	ctx context.Context,
	openID string,
	nickname *string,
	avatarURL *string,
) (*model.User, error) {
	var user model.User

	err := r.db.QueryRow(ctx, `
		INSERT INTO users (openid, nickname, avatar_url)
		VALUES ($1, $2, $3)
		RETURNING id, openid, nickname, avatar_url,
		          status, created_at, updated_at
	`, openID, nickname, avatarURL).Scan(
		&user.ID,
		&user.OpenID,
		&user.Nickname,
		&user.AvatarURL,
		&user.Status,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		return nil, err
	}

	return &user, nil
}
