//go:build !linux

package platform

// NotifyStartupComplete is a no-op on non-Linux platforms.
func NotifyStartupComplete() {}
