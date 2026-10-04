package message

import (
	"path/filepath"
	"testing"

	"github.com/beheoxinh/hsx2mail/internal/database"
)

func newConvSortStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "conv.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	accountID := "acct-conv"
	folderID := "folder-conv"
	if _, err := db.Exec(`INSERT INTO accounts (id, name, email, imap_host, smtp_host, username) VALUES (?, 'Test', ?, 'imap', 'smtp', ?)`, accountID, accountID+"@example.com", accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO folders (id, account_id, name, path, folder_type) VALUES (?, ?, 'INBOX', 'INBOX', 'inbox')`, folderID, accountID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	return NewStore(db), accountID, folderID
}
