package service

import (
	"context"
	"errors"
	"strings"

	"ai-style-backend/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// userRepository 定义用户服务需要的数据库操作。
type userRepository interface {
	FindByOpenID(ctx context.Context, openID string) (*model.User, error)
	Create(ctx context.Context, openID string, nickname *string, avatarURL *string) (*model.User, error)
}

// UserService 负责用户相关的业务逻辑。
type UserService struct {
	userRepo userRepository
}

// NewUserService 创建用户服务。
func NewUserService(userRepo userRepository) *UserService {
	return &UserService{userRepo: userRepo}
}

// GetOrCreateByOpenID 根据 OpenID 查找用户；不存在时创建。
func (s *UserService) GetOrCreateByOpenID(
	ctx context.Context,
	openID string,
) (*model.User, error) {
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return nil, errors.New("openid cannot be empty")
	}

	user, err := s.userRepo.FindByOpenID(ctx, openID)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	user, err = s.userRepo.Create(ctx, openID, nil, nil)
	if err == nil {
		return user, nil
	}

	// 只有违反数据库唯一约束时，才尝试查询已有用户。
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil, err
	}

	existingUser, findErr := s.userRepo.FindByOpenID(ctx, openID)
	if findErr == nil {
		return existingUser, nil
	}

	return nil, err
}
