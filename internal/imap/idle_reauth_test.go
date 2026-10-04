package imap

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/oauth2"
	"github.com/rs/zerolog"
)

// newIdleForTest builds an IdleConnection wired to a stub credential provider so
// the reconnect loop can be driven without a live IMAP server.
func newIdleForTest(t *testing.T, getCreds func(string) (*ClientConfig, error)) *IdleConnection {
	t.Helper()
	return &IdleConnection{
		accountID:      "acct-test",
		accountName:    "Test",
		log:            zerolog.Nop(),
		getCredentials: getCreds,
		config: IdleConfig{
			ReconnectBackoff:     time.Millisecond,
			MaxReconnectBackoff:  2 * time.Millisecond,
			MaxReconnectAttempts: 6,
		},
		stopCh: make(chan struct{}),
	}
}

// TestIdleLoopStopsImmediatelyOnReauthRequired is the core regression guard.
//
// Before the fix, a revoked grant made ensureConnected fail on every attempt, so
// the loop spun all MaxReconnectAttempts with exponential backoff and then gave
// up silently — no re-auth prompt, sync simply stopped working. The loop must
// instead bail on the first terminal error.
func TestIdleLoopStopsImmediatelyOnReauthRequired(t *testing.T) {
	var calls atomic.Int32
	ic := newIdleForTest(t, func(string) (*ClientConfig, error) {
		calls.Add(1)
		return nil, fmt.Errorf("failed to get OAuth token: %w",
			&oauth2.TokenError{
				Code:        "invalid_client",
				Description: "The provided client secret is invalid.",
				Reauth:      true,
				Err:         oauth2.ErrReauthRequired,
			})
	})

	done := make(chan struct{})
	go func() {
		ic.run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle loop did not return on re-auth-required error")
	}

	if n := calls.Load(); n != 1 {
		t.Fatalf("credential lookups = %d, want 1 (loop must not retry a terminal error)", n)
	}
}

// TestIdleLoopStillRetriesTransientErrors is the counterweight: the early-exit
// must not disable normal reconnect behaviour for network blips.
func TestIdleLoopStillRetriesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	ic := newIdleForTest(t, func(string) (*ClientConfig, error) {
		calls.Add(1)
		return nil, errors.New("dial tcp: connection refused")
	})

	done := make(chan struct{})
	go func() {
		ic.run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("idle loop did not exhaust its retry budget")
	}

	if n := calls.Load(); n < 3 {
		t.Fatalf("credential lookups = %d, want >= 3 (transient errors must retry)", n)
	}
}

// TestIdleLoopStopsOnStopChannel guards the normal shutdown path.
func TestIdleLoopStopsOnStopChannel(t *testing.T) {
	ic := newIdleForTest(t, func(string) (*ClientConfig, error) {
		return nil, errors.New("dial tcp: connection refused")
	})

	done := make(chan struct{})
	go func() {
		ic.run(context.Background())
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	close(ic.stopCh)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle loop ignored the stop channel")
	}
}