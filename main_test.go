package main

import (
	"os"
	"testing"
)

func TestDebugModeDefault(t *testing.T) {
	// Ensure the env var is not set so we only test the default flag value
	t.Setenv("HSX2MAIL_DEBUG", "")

	if DebugMode() {
		t.Error("DebugMode() = true, want false by default")
	}
}

func TestDebugModeEnvVar(t *testing.T) {
	t.Setenv("HSX2MAIL_DEBUG", "1")

	if !DebugMode() {
		t.Error("DebugMode() = false, want true when HSX2MAIL_DEBUG=1")
	}

	// Verify env var was actually set (sanity check)
	if os.Getenv("HSX2MAIL_DEBUG") != "1" {
		t.Error("HSX2MAIL_DEBUG env var not set correctly")
	}
}
