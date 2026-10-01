package credentials

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	gokeyring "github.com/zalando/go-keyring"
)

// ErrKeyringUnavailable is returned when the OS keyring was reachable but a
// runtime operation failed (locked keyring, Secret Service restarting, ...).
// Callers must fail the write rather than silently copying the secret into the
// encrypted DB: that migration is invisible to the user, the DB copy is never
// migrated back, and the process keeps using it for the rest of the session.
var ErrKeyringUnavailable = errors.New("OS keyring unavailable")

// keyringLive is true when the OS keyring is believed usable. It is set by the
// startup probe and re-probed after a runtime failure, so a keyring that comes
// back (screen unlock, Secret Service restart) is picked up instead of latched
// off for the process lifetime.
var keyringLive atomic.Bool

// Keyring calls are indirected so the fail-closed branch is testable without a
// real Secret Service. ponytail: swap for gokeyring.* directly if the real
// keyring ever gains a hermetic test backend.
var (
	keyringSetFn    = gokeyring.Set
	keyringGetFn    = gokeyring.Get
	keyringDeleteFn = gokeyring.Delete
)

// probeMu serializes the Set/Delete probe round-trip so a burst of failures
// does not hammer the Secret Service.
var probeMu sync.Mutex

// probeInterval rate-limits re-probing while the keyring is believed down.
// It bounds the cost of recovery to one Set/Delete round trip per interval
// instead of one per credential operation.
const probeInterval = 30 * time.Second

// lastProbe is the unix-nano time of the most recent probe round-trip.
var lastProbe atomic.Int64

// degraded records the first degradation reason so it can be surfaced instead
// of vanishing into a WARN log line.
var (
	degradedMu sync.Mutex
	degraded   string
)

// resetDegraded clears the latched degradation reason. Test-only.
func resetDegraded() {
	degradedMu.Lock()
	degraded = ""
	degradedMu.Unlock()
}

// keyringEnabledNow reports whether keyring writes should be attempted.
func keyringEnabledNow() bool { return keyringLive.Load() }

// probeKeyring does a real Set/Delete round trip and updates keyringLive.
func probeKeyring() error {
	probeMu.Lock()
	defer probeMu.Unlock()
	defer lastProbe.Store(time.Now().UnixNano())

	const testKey = "hsx2mail-test-keyring-check"
	if err := keyringSetFn(serviceName, testKey, "test"); err != nil {
		keyringLive.Store(false)
		return err
	}
	_ = keyringDeleteFn(serviceName, testKey)
	keyringLive.Store(true)
	return nil
}

// maybeReprobe re-runs the probe when the keyring is believed down and the last
// probe is at least probeInterval old. This is what lets a keyring that was
// locked at startup (or locked mid-session and then unlocked) come back without
// restarting the app, instead of latching off for the process lifetime.
func maybeReprobe() {
	if keyringLive.Load() {
		return
	}
	if time.Since(time.Unix(0, lastProbe.Load())) < probeInterval {
		return
	}
	_ = probeKeyring()
}

// markDegraded latches a human-readable reason the first time a write had to be
// refused because the keyring failed mid-session.
func markDegraded(reason string) {
	degradedMu.Lock()
	defer degradedMu.Unlock()
	if degraded == "" {
		degraded = reason
	}
}

// KeyringDegraded reports whether a credential write was refused because the
// OS keyring failed mid-session, and why. Empty means the keyring has been
// healthy for the whole run (or was never available at startup, which is a
// deliberate configuration, not a failure).
func (s *Store) KeyringDegraded() string {
	degradedMu.Lock()
	defer degradedMu.Unlock()
	return degraded
}

// keyringSet stores a secret in the OS keyring.
//
// It returns nil on success. It returns ErrKeyringUnavailable (and does NOT
// touch the DB) when the keyring was live and then failed, so the secret is
// neither leaked into the DB nor silently dropped. Callers decide whether to
// surface the error to the user.
//
// When the keyring was never available, this returns (false, nil): the store is
// in its documented DB-only mode and the caller uses encrypted DB storage.
func (s *Store) keyringSet(key, value string) (used bool, err error) {
	maybeReprobe()
	if !keyringEnabledNow() {
		return false, nil
	}

	kerr := keyringSetFn(serviceName, key, value)
	if kerr == nil {
		return true, nil
	}

	// Don't trust a single failure: a transient error (GNOME screen lock,
	// Secret Service restart) may have cleared. Re-probe, then decide.
	if perr := probeKeyring(); perr != nil {
		markDegraded(kerr.Error())
		s.log.Error().Err(kerr).
			Str("key", key).
			Msg("OS keyring write failed and is still unavailable — credential NOT stored (refusing encrypted-DB fallback)")
		return false, ErrKeyringUnavailable
	}

	s.log.Warn().Err(kerr).
		Str("key", key).
		Msg("OS keyring write failed but re-probe succeeded — credential NOT stored")
	return false, ErrKeyringUnavailable
}

// keyringGet reads a secret from the OS keyring.
//
// Returns (value, true, nil) on a hit. (false, nil) when the keyring is
// disabled or the entry is simply absent. (false, ErrKeyringUnavailable) when
// the keyring is live but errored — the caller must not silently substitute the
// DB copy in that case, because it cannot tell "locked" from "empty".
func (s *Store) keyringGet(key string) (string, bool, error) {
	maybeReprobe()
	if !keyringEnabledNow() {
		return "", false, nil
	}

	value, kerr := keyringGetFn(serviceName, key)
	if kerr == nil {
		return value, true, nil
	}
	if errors.Is(kerr, gokeyring.ErrNotFound) {
		return "", false, nil
	}

	// A read error is not the same as "not stored": the entry may exist behind
	// a locked keyring. Re-probe to tell the two apart.
	if perr := probeKeyring(); perr != nil {
		markDegraded(kerr.Error())
		s.log.Error().Err(kerr).
			Str("key", key).
			Msg("OS keyring read failed and is still unavailable — refusing DB fallback")
		return "", false, ErrKeyringUnavailable
	}

	s.log.Warn().Err(kerr).
		Str("key", key).
		Msg("OS keyring read failed but re-probe succeeded")
	return "", false, nil
}

// keyringDelete removes a secret from the OS keyring. Missing entries are not
// an error: deletion must succeed even when the keyring is unreachable so the
// DB copy still gets cleared.
func (s *Store) keyringDelete(key string) {
	maybeReprobe()
	if !keyringEnabledNow() {
		return
	}
	if err := keyringDeleteFn(serviceName, key); err != nil && !errors.Is(err, gokeyring.ErrNotFound) {
		s.log.Warn().Err(err).Str("key", key).Msg("OS keyring delete failed")
	}
}
