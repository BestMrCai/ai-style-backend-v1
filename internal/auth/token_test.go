package auth

import (
	"strings"
	"testing"
	"time"
)

func TestSignAndVerify(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"

	token, err := Sign(123, secret, time.Hour)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	userID, err := Verify(token, secret)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	if userID != 123 {
		t.Fatalf("expected user ID 123, got %d", userID)
	}
}

func TestVerifyRejectsTamperedToken(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"

	token, err := Sign(123, secret, time.Hour)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	parts := strings.Split(token, ".")
	parts[1] = parts[1] + "tampered"
	tamperedToken := strings.Join(parts, ".")

	_, err = Verify(tamperedToken, secret)
	if err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef"

	token, err := Sign(123, secret, time.Hour)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	_, err = Verify(
		token,
		"abcdef0123456789abcdef0123456789",
	)
	if err == nil {
		t.Fatal("expected token signed with another secret to be rejected")
	}
}

func TestSignRejectsShortSecret(t *testing.T) {
	_, err := Sign(123, "short", time.Hour)
	if err == nil {
		t.Fatal("expected short secret to be rejected")
	}
}
