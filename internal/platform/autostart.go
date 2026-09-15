package platform

// AutostartManager manages application autostart on login.
type AutostartManager interface {
	// Enable creates the autostart entry.
	Enable() error
	// Disable removes the autostart entry.
	Disable() error
	// IsEnabled checks if autostart is currently configured.
	IsEnabled() bool
}
