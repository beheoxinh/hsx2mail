package draft

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StagingStore keeps attachment bytes in the Go process so the composer IPC
// payload only has to carry a staging id plus metadata instead of the full
// base64 body of every file.
//
// The id is the content's SHA-256 hex digest. That makes staging idempotent
// (the same file staged twice reuses one copy) and makes the id reusable
// across a save/load cycle: a draft persisted with staging ids resolves to
// the same bytes after a reload without the caller ever sending them again.
// The digest is 32 bytes of entropy, so ids are not guessable and cannot be
// used to probe another draft's attachments.
type StagingStore struct {
	dir string

	mu    sync.RWMutex
	files map[string]string // staging id -> absolute path
}

// NewStagingStore creates a staging store rooted at dir, creating the
// directory if needed. Returns nil when the directory cannot be created, so
// callers can degrade to inline base64 rather than failing to compose.
// StagingRetention is how long an unreferenced staged attachment blob is kept
// before the startup sweep removes it. Long enough that reopening a draft from
// the last few days always finds its bytes, short enough that the directory
// cannot grow without bound.
const StagingRetention = 7 * 24 * time.Hour

func NewStagingStore(dir string) *StagingStore {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	return &StagingStore{dir: dir, files: map[string]string{}}
}

// StagingID computes the id Put will assign to content. Exposed so callers
// can reuse an id for content they already hold instead of re-staging it.
func StagingID(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Put stores content and returns its staging id. Storing content that is
// already staged is a no-op and returns the existing id.
func (s *StagingStore) Put(content []byte) (string, error) {
	if s == nil {
		return "", fmt.Errorf("staging store not initialized")
	}
	id := StagingID(content)

	s.mu.RLock()
	path, ok := s.files[id]
	s.mu.RUnlock()
	if ok {
		// Refresh the mtime. The id is content-addressed, so a blob that is
		// re-staged every time a draft is reopened would otherwise keep the
		// mtime of its very first write and be reclaimed by SweepOlderThan
		// while live drafts still reference it.
		_ = os.Chtimes(path, time.Now(), time.Now())
		return id, nil
	}

	// Write to a unique temp name then rename: two windows staging the same
	// bytes concurrently must not observe a half-written file.
	tmp, err := os.CreateTemp(s.dir, ".stage-*")
	if err != nil {
		return "", fmt.Errorf("failed to create staging temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return "", fmt.Errorf("failed to write staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("failed to close staging file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return "", fmt.Errorf("failed to set staging file mode: %w", err)
	}

	path = filepath.Join(s.dir, id)
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("failed to publish staging file: %w", err)
	}

	s.mu.Lock()
	s.files[id] = path
	s.mu.Unlock()

	return id, nil
}

// Get returns the bytes for a staging id.
func (s *StagingStore) Get(id string) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("staging store not initialized")
	}
	if id == "" {
		return nil, fmt.Errorf("empty staging id")
	}
	// The id is a hex digest, never a path. Reject anything else so a
	// hostile or corrupted id cannot escape the staging directory.
	if !validStagingID(id) {
		return nil, fmt.Errorf("invalid staging id")
	}
	return os.ReadFile(filepath.Join(s.dir, id))
}

// Has reports whether content for id is staged.
func (s *StagingStore) Has(id string) bool {
	if s == nil {
		return false
	}
	if _, err := s.Get(id); err != nil {
		return false
	}
	return true
}

// Remove deletes staged content. Safe to call for an id that is not staged.
func (s *StagingStore) Remove(id string) error {
	if s == nil || id == "" {
		return nil
	}
	if !validStagingID(id) {
		return fmt.Errorf("invalid staging id")
	}
	s.mu.Lock()
	delete(s.files, id)
	s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.dir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// SweepOlderThan deletes staged blobs whose file modification time is older
// than maxAge AND that `referenced` reports as unreferenced, and returns how
// many were removed. Pass a nil predicate only when you know nothing else can
// point at these files.
//
// The composer stages attachment bytes on disk so they stop crossing the Wails
// IPC bridge on every autosave. Nothing deletes them at send time (the same
// draft can be reopened), so without a sweep the directory grows without bound.
// An orphan is always recoverable: a draft referencing a missing staging id
// falls back to inline content, and a re-opened composer re-stages the file.
func (s *StagingStore) SweepOlderThan(maxAge time.Duration, referenced func(id string) (bool, error)) (int, error) {
	if s == nil {
		return 0, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read staging dir: %w", err)
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !validStagingID(name) {
			// Not ours; leave unknown files alone.
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		// Content-addressed blobs are shared: two drafts with the same bytes
		// have the same id. Age alone says nothing about whether a live draft
		// still points at it, so the caller decides.
		if referenced != nil {
			keep, err := referenced(name)
			if err != nil {
				// Cannot prove it is unreferenced, so keep it. Leaking a file
				// costs disk; deleting a live attachment costs data.
				continue
			}
			if keep {
				continue
			}
		}
		if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		removed++
	}
	return removed, nil
}

// validStagingID reports whether id is a 64-character hex digest. The id is
// always a path segment, so anything else must be rejected before it is used.
func validStagingID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// StagingReferencedChecker reports whether any stored draft still points at a
// staging id. It is the predicate SweepOlderThan needs, because the blob
// namespace is content-addressed and shared between drafts.
type StagingReferencedChecker struct {
	Store *Store
}

// IsReferenced reports whether any draft row's attachment payload mentions the
// id. attachments_data is a JSON array of attachment objects, so the id is
// located by its JSON key rather than by a LIKE on a bare hash.
func (c StagingReferencedChecker) IsReferenced(id string) (bool, error) {
	if c.Store == nil || c.Store.db == nil {
		return true, nil // Unknown: treat as referenced so nothing is deleted.
	}
	var count int
	// attachments_data is a []byte, so the driver binds it as a BLOB and a
	// LIKE against it can never match — cast to TEXT first. instr over a 64-char
	// hex digest avoids the LIKE wildcards entirely, and a false positive would
	// require another digest to contain this one as a substring.
	if err := c.Store.db.QueryRow(
		`SELECT COUNT(*) FROM drafts WHERE instr(CAST(attachments_data AS TEXT), ?) > 0`, id,
	).Scan(&count); err != nil {
		return true, err
	}
	return count > 0, nil
}
