package database

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWALHousekeeping_WalStaysBounded proves the v42-era housekeeping actually
// reclaims the -wal file: after a burst of writes the TRUNCATE checkpoint
// routine must leave the file small, not at its high-water mark.
func TestWALHousekeeping_WalStaysBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO accounts (id, name, email, imap_host, smtp_host, username)
		 VALUES ('a1', 'A', 'a@example.com', 'imap.example.com', 'smtp.example.com', 'a')`); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	// A write burst far larger than the checkpoint interval.
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for i := 0; i < 20000; i++ {
		if _, err := tx.Exec(
			`INSERT INTO settings (key, value) VALUES (?, ?)`,
			"k"+itoa(i), "value-padding-padding-padding-padding-padding"); err != nil {
			_ = tx.Rollback()
			t.Fatalf("bulk insert %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	peak := fileSize(filepath.Join(path + "-wal"))
	if err := db.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	after := fileSize(filepath.Join(path + "-wal"))

	// TRUNCATE resets the file to zero bytes. PASSIVE would leave it at the
	// high-water size, which is the growth this task exists to stop.
	if after != 0 {
		t.Errorf("after TRUNCATE checkpoint the -wal file is %d bytes (peak was %d); PASSIVE-style "+
			"behaviour would leave it at the high-water mark", after, peak)
	}
	if peak == 0 {
		t.Skip("WAL file was already empty at peak; nothing to reclaim in this environment")
	}

	if err := db.Optimize(); err != nil {
		t.Fatalf("Optimize: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
