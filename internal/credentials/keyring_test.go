package credentials

import (
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	gokeyring "github.com/zalando/go-keyring"
)

func testLogger() zerolog.Logger { return zerolog.Nop() }

// fakeKeyring installs an in-memory keyring (or a failing one) for the duration
// of a test and restores the real gokeyring calls afterwards.
type fakeKeyring struct {
	mu     sync.Mutex
	vals   map[string]string
	setErr error
	getErr error
}

func installFakeKeyring(t *testing.T, f *fakeKeyring) {
	t.Helper()
	prevSet, prevGet, prevDel := keyringSetFn, keyringGetFn, keyringDeleteFn
	keyringSetFn = f.set
	keyringGetFn = f.get
	keyringDeleteFn = f.del
	keyringLive.Store(true)
	resetDegraded()
	t.Cleanup(func() {
		keyringSetFn, keyringGetFn, keyringDeleteFn = prevSet, prevGet, prevDel
		keyringLive.Store(false)
		resetDegraded()
	})
}

func (f *fakeKeyring) set(_, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	if f.vals == nil {
		f.vals = map[string]string{}
	}
	f.vals[key] = value
	return nil
}

func (f *fakeKeyring) get(_, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.vals[key]
	if !ok {
		return "", gokeyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeKeyring) del(_, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.vals, key)
	return nil
}

func (f *fakeKeyring) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.vals[key]
	return ok
}

// TestKeyringWriteFailureDoesNotFallBackToDB covers 5-04: a keyring that fails
// mid-session must fail the write loudly, not quietly copy the secret into the
// SQLite file.
func TestKeyringWriteFailureDoesNotFallBackToDB(t *testing.T) {
	s := newStore(t)
	installFakeKeyring(t, &fakeKeyring{setErr: errors.New("secret service locked")})

	err := s.SetPassword("acct-1", "hunter2")
	if err == nil {
		t.Fatal("SetPassword succeeded despite a live keyring failure")
	}
	if !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("error = %v, want ErrKeyringUnavailable", err)
	}
	if got := s.KeyringDegraded(); got == "" {
		t.Fatal("KeyringDegraded() is empty: the failure was swallowed")
	}
	// The whole point: nothing was quietly written into SQLite.
	if v := dbColumn(t, s, "encrypted_password", "acct-1"); v.Valid && v.String != "" {
		t.Fatalf("password was silently copied into the DB fallback: %q", v.String)
	}
	if v, gerr := keyringGetFn(serviceName, "acct-1"); gerr == nil {
		t.Fatalf("password landed in the keyring despite Set reporting failure: %q", v)
	}
}

// TestKeyringReadFailureIsSurfaced covers the read half: a locked keyring must
// not be answered from the DB copy, because "locked" and "absent" differ.
func TestKeyringReadFailureIsSurfaced(t *testing.T) {
	s := newStore(t)
	// Both calls fail: the probe that distinguishes "locked" from "absent"
	// uses Set, so a Get-only failure would look like an empty keyring.
	installFakeKeyring(t, &fakeKeyring{
		getErr: errors.New("secret service locked"),
		setErr: errors.New("secret service locked"),
	})

	if _, err := s.GetPassword("acct-1"); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("GetPassword error = %v, want ErrKeyringUnavailable", err)
	}
	if got := s.KeyringDegraded(); got == "" {
		t.Fatal("KeyringDegraded() is empty: the read failure was swallowed")
	}
}

// TestKeyringReadFailureNeverYieldsSecret asserts the no-leak property across
// every OAuth token read: a broken keyring yields no token at all rather than
// silently substituting the encrypted-DB copy.
func TestKeyringReadFailureNeverYieldsSecret(t *testing.T) {
	s := newStore(t)
	installFakeKeyring(t, &fakeKeyring{
		getErr: errors.New("secret service locked"),
		setErr: errors.New("secret service locked"),
	})

	reads := map[string]func() (string, error){
		"access_token":  func() (string, error) { return s.getOAuthAccessToken("acct-1") },
		"refresh_token": func() (string, error) { return s.getOAuthRefreshToken("acct-1") },
		"smtp_password": func() (string, error) { return s.GetSMTPPassword("acct-1") },
		"carddav":       func() (string, error) { return s.GetCardDAVPassword("acct-1") },
	}
	for name, read := range reads {
		v, err := read()
		if v != "" {
			t.Errorf("%s returned a value while the keyring was broken", name)
		}
		if err == nil {
			t.Errorf("%s returned no error while the keyring was broken", name)
		}
	}
}

// TestKeyringTransientFailureDoesNotLatch proves the store re-probes: after a
// single failed write the next write still goes to the keyring, not the DB.
func TestKeyringTransientFailureDoesNotLatch(t *testing.T) {
	s := newStore(t)
	kr := &fakeKeyring{}
	installFakeKeyring(t, kr)

	// One transient failure: the probe that follows it also fails, so the write
	// is refused.
	kr.mu.Lock()
	kr.setErr = errors.New("transient")
	kr.mu.Unlock()
	if err := s.SetPassword("acct-1", "x"); !errors.Is(err, ErrKeyringUnavailable) {
		t.Fatalf("expected refusal during the outage, got %v", err)
	}

	// Keyring recovers; the next write must reach it rather than latching to
	// the DB-only mode. Force the rate-limited re-probe to run now.
	kr.mu.Lock()
	kr.setErr = nil
	kr.mu.Unlock()
	lastProbe.Store(0)
	if err := s.SetPassword("acct-1", "x"); err != nil {
		t.Fatalf("write after recovery failed: %v", err)
	}
	if !kr.has("acct-1") {
		t.Fatal("secret never reached the keyring after recovery")
	}
}

// TestKeyringDisabledUsesDBFallback keeps the documented DB-only mode working.
func TestKeyringDisabledUsesDBFallback(t *testing.T) {
	s := newStore(t)
	installFakeKeyring(t, &fakeKeyring{})
	keyringLive.Store(false)

	// keyringSet must report "not used, no error" so callers keep the DB path.
	used, err := s.keyringSet("k", "v")
	if err != nil {
		t.Fatalf("keyringSet in DB-only mode errored: %v", err)
	}
	if used {
		t.Fatal("keyringSet reported a write in DB-only mode")
	}
	if got := s.KeyringDegraded(); got != "" {
		t.Fatalf("DB-only mode must not latch a degradation, got %q", got)
	}
}

// newStore builds a real Store on a temp DB so the DB-fallback column can be
// asserted, not just the returned error.
func newStore(t *testing.T) *Store {
	t.Helper()
	return openTestStore(t)
}

// dbColumn returns the raw value of an accounts-table fallback column.
func dbColumn(t *testing.T, s *Store, column, accountID string) sql.NullString {
	t.Helper()
	var v sql.NullString
	//nolint:gosec // column name comes from a fixed literal in this test file.
	switch err := s.db.QueryRow("SELECT "+column+" FROM accounts WHERE id = ?", accountID).Scan(&v); {
	case errors.Is(err, sql.ErrNoRows):
		return sql.NullString{}
	case err != nil:
		t.Fatalf("query %s: %v", column, err)
	}
	return v
}
