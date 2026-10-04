package database

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestFTSUpdateTriggerSkipsFlagWrites pins the v43 behaviour. The trigger used
// to fire on every UPDATE, so flipping is_read re-indexed the message's whole
// text: two FTS operations per flag change on every sync cycle. The guarded
// trigger fires only when an indexed column actually changed.
func TestFTSUpdateTriggerSkipsFlagWrites(t *testing.T) {
	db := openTestDB(t)

	var def string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'messages_fts_update'`,
	).Scan(&def)
	if err != nil {
		t.Fatalf("read messages_fts_update definition: %v", err)
	}
	if !strings.Contains(strings.ToUpper(def), "WHEN OLD.") {
		t.Fatalf("messages_fts_update is unguarded; a flag write would re-index FTS text:\n%s", def)
	}
	// Every indexed column must appear in the guard, otherwise editing one of
	// them silently leaves the FTS index stale.
	for _, col := range []string{
		"subject", "from_name", "from_email", "to_list", "cc_list", "snippet", "body_text",
	} {
		want := "OLD." + col + " IS NOT NEW." + col
		if !strings.Contains(def, want) {
			t.Errorf("guard missing %q — updating %s would leave FTS stale", want, col)
		}
	}
}

// TestFTSUpdateTriggerStillIndexesTextChanges is the counterweight: the guard
// must not suppress real content edits, or search would silently go stale.
func TestFTSUpdateTriggerStillIndexesTextChanges(t *testing.T) {
	db := openTestDB(t)

	// Seed a message, then change only its subject.
	const accountID = "acct-fts"
	const folderID = "folder-fts"
	seedAccountAndFolder(t, db, accountID, folderID)

	_, err := db.Exec(`
		INSERT INTO messages (account_id, folder_id, uid, message_id, subject, from_email, date)
		VALUES (?, ?, 1, 'fts-1@example.com', 'Original Subject', 'a@example.com', CURRENT_TIMESTAMP)
	`, accountID, folderID)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}

	if n := matchCount(t, db, "Original"); n == 0 {
		t.Fatal("FTS index did not receive the insert")
	}

	if _, err := db.Exec(`UPDATE messages SET subject = 'Rewritten Subject' WHERE uid = 1`); err != nil {
		t.Fatalf("update subject: %v", err)
	}

	if n := matchCount(t, db, "Original"); n != 0 {
		t.Errorf("stale FTS row survived a subject change: %d matches for 'Original'", n)
	}
	if n := matchCount(t, db, "Rewritten"); n == 0 {
		t.Error("subject change did not reach the FTS index")
	}
}

// TestFTSUpdateTriggerSkipsFlagOnlyChange is the performance guard: a flag-only
// UPDATE must leave the FTS row count unchanged (no delete+reinsert churn).
func TestFTSUpdateTriggerSkipsFlagOnlyChange(t *testing.T) {
	db := openTestDB(t)

	const accountID = "acct-flag"
	const folderID = "folder-flag"
	seedAccountAndFolder(t, db, accountID, folderID)

	_, err := db.Exec(`
		INSERT INTO messages (account_id, folder_id, uid, message_id, subject, from_email, date, body_text)
		VALUES (?, ?, 1, 'flag-1@example.com', 'Subject', 'a@example.com', CURRENT_TIMESTAMP, 'body text here')
	`, accountID, folderID)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}

	if _, err := db.Exec(`UPDATE messages SET is_read = 1 WHERE uid = 1`); err != nil {
		t.Fatalf("flag update: %v", err)
	}
	if n := matchCount(t, db, "body"); n != 1 {
		t.Fatalf("flag write disturbed the FTS index: %d matches, want 1", n)
	}
}

// matchCount runs an FTS MATCH and returns the number of rows.
func matchCount(t *testing.T, db *DB, term string) int {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?`, term).Scan(&n)
	if err != nil {
		t.Fatalf("MATCH %q: %v", term, err)
	}
	return n
}

// seedAccountAndFolder inserts the minimum rows for messages to reference.
func seedAccountAndFolder(t *testing.T, db *DB, accountID, folderID string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unused") // keep filepath import honest if reused
	_ = path
	if _, err := db.Exec(
		`INSERT INTO accounts (id, name, email, imap_host, smtp_host, username)
		 VALUES (?, 'Test', ?, 'imap.example.com', 'smtp.example.com', ?)`,
		accountID, accountID+"@example.com", accountID,
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO folders (id, account_id, name, path, folder_type)
		 VALUES (?, ?, 'INBOX', 'INBOX', 'inbox')`,
		folderID, accountID,
	); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
}