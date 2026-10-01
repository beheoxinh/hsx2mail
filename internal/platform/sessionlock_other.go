//go:build !linux

package platform

import "context"

// nonLinuxSessionLockMonitor is the no-op used where no session-lock signal
// is wired up yet. Reporting "not locked" keeps notifications verbatim
// instead of silently hiding them — the safe default for a feature that
// exists only to redact.
type nonLinuxSessionLockMonitor struct{}

func NewSessionLockMonitor() SessionLockMonitor { return nonLinuxSessionLockMonitor{} }

func (nonLinuxSessionLockMonitor) Start(context.Context) error { return nil }

func (nonLinuxSessionLockMonitor) Locked() bool { return false }

func (nonLinuxSessionLockMonitor) Stop() error { return nil }
