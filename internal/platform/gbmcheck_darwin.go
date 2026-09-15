//go:build darwin

package platform

// MonitorGBMErrors is a no-op on macOS (Flatpak is Linux-only).
func MonitorGBMErrors() {}
