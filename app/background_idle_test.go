package app

import (
	"testing"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/imap"
)

// swapIdleResyncAfterFunc replaces the retry-arming edge for the duration of
// the test: the recorded callbacks are never fired, so the bookkeeping can be
// asserted without a real 15s wait or a live handleIdleNewMail.
func swapIdleResyncAfterFunc(t *testing.T) *int {
	t.Helper()
	orig := idleResyncAfterFunc
	armed := 0
	idleResyncAfterFunc = func(d time.Duration, f func()) *time.Timer {
		armed++
		return nil
	}
	t.Cleanup(func() { idleResyncAfterFunc = orig })
	return &armed
}

// TestRequestIdleResyncArmsSingleRetryPerInbox: several IDLE events landing
// while the inbox is busy must coalesce into ONE delayed retry, with the
// attempt counter folding them together.
func TestRequestIdleResyncArmsSingleRetryPerInbox(t *testing.T) {
	armed := swapIdleResyncAfterFunc(t)
	a := &App{}
	ev := imap.MailEvent{AccountID: "acc-1", Count: 2}

	a.requestIdleResync(ev, "inbox-1")
	a.requestIdleResync(ev, "inbox-1")

	if *armed != 1 {
		t.Fatalf("timers armed = %d, want 1 (one retry per inbox)", *armed)
	}
	if got := a.idleResyncPending["acc-1:inbox-1"].attempts; got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}

	// A different inbox arms its own timer.
	a.requestIdleResync(ev, "inbox-2")
	if *armed != 2 {
		t.Fatalf("timers armed = %d, want 2", *armed)
	}
}

// TestRequestIdleResyncGivesUpAfterMaxAttempts: a permanently-busy inbox must
// not retry forever — after the bounded chain the entry is dropped (the
// scheduled poll is the backstop), and a genuinely new burst starts fresh.
func TestRequestIdleResyncGivesUpAfterMaxAttempts(t *testing.T) {
	swapIdleResyncAfterFunc(t)
	a := &App{}
	ev := imap.MailEvent{AccountID: "acc-1"}
	key := "acc-1:inbox-1"

	// Calls 1..max arm retries; call max+1 exceeds the bound and drops it.
	for i := 0; i <= idleResyncMaxAttempts; i++ {
		a.requestIdleResync(ev, "inbox-1")
	}
	if _, exists := a.idleResyncPending[key]; exists {
		t.Fatal("pending entry should have been dropped after max attempts")
	}

	// A new request after give-up is a fresh chain, not a poisoned one.
	a.requestIdleResync(ev, "inbox-1")
	entry, exists := a.idleResyncPending[key]
	if !exists {
		t.Fatal("fresh burst after give-up should re-arm")
	}
	if entry.attempts != 1 {
		t.Errorf("attempts after fresh burst = %d, want 1", entry.attempts)
	}
}
