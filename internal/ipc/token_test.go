package ipc

import (
	"testing"
)

func TestNewTokenManager(t *testing.T) {
	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager failed: %v", err)
	}
	if tm == nil {
		t.Fatal("expected non-nil TokenManager")
	}

	token := tm.GetToken()
	if len(token) != 64 {
		t.Fatalf("expected 64 hex chars, got %d: %q", len(token), token)
	}
}

func TestValidateCorrect(t *testing.T) {
	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager failed: %v", err)
	}

	token := tm.GetToken()
	if !tm.Validate(token) {
		t.Fatal("expected Validate to return true for correct token")
	}
}

func TestValidateWrong(t *testing.T) {
	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager failed: %v", err)
	}

	if tm.Validate("wrong_token") {
		t.Fatal("expected Validate to return false for wrong token")
	}
}

func TestValidateEmpty(t *testing.T) {
	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager failed: %v", err)
	}

	if tm.Validate("") {
		t.Fatal("expected Validate to return false for empty token")
	}
}

func TestRegenerate(t *testing.T) {
	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager failed: %v", err)
	}

	oldToken := tm.GetToken()

	if err := tm.Regenerate(); err != nil {
		t.Fatalf("Regenerate failed: %v", err)
	}

	newToken := tm.GetToken()
	if newToken == oldToken {
		t.Fatal("expected new token to differ from old token after Regenerate")
	}
	if len(newToken) != 64 {
		t.Fatalf("expected 64 hex chars after Regenerate, got %d", len(newToken))
	}
}
