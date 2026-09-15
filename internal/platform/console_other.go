//go:build !windows

package platform

// AttachConsole is a no-op on non-Windows platforms.
func AttachConsole() {}
