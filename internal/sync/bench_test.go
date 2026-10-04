package sync

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/database"
	"github.com/beheoxinh/hsx2mail/internal/message"
)

// newSyncBenchStore mirrors newSyncTestStore for benchmarks.
func newSyncBenchStore(b *testing.B) (*message.Store, string, string) {
	b.Helper()
	db, err := database.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		b.Fatalf("Migrate: %v", err)
	}

	const accountID = "bench-acct"
	const folderID = "bench-folder"
	if _, err := db.Exec(
		`INSERT INTO accounts (id, name, email, imap_host, smtp_host, username)
		 VALUES (?, 'Bench', ?, 'imap.example.com', 'smtp.example.com', ?)`,
		accountID, accountID+"@example.com", accountID,
	); err != nil {
		b.Fatalf("seed account: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO folders (id, account_id, name, path, folder_type)
		 VALUES (?, ?, 'INBOX', 'INBOX', 'inbox')`,
		folderID, accountID,
	); err != nil {
		b.Fatalf("seed folder: %v", err)
	}
	return message.NewStore(db), accountID, folderID
}

// seedBenchMessages inserts n messages carrying realistic body text so the FTS
// trigger cost is represented.
func seedBenchMessages(b *testing.B, store *message.Store, accountID, folderID string, n int) {
	b.Helper()
	now := time.Now()
	body := make([]byte, 2048)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	for uid := 1; uid <= n; uid++ {
		m := &message.Message{
			AccountID:  accountID,
			FolderID:   folderID,
			UID:        uint32(uid),
			MessageID:  "bench@example.com",
			ThreadID:   "bench-thread",
			Subject:    "benchmark message",
			FromEmail:  "sender@example.com",
			FromName:   "Sender",
			Date:       now,
			ReceivedAt: now,
			Size:       len(body),
			BodyText:   string(body),
			IsRead:     true,
		}
		if err := store.Upsert(m); err != nil {
			b.Fatalf("Upsert(%d): %v", uid, err)
		}
	}
}

// BenchmarkGetUIDFlags measures the snapshot the flag reconcile reads once per
// sync cycle. This replaced per-message lookups, so its cost decides whether
// full reconciliation is affordable on a large mailbox.
func BenchmarkGetUIDFlags(b *testing.B) {
	store, accountID, folderID := newSyncBenchStore(b)
	seedBenchMessages(b, store, accountID, folderID, 1402)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.GetUIDFlags(folderID); err != nil {
			b.Fatalf("GetUIDFlags: %v", err)
		}
	}
}

// BenchmarkGetAllUIDs measures the UID-only projection used to decide which
// messages still exist server-side.
func BenchmarkGetAllUIDs(b *testing.B) {
	store, accountID, folderID := newSyncBenchStore(b)
	seedBenchMessages(b, store, accountID, folderID, 1402)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.GetAllUIDs(folderID); err != nil {
			b.Fatalf("GetAllUIDs: %v", err)
		}
	}
}

// BenchmarkUpdateFlagsNoOp measures a flag write that changes nothing. With the
// v43 guarded FTS trigger this must not re-tokenize the body, which is the
// entire reason that trigger was rewritten.
func BenchmarkUpdateFlagsNoOp(b *testing.B) {
	store, accountID, folderID := newSyncBenchStore(b)
	seedBenchMessages(b, store, accountID, folderID, 200)

	cur, err := store.GetUIDFlags(folderID)
	if err != nil {
		b.Fatalf("GetUIDFlags: %v", err)
	}
	updates := make([]message.FlagUpdate, 0, len(cur))
	for uid, f := range cur {
		f.UID = uid
		updates = append(updates, f)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.UpdateFlagsByUIDBatch(folderID, updates); err != nil {
			b.Fatalf("UpdateFlagsByUIDBatch: %v", err)
		}
	}
}

// BenchmarkUpdateFlagsRealChange measures the same write when the flag actually
// flips, so the no-op numbers above cannot be mistaken for "flag writes are
// free".
func BenchmarkUpdateFlagsRealChange(b *testing.B) {
	store, accountID, folderID := newSyncBenchStore(b)
	seedBenchMessages(b, store, accountID, folderID, 200)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		read := i%2 == 0
		updates := make([]message.FlagUpdate, 0, 200)
		for uid := 1; uid <= 200; uid++ {
			updates = append(updates, message.FlagUpdate{
				UID:     uint32(uid),
				IsRead:  read,
				IsDraft: read,
			})
		}
		if err := store.UpdateFlagsByUIDBatch(folderID, updates); err != nil {
			b.Fatalf("UpdateFlagsByUIDBatch: %v", err)
		}
	}
}