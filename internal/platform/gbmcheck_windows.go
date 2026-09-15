//go:build windows

package platform

// MonitorGBMErrors is a no-op on Windows (Flatpak is Linux-only).
func MonitorGBMErrors() {}
