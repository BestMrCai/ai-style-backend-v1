package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"ai-style-backend/internal/model"

	"github.com/jackc/pgx/v5"
)

type fakeUserRepository struct {
	findFunc   func(context.Context, string) (*model.User, error)
	createFunc func(context.Context, string, *string, *string) (*model.User, error)
}

func (f *fakeUserRepository) FindByOpenID(
	ctx context.Context,
	openID string,
) (*model.User, error) {
	return f.findFunc(ctx, openID)
}

func (f *fakeUserRepository) Create(
	ctx context.Context,
	openID string,
	nickname *string,
	avatarURL *string,
) (*model.User, error) {
	return f.createFunc(ctx, openID, nickname, avatarURL)
}

func TestGetOrCreateByOpenIDRejectsEmptyOpenID(t *testing.T) {
	repo := &fakeUserRepository{}
	svc := NewUserService(repo)

	_, err := svc.GetOrCreateByOpenID(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected an error for empty OpenID")
	}
}

func TestGetOrCreateByOpenIDReturnsExistingUser(t *testing.T) {
	expected := &model.User{ID: 1, OpenID: "test-openid"}

	repo := &fakeUserRepository{
		findFunc: func(_ context.Context, openID string) (*model.User, error) {
			if openID != "test-openid" {
				t.Fatalf("unexpected OpenID: %q", openID)
			}
			return expected, nil
		},
		createFunc: func(context.Context, string, *string, *string) (*model.User, error) {
			t.Fatal("Create should not be called for an existing user")
			return nil, nil
		},
	}

	svc := NewUserService(repo)
	user, err := svc.GetOrCreateByOpenID(context.Background(), "test-openid")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.ID != expected.ID {
		t.Fatalf("expected user ID %d, got %d", expected.ID, user.ID)
	}
}

func TestGetOrCreateByOpenIDCreatesNewUser(t *testing.T) {
	expected := &model.User{ID: 2, OpenID: "new-openid"}
	createCalled := false

	repo := &fakeUserRepository{
		findFunc: func(context.Context, string) (*model.User, error) {
			return nil, pgx.ErrNoRows
		},
		createFunc: func(
			_ context.Context,
			openID string,
			nickname *string,
			avatarURL *string,
		) (*model.User, error) {
			createCalled = true
			if openID != "new-openid" {
				t.Fatalf("unexpected OpenID: %q", openID)
			}
			return expected, nil
		},
	}

	svc := NewUserService(repo)
	user, err := svc.GetOrCreateByOpenID(context.Background(), "new-openid")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !createCalled {
		t.Fatal("expected Create to be called")
	}
	if user.ID != expected.ID {
		t.Fatalf("expected user ID %d, got %d", expected.ID, user.ID)
	}
}

func TestGetOrCreateByOpenIDHandlesConcurrentCreation(t *testing.T) {
	expected := &model.User{ID: 3, OpenID: "racing-openid"}
	findCalls := 0

	repo := &fakeUserRepository{
		findFunc: func(context.Context, string) (*model.User, error) {
			findCalls++
			if findCalls == 1 {
				return nil, pgx.ErrNoRows
			}
			return expected, nil
		},
		createFunc: func(context.Context, string, *string, *string) (*model.User, error) {
			return nil, &pgconn.PgError{
				Code: "23505",
			}
		},
	}

	svc := NewUserService(repo)
	user, err := svc.GetOrCreateByOpenID(context.Background(), "racing-openid")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.ID != expected.ID {
		t.Fatalf("expected user ID %d, got %d", expected.ID, user.ID)
	}
}
