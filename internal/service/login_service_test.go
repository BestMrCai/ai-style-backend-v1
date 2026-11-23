package service

import (
	"context"
	"errors"
	"testing"

	"ai-style-backend/internal/model"

	"github.com/jackc/pgx/v5"
)

// fakeWeChatClient 模拟微信服务器。
type fakeWeChatClient struct {
	openID string
	err    error
}

func (f *fakeWeChatClient) ExchangeCode(
	ctx context.Context,
	code string,
) (string, error) {
	if f.err != nil {
		return "", f.err
	}

	if code == "" {
		return "", ErrLoginCodeEmpty
	}

	return f.openID, nil
}

func TestLoginByCodeSuccess(t *testing.T) {
	expectedUser := &model.User{
		ID:     10,
		OpenID: "test-openid",
	}

	userRepo := &fakeUserRepository{
		findFunc: func(
			ctx context.Context,
			openID string,
		) (*model.User, error) {
			if openID != "test-openid" {
				t.Fatalf("unexpected OpenID: %q", openID)
			}

			return expectedUser, nil
		},
		createFunc: func(
			context.Context,
			string,
			*string,
			*string,
		) (*model.User, error) {
			t.Fatal("Create should not be called")
			return nil, nil
		},
	}

	userService := NewUserService(userRepo)

	wechatClient := &fakeWeChatClient{
		openID: "test-openid",
	}

	loginService := NewLoginService(
		wechatClient,
		userService,
	)

	user, err := loginService.LoginByCode(
		context.Background(),
		"test-code",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if user.ID != expectedUser.ID {
		t.Fatalf(
			"expected user ID %d, got %d",
			expectedUser.ID,
			user.ID,
		)
	}
}

func TestLoginByCodeWeChatFailure(t *testing.T) {
	wechatClient := &fakeWeChatClient{
		err: ErrWeChatAPI,
	}

	userRepo := &fakeUserRepository{
		findFunc: func(
			context.Context,
			string,
		) (*model.User, error) {
			t.Fatal("database lookup should not happen")
			return nil, pgx.ErrNoRows
		},
		createFunc: func(
			context.Context,
			string,
			*string,
			*string,
		) (*model.User, error) {
			t.Fatal("user creation should not happen")
			return nil, errors.New("unexpected create call")
		},
	}

	userService := NewUserService(userRepo)

	loginService := NewLoginService(
		wechatClient,
		userService,
	)

	_, err := loginService.LoginByCode(
		context.Background(),
		"test-code",
	)

	if !errors.Is(err, ErrWeChatAPI) {
		t.Fatalf("expected ErrWeChatAPI, got %v", err)
	}
}
