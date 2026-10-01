package draft

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beheoxinh/hsx2mail/internal/database"
)

func newStagingRefStore(t *testing.T) (*Store, *database.DB) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO accounts (id, name, email, imap_host, imap_port, smtp_host, smtp_port, auth_type, username)
		VALUES ('a1','A','a@example.com','h',993,'s',587,'password','a@example.com')`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return NewStore(db), db
}

func draftWithStagingID(t *testing.T, s *Store, id string) {
	t.Helper()
	d := &Draft{
		AccountID: "a1", Subject: "s",
		ToList: "[]", CcList: "[]", BccList: "[]",
		AttachmentsData: []byte(`[{"staging_id":"` + id + `","filename":"a.txt","content_type":"text/plain","size":5}]`),
	}
	if err := s.Create(d); err != nil {
		t.Fatalf("create draft: %v", err)
	}
}

// attachments_data is a []byte, so it is stored as a BLOB. A naive LIKE against
// it silently matches nothing, which would make the sweep delete every blob.
func TestStagingReferencedChecker_DetectsReferenceInBlobColumn(t *testing.T) {
	s, _ := newStagingRefStore(t)
	checker := StagingReferencedChecker{Store: s}

	id := StagingID([]byte("shared attachment bytes"))
	draftWithStagingID(t, s, id)

	referenced, err := checker.IsReferenced(id)
	if err != nil {
		t.Fatalf("IsReferenced: %v", err)
	}
	if !referenced {
		t.Fatal("a draft referencing the id was reported unreferenced")
	}

	other, err := checker.IsReferenced(StagingID([]byte("nothing here")))
	if err != nil {
		t.Fatalf("IsReferenced: %v", err)
	}
	if other {
		t.Fatal("an id no draft references was reported referenced")
	}
}

// The sweep must keep a blob that is older than the cutoff but still referenced,
// and must drop one nothing points at.
func TestSweepOlderThan_KeepsReferencedBlob(t *testing.T) {
	s, _ := newStagingRefStore(t)
	staging := NewStagingStore(t.TempDir())
	checker := StagingReferencedChecker{Store: s}

	liveID, err := staging.Put([]byte("still attached"))
	if err != nil {
		t.Fatalf("put live: %v", err)
	}
	orphanID, err := staging.Put([]byte("no draft left"))
	if err != nil {
		t.Fatalf("put orphan: %v", err)
	}
	draftWithStagingID(t, s, liveID)

	// Age both blobs well past the cutoff.
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, id := range []string{liveID, orphanID} {
		touchForTest(t, staging, id, old)
	}

	removed, err := staging.SweepOlderThan(StagingRetention, checker.IsReferenced)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (only the orphan)", removed)
	}
	if _, err := staging.Get(liveID); err != nil {
		t.Errorf("referenced blob was swept: %v", err)
	}
	if _, err := staging.Get(orphanID); err == nil {
		t.Error("unreferenced blob was not swept")
	}
}

// Re-staging the same bytes must refresh the mtime, otherwise a blob used
// daily still ages out on the day it was first written.
func TestPut_RefreshesMtimeForExistingBlob(t *testing.T) {
	staging := NewStagingStore(t.TempDir())
	id, err := staging.Put([]byte("reused"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	aged := time.Now().Add(-30 * 24 * time.Hour)
	touchForTest(t, staging, id, aged)
	again, err := staging.Put([]byte("reused"))
	if err != nil {
		t.Fatalf("re-put: %v", err)
	}
	if again != id {
		t.Fatalf("re-put returned a different id: %q vs %q", again, id)
	}
	removed, err := staging.SweepOlderThan(StagingRetention, nil)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if removed != 0 {
		t.Errorf("a re-staged blob was swept (removed=%d)", removed)
	}
}

// touchForTest backdates a staged blob so the sweep's age check can be
// exercised without waiting a week. Test-only: production never rewrites mtimes.
func touchForTest(t *testing.T, s *StagingStore, id string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(s.dir, id), when, when); err != nil {
		t.Fatalf("touch %s: %v", id, err)
	}
}
