package app

import (
	"github.com/beheoxinh/hsx2mail/internal/logging"
	"github.com/beheoxinh/hsx2mail/internal/tray"
)

// trayWanted reports whether a tray icon should exist for the current
// settings.
//
// The icon exists to make a hidden instance recoverable, so it is created
// exactly when the instance can be window-less: background mode (close hides
// instead of quitting) or autostart (a session login may start it hidden).
// Users who never enable either keep the previous, tray-free behaviour.
func (a *App) trayWanted() bool {
	runBg, _ := a.settingsStore.GetRunBackground()
	if runBg {
		return true
	}
	autostart, _ := a.settingsStore.GetAutostart()
	return autostart
}

// startTray creates the tray icon if the settings call for one. Called at the
// end of Startup and again whenever run_background / autostart is switched
// on, so enabling either setting mid-session immediately yields a way back
// into the window. tray.Start is idempotent.
// syncTray makes the tray icon match the current settings: created when
// background mode or autostart is on, removed when neither is.
//
// This must run on BOTH edges. Creating it only (as the settings toggle
// originally did) leaves the icon on screen with no way to remove it short of
// restarting the app, which is worse than not having one at all.
func (a *App) syncTray() {
	if !a.trayWanted() {
		tray.Stop()
		return
	}
	a.startTray()
}

func (a *App) startTray() {
	if a.settingsStore == nil || !a.trayWanted() {
		return
	}
	if tray.Running() {
		return
	}

	tray.Start(tray.Callbacks{
		Show: a.ShowWindow,
		Sync: func() {
			log := logging.WithComponent("app.tray")
			log.Info().Msg("Tray requested sync")
			if err := a.SyncAllComplete(); err != nil {
				log.Error().Err(err).Msg("Tray sync failed")
			}
		},
		Quit: a.QuitApp,
	})
}
