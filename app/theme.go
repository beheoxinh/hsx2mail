package app

import (
	"context"

	"github.com/beheoxinh/hsx2mail/internal/logging"
	"github.com/beheoxinh/hsx2mail/internal/platform"
	"github.com/beheoxinh/hsx2mail/internal/settings"
	"github.com/beheoxinh/hsx2mail/internal/tray"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// initThemeMonitor initializes the system theme monitor for portal-based theme detection.
// On Linux, this uses the XDG Settings Portal. On other platforms, it's a no-op
// and the frontend falls back to matchMedia.
func (a *App) initThemeMonitor(ctx context.Context) {
	log := logging.WithComponent("app.theme")

	a.themeMonitor = platform.NewThemeMonitor()

	// The tray icon has to follow the same colour scheme as the rest of the UI, so
	if err := a.themeMonitor.Start(ctx); err != nil {
		log.Debug().Err(err).Msg("System theme monitor not available, frontend will use matchMedia fallback")
		a.themeMonitor = nil
		return
	}

	// Emit initial theme value so the frontend can use it immediately
	initialTheme := a.themeMonitor.GetTheme()
	if initialTheme != platform.SystemThemeNoPreference {
		wailsRuntime.EventsEmit(ctx, "theme:system-preference", string(initialTheme))
	}

	// Only now does GetTheme() hold a value: the monitor reads the current
	// preference inside Start. The tray icon follows the same colour scheme as
	// the rest of the UI and is seeded from this.
	a.syncTrayScheme()

	go a.processThemeEvents(ctx)

	log.Info().Msg("System theme monitor initialized")
}

// processThemeEvents listens for system theme changes and emits events to the frontend
func (a *App) processThemeEvents(ctx context.Context) {
	defer recoverPanic("app.theme", "process theme events")
	if a.themeMonitor == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case theme, ok := <-a.themeMonitor.Events():
			if !ok {
				return
			}
			a.syncTrayScheme()
			wailsRuntime.EventsEmit(a.ctx, "theme:system-preference", string(theme))
		}
	}
}

// GetSystemTheme returns the current system theme preference detected via
// the XDG Settings Portal on Linux. Returns "light", "dark", or "" if not available.
func (a *App) GetSystemTheme() string {
	if a.themeMonitor == nil {
		return ""
	}
	return string(a.themeMonitor.GetTheme())
}

// syncTrayScheme points the tray icon at the artwork that matches the desktop's
// colour scheme. Only an explicit light/dark theme choice overrides the OS
// preference; the named themes (Nord, Catppuccin, …) all declare their own
// background, so guessing from their names would be wrong more often than right,
// and the OS preference is the one the tray panel itself is painted with.
func (a *App) syncTrayScheme() {
	mode, err := a.settingsStore.GetThemeMode()
	if err != nil {
		// No stored preference: fall back to the OS, which is what the panel uses.
		a.applyTrayScheme()
		return
	}
	switch mode {
	case settings.ThemeModeLight:
		tray.SetDark(false)
	case settings.ThemeModeDark:
		tray.SetDark(true)
	default:
		a.applyTrayScheme()
	}
}

// applyTrayScheme follows the OS colour preference, treating "no preference" as
// light since that is the safer default for artwork drawn in black.
func (a *App) applyTrayScheme() {
	if a.themeMonitor == nil {
		tray.SetDark(false)
		return
	}
	tray.SetDark(a.themeMonitor.GetTheme() == platform.SystemThemeDark)
}
