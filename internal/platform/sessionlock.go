package platform

import "context"

// SessionLockMonitor reports whether the desktop session is locked.
//
// It exists for one reason (Phase 3 task 3-14): a notification whose body
// carries the subject and whose summary carries the sender is shown on the
// lock screen, leaking mail metadata to anyone standing at the machine. The
// caller uses Locked() to strip those fields.
//
// Implementations must be safe to call from any goroutine and must never
// block: Locked() returns the last known state.
type SessionLockMonitor interface {
	// Start begins monitoring. Returns an error when the platform offers no
	// lock signal; callers should degrade to "never locked" rather than fail.
	Start(ctx context.Context) error

	// Locked reports the last observed lock state.
	Locked() bool

	// Stop tears down the monitor.
	Stop() error
}
