package sync

import (
	"errors"
	"testing"
)

// TestSyncAccountInboxBlockingReportsBusy proves the busy guard returns a
// typed ErrSyncBusy — not a silent "no new mail" — when the account's sync
// slot is already claimed. The IDLE new-mail handler branches on that error
// to park and retry the event; with the old (nil, nil) skip it could not
// tell "nothing arrived" from "a tick owns the account", and dropped real
// new mail until the next scheduled poll.
func TestSyncAccountInboxBlockingReportsBusy(t *testing.T) {
	s := NewScheduler(nil, nil, nil)
	const id = "acct-busy"

	s.syncingMu.Lock()
	s.syncing[id] = true
	s.syncingMu.Unlock()

	_, err := s.SyncAccountInboxBlocking(id)
	if !errors.Is(err, ErrSyncBusy) {
		t.Fatalf("SyncAccountInboxBlocking on busy account = %v, want ErrSyncBusy", err)
	}

	// The guard must not consume the owner's slot.
	s.syncingMu.Lock()
	stillClaimed := s.syncing[id]
	s.syncingMu.Unlock()
	if !stillClaimed {
		t.Error("busy guard cleared the owner's syncing slot")
	}
}

// TestClearSyncingSlotReleasesClaim covers the early-return path: the slot is
// claimed before the account load, so a load failure must hand it back or the
// account is wedged forever.
func TestClearSyncingSlotReleasesClaim(t *testing.T) {
	s := NewScheduler(nil, nil, nil)
	const id = "acct-loadfail"

	s.syncingMu.Lock()
	s.syncing[id] = true
	s.syncingMu.Unlock()

	s.clearSyncingSlot(id)

	s.syncingMu.Lock()
	_, claimed := s.syncing[id]
	s.syncingMu.Unlock()
	if claimed {
		t.Error("clearSyncingSlot left the slot claimed")
	}
}
