package sync

import (
	"testing"
	"time"
)

// TestBackoffGrowsAndIsCapped is the runnable check for the Phase 3 task 3-07
// failure backoff: consecutive failures must double the delay, the delay must
// never exceed BackoffMax, and one success must clear the state so a recovered
// account is not penalised on its next scheduled sync.
func TestBackoffGrowsAndIsCapped(t *testing.T) {
	s := NewScheduler(nil, nil, nil)
	const id = "acct-1"

	for want := 1; want <= 9; want++ {
		before := time.Now()
		s.recordFailure(id)

		s.stormMu.Lock()
		got := s.failCount[id]
		next := s.nextAttempt[id]
		s.stormMu.Unlock()

		if got != want {
			t.Fatalf("failCount = %d, want %d", got, want)
		}
		wait := next.Sub(before)
		// BackoffBase << (n-1), clamped to BackoffMax, plus up to 25% jitter.
		lo := BackoffBase << (want - 1)
		if lo > BackoffMax {
			lo = BackoffMax
		}
		if wait < lo || wait > BackoffMax+BackoffMax/4+time.Second {
			t.Errorf("attempt %d backoff = %v, want within [%v, %v]", want, wait, lo, BackoffMax+BackoffMax/4+time.Second)
		}
	}

	// A successful sync clears the penalty entirely.
	s.recordSuccess(id)

	s.stormMu.Lock()
	fails := s.failCount[id]
	_, hasNext := s.nextAttempt[id]
	s.stormMu.Unlock()

	if fails != 0 {
		t.Errorf("failCount after success = %d, want 0", fails)
	}
	if hasNext {
		t.Errorf("nextAttempt after success = %v, want cleared", s.nextAttempt[id])
	}
}

// TestBackoffBlocksThenReleases verifies the gate itself: right after a
// failure the account is blocked, and once the deadline has passed it is
// allowed again.
func TestBackoffBlocksThenReleases(t *testing.T) {
	s := NewScheduler(nil, nil, nil)
	const id = "acct-2"

	if _, blocked := s.backoffRemaining(id); blocked {
		t.Fatal("fresh account must not be blocked")
	}

	s.recordFailure(id)
	wait, blocked := s.backoffRemaining(id)
	if !blocked {
		t.Fatal("account must be blocked right after a failure")
	}
	if wait <= 0 || wait > BackoffMax+BackoffBase/4 {
		t.Errorf("backoffRemaining = %v, want (0, %v]", wait, BackoffMax+BackoffBase/4)
	}

	// Expire the deadline by hand rather than sleeping.
	s.stormMu.Lock()
	s.nextAttempt[id] = time.Now().Add(-time.Second)
	s.stormMu.Unlock()

	if _, blocked := s.backoffRemaining(id); blocked {
		t.Error("account must be released once the deadline has passed")
	}
}

// TestSeedJitterIsDrawnOnceAndIsBounded checks the boot-storm stagger: the
// per-account jitter is drawn exactly once (so it never re-delays a healthy
// account on later ticks) and always lands inside the configured window.
func TestSeedJitterIsDrawnOnceAndIsBounded(t *testing.T) {
	s := NewScheduler(nil, nil, nil)

	const accounts = 50
	spread := make(map[int64]struct{}, accounts)
	now := time.Now()
	for i := 0; i < accounts; i++ {
		id := string(rune('a'+i%26)) + string(rune('0'+i/26))
		s.seedJitter(id)

		s.stormMu.Lock()
		first := s.nextAttempt[id]
		seeded := s.seeded[id]
		s.stormMu.Unlock()

		if !seeded {
			t.Fatalf("%s: not marked as seeded", id)
		}
		d := first.Sub(now)
		if d < 0 || d > InitialJitterWindow+time.Second {
			t.Errorf("%s: jitter = %v, want within [0, %v]", id, d, InitialJitterWindow)
		}
		spread[first.UnixNano()] = struct{}{}

		// Second draw must be a no-op.
		s.seedJitter(id)
		s.stormMu.Lock()
		second := s.nextAttempt[id]
		s.stormMu.Unlock()
		if !second.Equal(first) {
			t.Errorf("%s: jitter re-drawn (%v -> %v)", id, first, second)
		}
	}

	// The whole point is de-correlation: the accounts must not all collapse
	// onto a single instant. Allow a small collision count for 50 samples.
	if len(spread) < accounts*3/4 {
		t.Errorf("only %d distinct jitter deadlines across %d accounts; the boot fan-out is not staggered", len(spread), accounts)
	}
}

// TestSyncSlotsBoundConcurrency proves the cap exists: acquiring more than
// MaxConcurrentAccountSyncs slots without releasing must fail.
func TestSyncSlotsBoundConcurrency(t *testing.T) {
	s := NewScheduler(nil, nil, nil)
	if cap(s.slots) != MaxConcurrentAccountSyncs {
		t.Fatalf("slot capacity = %d, want %d", cap(s.slots), MaxConcurrentAccountSyncs)
	}
	for i := 0; i < MaxConcurrentAccountSyncs; i++ {
		s.slots <- struct{}{}
	}
	select {
	case s.slots <- struct{}{}:
		t.Fatal("slot acquired beyond the cap")
	default:
	}
}
