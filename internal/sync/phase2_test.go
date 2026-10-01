package sync

import (
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/database"
	"github.com/beheoxinh/hsx2mail/internal/message"
)

// ------------------------------------------------------------------ 2-02 --

// TestDiffUIDs_MatchesSetDifference pins the map-free UID merge against the
// set semantics it replaced, and asserts the ascending order the old map
// iteration never guaranteed.
func TestDiffUIDs_MatchesSetDifference(t *testing.T) {
	cases := []struct {
		name          string
		local, remote []uint32
	}{
		{"both empty", nil, nil},
		{"empty local", nil, []uint32{1, 2, 3}},
		{"empty remote", []uint32{1, 2, 3}, nil},
		{"disjoint", []uint32{1, 3, 5}, []uint32{2, 4, 6}},
		{"identical", []uint32{1, 2, 3}, []uint32{1, 2, 3}},
		{"remote superset", []uint32{1, 2, 3}, []uint32{1, 2, 3, 4, 5}},
		{"local superset", []uint32{1, 2, 3, 4, 5}, []uint32{1, 2, 3}},
		{"interleaved", []uint32{1, 4, 9}, []uint32{2, 4, 5, 9, 12}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			local := append([]uint32(nil), tc.local...)
			remote := append([]uint32(nil), tc.remote...)
			// diffUIDs requires ascending input; the sync path sorts first.
			sort.Slice(local, func(i, j int) bool { return local[i] < local[j] })
			sort.Slice(remote, func(i, j int) bool { return remote[i] < remote[j] })

			newUIDs, deletedUIDs, existingUIDs := diffUIDs(local, remote)

			localSet := toSet(local)
			remoteSet := toSet(remote)

			var wantNew, wantDeleted, wantExisting []uint32
			for _, u := range remote {
				if !localSet[u] {
					wantNew = append(wantNew, u)
				}
			}
			for _, u := range local {
				if !remoteSet[u] {
					wantDeleted = append(wantDeleted, u)
				} else {
					wantExisting = append(wantExisting, u)
				}
			}

			if !equalOrNil(newUIDs, wantNew) {
				t.Errorf("new = %v, want %v", newUIDs, wantNew)
			}
			if !equalOrNil(deletedUIDs, wantDeleted) {
				t.Errorf("deleted = %v, want %v", deletedUIDs, wantDeleted)
			}
			if !equalOrNil(existingUIDs, wantExisting) {
				t.Errorf("existing = %v, want %v", existingUIDs, wantExisting)
			}
			if !sort.SliceIsSorted(deletedUIDs, func(i, j int) bool { return deletedUIDs[i] < deletedUIDs[j] }) {
				t.Errorf("deleted is not ascending: %v", deletedUIDs)
			}
			// Every local uid lands in exactly one of the two buckets.
			if len(deletedUIDs)+len(existingUIDs) != len(local) {
				t.Errorf("local uids not fully partitioned: %d deleted + %d existing != %d local",
					len(deletedUIDs), len(existingUIDs), len(local))
			}
			if len(newUIDs)+len(existingUIDs) != len(remote) {
				t.Errorf("remote uids not fully partitioned: %d new + %d existing != %d remote",
					len(newUIDs), len(existingUIDs), len(remote))
			}
		})
	}
}

func toSet(uids []uint32) map[uint32]bool {
	m := make(map[uint32]bool, len(uids))
	for _, u := range uids {
		m[u] = true
	}
	return m
}

func equalOrNil(a, b []uint32) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// ------------------------------------------------------------------ 2-17 --

// TestBodyFetchCounters_ConcurrentAccess is the -race regression for the
// heartbeat/fetch-loop counter pair. Under `go test -race` this fails if the
// counters ever go back to plain ints.
func TestBodyFetchCounters_ConcurrentAccess(t *testing.T) {
	var c bodyFetchCounters

	var wg sync.WaitGroup
	// Reader: the heartbeat goroutine's access pattern.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20000; i++ {
			_ = c.fetched.Load()
			_ = c.failed.Load()
		}
	}()
	// Writers: the fetch loop, in both of its shapes.
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			c.fetched.Add(3)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			c.failed.Add(1)
		}
	}()
	wg.Wait()

	if got := c.fetched.Load(); got != 30000 {
		t.Errorf("fetched = %d, want 30000", got)
	}
	if got := c.failed.Load(); got != 10000 {
		t.Errorf("failed = %d, want 10000", got)
	}
}

// ------------------------------------------------------------------ 2-09 --

// TestComputeThreadIDsBatch_MatchesPerMessage asserts the batched thread-key
// resolution returns exactly what repeated computeThreadID returned, for the
// cases that actually differ: resolved references, unresolved references, and
// messages with no threading headers at all.
func TestComputeThreadIDsBatch_MatchesPerMessage(t *testing.T) {
	store, accountID, folderID := newSyncTestStore(t)
	e := &Engine{messageStore: store}

	if err := store.Upsert(&message.Message{
		AccountID: accountID, FolderID: folderID, UID: 1,
		MessageID: "<root@example.com>", ThreadID: "root@example.com",
		Subject: "root", Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	// A row with no thread_id: COALESCE falls back to the row id.
	if err := store.Upsert(&message.Message{
		AccountID: accountID, FolderID: folderID, UID: 2,
		MessageID: "<headless@example.com>", Subject: "headless",
		Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed headless: %v", err)
	}

	msgs := []*message.Message{
		{ID: "msg-a", AccountID: accountID, FolderID: folderID, UID: 10,
			MessageID: "<a@example.com>", InReplyTo: "<root@example.com>",
			Subject: "a", References: `["<root@example.com>"]`},
		{ID: "msg-b", AccountID: accountID, FolderID: folderID, UID: 11,
			MessageID: "<b@example.com>",
			Subject:   "b", References: `["<ghost@example.com>","<root@example.com>"]`},
		{ID: "msg-c", AccountID: accountID, FolderID: folderID, UID: 12,
			MessageID: "<c@example.com>", Subject: "c"},
		{ID: "msg-d", AccountID: accountID, FolderID: folderID, UID: 13,
			MessageID: "<d@example.com>", InReplyTo: "<ghost@example.com>",
			Subject: "d", References: `["<also-ghost@example.com>"]`},
	}

	batched := e.computeThreadIDsBatch(accountID, msgs)
	for _, m := range msgs {
		want := e.computeThreadID(accountID, m)
		got, ok := batched[m.ID]
		if !ok {
			t.Errorf("%s: batched map has no entry", m.MessageID)
			continue
		}
		if got != want {
			t.Errorf("%s: batched=%q per-message=%q", m.MessageID, got, want)
		}
	}
}

// TestComputeThreadIDsBatch_DoesNotSelfThread guards the subtle regression the
// batch introduced: a freshly-inserted message with no thread_id must not
// resolve its thread key to its own row UUID. FindThreadID only ever probes
// In-Reply-To and References, so a header-less message is its own thread.
func TestComputeThreadIDsBatch_DoesNotSelfThread(t *testing.T) {
	store, accountID, folderID := newSyncTestStore(t)
	e := &Engine{messageStore: store}

	if err := store.Upsert(&message.Message{
		AccountID: accountID, FolderID: folderID, UID: 1,
		MessageID: "<lonely@example.com>", Subject: "lonely",
		Date: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	m := &message.Message{
		ID: "msg-lonely", AccountID: accountID, FolderID: folderID, UID: 2,
		MessageID: "<also-lonely@example.com>", Subject: "also lonely",
		Date: time.Now().UTC(),
	}
	batched := e.computeThreadIDsBatch(accountID, []*message.Message{m})
	want := e.computeThreadID(accountID, m)
	if got := batched[m.ID]; got != want {
		t.Errorf("self-thread regression: batched=%q per-message=%q", got, want)
	}
	if batched[m.ID] == m.ID {
		t.Error("a header-less message resolved to its own row id")
	}
}

func newSyncTestStore(t *testing.T) (*message.Store, string, string) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const accountID = "acct-1"
	const folderID = "folder-1"
	if _, err := db.Exec(
		`INSERT INTO accounts (id, name, email, imap_host, smtp_host, username)
		 VALUES (?, ?, ?, ?, ?, ?)`, accountID, "Test", "test@example.com",
		"imap.example.com", "smtp.example.com", "test@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO folders (id, account_id, name, path, folder_type)
		 VALUES (?, ?, ?, ?, ?)`, folderID, accountID, "INBOX", "INBOX", "inbox"); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	return message.NewStore(db), accountID, folderID
}
