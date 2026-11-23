package service

import (
	"context"
	"errors"

	"ai-style-backend/internal/model"
)

// wechatCodeExchanger 定义微信登录所需的能力。
type wechatCodeExchanger interface {
	ExchangeCode(ctx context.Context, code string) (string, error)
}

// LoginService 负责协调微信登录和用户管理。
type LoginService struct {
	wechatClient wechatCodeExchanger
	userService  *UserService
}

// NewLoginService 创建登录服务。
func NewLoginService(
	wechatClient wechatCodeExchanger,
	userService *UserService,
) *LoginService {
	return &LoginService{
		wechatClient: wechatClient,
		userService:  userService,
	}
}

// LoginByCode 使用微信临时登录凭证完成登录。
func (s *LoginService) LoginByCode(
	ctx context.Context,
	code string,
) (*model.User, error) {
	if s.wechatClient == nil || s.userService == nil {
		return nil, errors.New("login service is not configured")
	}

	// 第一步：向微信服务器换取 OpenID。
	openID, err := s.wechatClient.ExchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}

	// 第二步：根据 OpenID 查询或创建用户。
	user, err := s.userService.GetOrCreateByOpenID(ctx, openID)
	if err != nil {
		return nil, err
	}

	return user, nil
}
