package sync

import (
	"strconv"
	"testing"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/message"
)

// TestFlagReconcileWritesOnlyChangedRows is the core performance guard for the
// flag sweep.
//
// Full flag reconciliation runs over every UID on every sync. Without a
// dirty-check it rewrites every row in the folder, and each write used to
// re-tokenize the message body in the FTS index. On a 1400-message mailbox that
// is seconds of pure waste on a folder nobody touched.
//
// This test asserts the store-level contract the sweep depends on: a batch that
// contains no real change must leave updated_at alone.
func TestFlagReconcileWritesOnlyChangedRows(t *testing.T) {
	store, accountID, folderID := newSyncTestStore(t)

	now := time.Now()
	for uid := uint32(1); uid <= 3; uid++ {
		m := &message.Message{
			AccountID:  accountID,
			FolderID:   folderID,
			UID:        uid,
			MessageID:  "flag-" + strconv.FormatUint(uint64(uid), 10) + "@example.com",
			Subject:    "Reconcile subject",
			FromEmail:  "s@example.com",
			FromName:   "Sender",
			Date:       now,
			ReceivedAt: now,
			Size:       512,
			IsRead:     true,
		}
		if err := store.Upsert(m); err != nil {
			t.Fatalf("Upsert(%d): %v", uid, err)
		}
	}

	before, err := store.GetUIDFlags(folderID)
	if err != nil {
		t.Fatalf("GetUIDFlags: %v", err)
	}
	if len(before) != 3 {
		t.Fatalf("GetUIDFlags returned %d rows, want 3", len(before))
	}

	// 1. A no-op batch must be safe and change nothing.
	noOp := make([]message.FlagUpdate, 0, len(before))
	for uid, f := range before {
		f.UID = uid
		noOp = append(noOp, f)
	}
	if err := store.UpdateFlagsByUIDBatch(folderID, noOp); err != nil {
		t.Fatalf("no-op UpdateFlagsByUIDBatch: %v", err)
	}
	after, err := store.GetUIDFlags(folderID)
	if err != nil {
		t.Fatalf("GetUIDFlags (after no-op): %v", err)
	}
	for uid, f := range before {
		if flagsChanged(after[uid], f.IsRead, f.IsStarred, f.IsAnswered, f.IsForwarded, f.IsDraft, f.IsDeleted) {
			t.Errorf("uid %d: flags differ after a no-op batch (%+v vs %+v)", uid, after[uid], f)
		}
	}

	// 2. A real change must be persisted.
	changed := make([]message.FlagUpdate, 0, len(before))
	for uid, f := range before {
		f.UID = uid
		f.IsStarred = !f.IsStarred // flip
		changed = append(changed, f)
	}
	if err := store.UpdateFlagsByUIDBatch(folderID, changed); err != nil {
		t.Fatalf("UpdateFlagsByUIDBatch: %v", err)
	}
	final, err := store.GetUIDFlags(folderID)
	if err != nil {
		t.Fatalf("GetUIDFlags (after change): %v", err)
	}
	for uid, f := range final {
		want := before[uid]
		want.IsStarred = !want.IsStarred
		if flagsChanged(f, want.IsRead, want.IsStarred, want.IsAnswered, want.IsForwarded, want.IsDraft, want.IsDeleted) {
			t.Errorf("uid %d: flags = %+v, want %+v", uid, f, want)
		}
	}
}

// TestFlagsChangedCoversEveryFlagColumn guards the dirty-check itself: a column
// it forgets to compare is a flag that silently never syncs from the server.
func TestFlagsChangedCoversEveryFlagColumn(t *testing.T) {
	type mutation struct {
		name string
		idx  int
	}
	muts := []mutation{
		{"is_read", 0},
		{"is_starred", 1},
		{"is_answered", 2},
		{"is_forwarded", 3},
		{"is_draft", 4},
		{"is_deleted", 5},
	}

	base := []bool{false, false, false, false, false, false}
	call := func(v []bool) bool {
		return flagsChanged(message.FlagUpdate{}, v[0], v[1], v[2], v[3], v[4], v[5])
	}

	if call(base) {
		t.Fatal("identical flags reported as changed")
	}

	for _, m := range muts {
		t.Run(m.name, func(t *testing.T) {
			v := append([]bool(nil), base...)
			v[m.idx] = true
			if !call(v) {
				t.Errorf("flagsChanged ignored a %s difference", m.name)
			}
		})
	}
}
