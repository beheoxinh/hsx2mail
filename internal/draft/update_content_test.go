package draft

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/database"
)

func newConflictStore(t *testing.T) (*Store, *database.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO accounts (id, name, email, imap_host, imap_port, smtp_host, smtp_port, auth_type, username)
		VALUES ('acct-1','A','a@example.com','h',993,'s',587,'password','a@example.com')`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return NewStore(db), db
}

// Consecutive autosaves from one editor must all succeed: the snapshot passed
// in is the row's own updated_at, so the guard must not reject the writer's
// own previous write.
func TestUpdateContent_ConsecutiveSavesSucceed(t *testing.T) {
	s, _ := newConflictStore(t)

	d := &Draft{AccountID: "acct-1", Subject: "v1", ToList: `[{"email":"a@example.com","name":"A"}]`, CcList: "[]", BccList: "[]"}
	if err := s.Create(d); err != nil {
		t.Fatalf("create: %v", err)
	}

	for i := 2; i <= 5; i++ {
		prev := d.UpdatedAt
		d.Subject = "v" + string(rune('0'+i))
		if err := s.UpdateContent(d, prev); err != nil {
			t.Fatalf("save %d rejected: %v", i, err)
		}
	}

	var subject string
	if err := s.db.QueryRow(`SELECT subject FROM drafts WHERE id = ?`, d.ID).Scan(&subject); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if subject != "v5" {
		t.Errorf("subject = %q, want v5", subject)
	}
}

// A save whose snapshot predates another writer's commit must be refused.
func TestUpdateContent_StaleSnapshotIsRejected(t *testing.T) {
	s, _ := newConflictStore(t)

	d := &Draft{AccountID: "acct-1", Subject: "original", ToList: `[{"email":"a@example.com","name":"A"}]`, CcList: "[]", BccList: "[]"}
	if err := s.Create(d); err != nil {
		t.Fatalf("create: %v", err)
	}

	// This editor read the draft at this point in time...
	staleSnapshot := d.UpdatedAt

	// ...and then the other window (a separate process) saves a newer version.
	newer := d.UpdatedAt.Add(5 * time.Second)
	if _, err := s.db.DB.Exec(`UPDATE drafts SET subject = 'from other window', updated_at = ? WHERE id = ?`,
		newer.Format(time.RFC3339Nano), d.ID); err != nil {
		t.Fatalf("simulate other window: %v", err)
	}

	d.Subject = "mine"
	err := s.UpdateContent(d, staleSnapshot)
	if !errors.Is(err, ErrDraftConflict) {
		t.Fatalf("expected ErrDraftConflict, got %v", err)
	}

	var subject string
	if err := s.db.QueryRow(`SELECT subject FROM drafts WHERE id = ?`, d.ID).Scan(&subject); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if subject != "from other window" {
		t.Errorf("stale save clobbered the newer content: subject = %q", subject)
	}
}

// The guard must not depend on the writer's timezone: updated_at is stored with
// a UTC offset, so a text comparison would misorder writes from windows running
// under different TZ.
func TestUpdateContent_GuardIsTimezoneIndependent(t *testing.T) {
	s, db := newConflictStore(t)

	d := &Draft{AccountID: "acct-1", Subject: "v1", ToList: `[{"email":"a@example.com","name":"A"}]`, CcList: "[]", BccList: "[]"}
	if err := s.Create(d); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A window in a very different zone writes the same instant, expressed with
	// a +14:00 offset instead of the local one.
	snapshot := d.UpdatedAt
	utc := snapshot.UTC()
	if _, err := db.Exec(`UPDATE drafts SET updated_at = ? WHERE id = ?`,
		utc.In(time.FixedZone("east14", 14*60*60)).Format(time.RFC3339Nano), d.ID); err != nil {
		t.Fatalf("write far-east timestamp: %v", err)
	}

	d.Subject = "v2"
	if err := s.UpdateContent(d, snapshot); err != nil {
		t.Fatalf("save after an equal-instant write in another zone was rejected: %v", err)
	}
}
