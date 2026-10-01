package draft

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStaging(t *testing.T) *StagingStore {
	t.Helper()
	s := NewStagingStore(filepath.Join(t.TempDir(), "staging"))
	if s == nil {
		t.Fatal("NewStagingStore() returned nil")
	}
	return s
}

func TestStagingRoundTrip(t *testing.T) {
	s := newTestStaging(t)
	content := []byte("the quick brown fox\x00\xff binary tail")

	id, err := s.Put(content)
	if err != nil {
		t.Fatalf("Put() returned error: %v", err)
	}
	if len(id) != 64 {
		t.Errorf("Put() id length = %d, want 64 hex chars", len(id))
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Get() = %q, want %q", got, content)
	}
	if !s.Has(id) {
		t.Error("Has() = false for a staged id, want true")
	}
}

func TestStagingIDIsContentAddressed(t *testing.T) {
	s := newTestStaging(t)
	content := []byte("same bytes, same id")

	first, err := s.Put(content)
	if err != nil {
		t.Fatalf("first Put() returned error: %v", err)
	}
	second, err := s.Put(content)
	if err != nil {
		t.Fatalf("second Put() returned error: %v", err)
	}
	if first != second {
		t.Errorf("re-staging identical content gave %q then %q, want the same id", first, second)
	}

	other, err := s.Put([]byte("different bytes"))
	if err != nil {
		t.Fatalf("Put(different) returned error: %v", err)
	}
	if other == first {
		t.Error("different content produced the same staging id")
	}
}

func TestStagingNilStoreFailsSoftly(t *testing.T) {
	// A nil store must degrade to the legacy inline-base64 path rather than
	// panicking, so a composer on an unwritable data dir still works.
	var s *StagingStore
	if s.Has("") {
		t.Error("nil store Has() = true")
	}
	if _, err := s.Put([]byte("x")); err == nil {
		t.Error("nil store Put() succeeded, want error")
	}
	if _, err := s.Get(""); err == nil {
		t.Error("nil store Get() succeeded, want error")
	}
}

func TestStagingRejectsNonDigestID(t *testing.T) {
	s := newTestStaging(t)
	// A hostile id must never escape the staging directory.
	for _, id := range []string{
		"../../etc/passwd",
		strings.Repeat("a", 63),
		strings.Repeat("z", 64), // not hex
		"",
	} {
		if _, err := s.Get(id); err == nil {
			t.Errorf("Get(%q) succeeded, want error", id)
		}
	}
}

func TestStagingMissingID(t *testing.T) {
	s := newTestStaging(t)
	if _, err := s.Get(StagingID([]byte("never staged"))); err == nil {
		t.Error("Get() for an unstaged id succeeded, want error")
	}
}

func TestStagingRemove(t *testing.T) {
	s := newTestStaging(t)
	id, err := s.Put([]byte("temporary"))
	if err != nil {
		t.Fatalf("Put() returned error: %v", err)
	}
	if err := s.Remove(id); err != nil {
		t.Fatalf("Remove() returned error: %v", err)
	}
	if s.Has(id) {
		t.Error("Has() = true after Remove(), want false")
	}
	// Removing a well-formed but never-staged id is a no-op, not an error.
	if err := s.Remove(StagingID([]byte("never staged"))); err != nil {
		t.Errorf("Remove(absent) returned error: %v", err)
	}
	// A malformed id is rejected outright rather than treated as a path.
	if err := s.Remove("../../etc/passwd"); err == nil {
		t.Error("Remove(path traversal) succeeded, want error")
	}
}

func TestStagingFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "staging")
	s := NewStagingStore(dir)
	if s == nil {
		t.Fatal("NewStagingStore() returned nil")
	}
	id, err := s.Put([]byte("secret attachment bytes"))
	if err != nil {
		t.Fatalf("Put() returned error: %v", err)
	}

	// Staged bytes are plaintext user content: the file must not be
	// world-readable, matching the 0600 rule used for the database.
	info, err := os.Stat(filepath.Join(dir, id))
	if err != nil {
		t.Fatalf("Stat() returned error: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("staged file mode = %o, want 600", perm)
	}
}
