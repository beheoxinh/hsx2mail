package message

import (
	"testing"
	"time"
)

func assertOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d conversations %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestConversationSortUnreadFirst pins the default ordering: unread threads
// float to the top (newest first within each group) so the mailbox surface
// matches what the user still owes a reply to.
func TestConversationSortUnreadFirst(t *testing.T) {
	assertOrder(t, convThreadOrder(t, "unread-first"),
		[]string{"thread-unread-new", "thread-unread-old", "thread-read-new", "thread-read-old"})
}

// TestConversationSortNewest is the plain recency order.
func TestConversationSortNewest(t *testing.T) {
	assertOrder(t, convThreadOrder(t, "newest"),
		[]string{"thread-unread-new", "thread-read-new", "thread-unread-old", "thread-read-old"})
}

// TestConversationSortOldest is the reverse-recency order.
func TestConversationSortOldest(t *testing.T) {
	assertOrder(t, convThreadOrder(t, "oldest"),
		[]string{"thread-read-old", "thread-unread-old", "thread-read-new", "thread-unread-new"})
}

// TestConversationSortUnknownFallsBack guards the SQL builder: an unrecognized
// sort string from a corrupted setting must land on a safe default instead of
// being interpolated into the query.
func TestConversationSortUnknownFallsBack(t *testing.T) {
	q := convListSQLFor(false, "uid; DROP TABLE messages--", "")
	if q == "" {
		t.Fatal("unknown sort order produced an empty query")
	}
	if len(q) > 0 && containsInjection(q, "DROP TABLE") {
		t.Fatalf("sort order was interpolated into the query:\n%s", q)
	}
}

func containsInjection(q, needle string) bool {
	for i := 0; i+len(needle) <= len(q); i++ {
		if q[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// convThreadOrder seeds four conversations (read/unread x old/new) and returns
// the thread IDs in the order the given sort mode produces.
func convThreadOrder(t *testing.T, sortOrder string) []string {
	t.Helper()
	store, accountID, folderID := newConvSortStore(t)

	now := time.Now()
	type row struct {
		uid    uint32
		thread string
		read   bool
		age    time.Duration
	}
	rows := []row{
		{1, "thread-read-old", true, 72 * time.Hour},
		{2, "thread-unread-old", false, 48 * time.Hour},
		{3, "thread-read-new", true, 2 * time.Hour},
		{4, "thread-unread-new", false, 0},
	}
	for _, r := range rows {
		id := "m-" + r.thread
		m := &Message{
			AccountID:  accountID,
			FolderID:   folderID,
			UID:        r.uid,
			MessageID:  id + "@example.com",
			ThreadID:   r.thread,
			Subject:    r.thread,
			FromEmail:  "s@example.com",
			FromName:   "Sender",
			Date:       now.Add(-r.age),
			ReceivedAt: now.Add(-r.age),
		}
		if err := store.Upsert(m); err != nil {
			t.Fatalf("Upsert(uid=%d): %v", r.uid, err)
		}
		if _, err := store.db.Exec(`UPDATE messages SET is_read = ? WHERE uid = ?`, r.read, r.uid); err != nil {
			t.Fatalf("set is_read(uid=%d): %v", r.uid, err)
		}
	}

	got, err := store.ListConversationsByFolder(folderID, 0, 50, sortOrder, "")
	if err != nil {
		t.Fatalf("ListConversationsByFolder(%q): %v", sortOrder, err)
	}
	out := make([]string, 0, len(got))
	for _, c := range got {
		out = append(out, c.ThreadID)
	}
	return out
}