package database

import (
	"path/filepath"
	"testing"
)

// The app depends on foreign_keys for the attachment/message relationship
// (internal/message). If the pragma were off, an orphan attachment row would be
// accepted silently. This asserts it through database.Open, i.e. the exact DSN
// the application uses -- a `PRAGMA foreign_keys` from the sqlite3 CLI reads 0
// regardless, because the pragma is per connection.
func TestForeignKeysEnabledOnAppConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if fk != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1", fk)
	}
}
