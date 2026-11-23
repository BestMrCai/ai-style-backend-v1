package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExchangeCodeSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query()

			if query.Get("appid") != "test-app-id" {
				t.Errorf("unexpected appid")
			}

			if query.Get("secret") != "test-app-secret" {
				t.Errorf("unexpected secret")
			}

			if query.Get("js_code") != "test-code" {
				t.Errorf("unexpected login code")
			}

			if query.Get("grant_type") != "authorization_code" {
				t.Errorf("unexpected grant_type")
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			_, _ = w.Write([]byte(
				`{"openid":"test-openid","session_key":"test-session-key"}`,
			))
		},
	))
	defer server.Close()

	client := NewWeChatClient(
		"test-app-id",
		"test-app-secret",
	)
	client.endpoint = server.URL

	openID, err := client.ExchangeCode(
		context.Background(),
		"test-code",
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if openID != "test-openid" {
		t.Fatalf("expected test-openid, got %q", openID)
	}
}

func TestExchangeCodeRejectsEmptyCode(t *testing.T) {
	client := NewWeChatClient(
		"test-app-id",
		"test-app-secret",
	)

	_, err := client.ExchangeCode(
		context.Background(),
		"   ",
	)

	if !errors.Is(err, ErrLoginCodeEmpty) {
		t.Fatalf("expected ErrLoginCodeEmpty, got %v", err)
	}
}

func TestExchangeCodeRejectsMissingConfiguration(t *testing.T) {
	client := NewWeChatClient("", "")

	_, err := client.ExchangeCode(
		context.Background(),
		"test-code",
	)

	if !errors.Is(err, ErrWeChatConfigMissing) {
		t.Fatalf("expected ErrWeChatConfigMissing, got %v", err)
	}
}

func TestExchangeCodeHandlesWeChatError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			_, _ = w.Write([]byte(
				`{"errcode":40029,"errmsg":"invalid code"}`,
			))
		},
	))
	defer server.Close()

	client := NewWeChatClient(
		"test-app-id",
		"test-app-secret",
	)
	client.endpoint = server.URL

	_, err := client.ExchangeCode(
		context.Background(),
		"invalid-code",
	)

	if !errors.Is(err, ErrWeChatAPI) {
		t.Fatalf("expected ErrWeChatAPI, got %v", err)
	}
}
