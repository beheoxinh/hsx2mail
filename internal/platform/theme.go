package platform

import "context"

// SystemTheme represents the system's preferred color scheme
type SystemTheme string

const (
	SystemThemeLight        SystemTheme = "light"
	SystemThemeDark         SystemTheme = "dark"
	SystemThemeNoPreference SystemTheme = "" // No preference or unavailable
)

// ThemeMonitor monitors system theme preference changes
type ThemeMonitor interface {
	// Start begins monitoring for theme changes
	Start(ctx context.Context) error

	// GetTheme returns the current system theme preference.
	// Returns SystemThemeNoPreference if unavailable.
	GetTheme() SystemTheme

	// Events returns a channel that receives theme change events
	Events() <-chan SystemTheme

	// Stop stops the monitor and cleans up resources
	Stop() error
}
