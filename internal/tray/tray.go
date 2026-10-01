// Package tray exposes a system-tray icon so a background (window-less)
// instance stays reachable. Wails v2 exposes no tray API, so this is a thin
// wrapper around fyne.io/systray running on its own goroutine.
//
// On Linux the icon is a StatusNotifierItem on the session bus. Desktops that
// do not host a StatusNotifierItem watcher (stock GNOME without the
// AppIndicator / KStatusNotifierItem extension) will not render it; the
// notification click path stays the fallback there.
package tray

import (
	_ "embed"
	"sync/atomic"
	"time"

	"fyne.io/systray"
)

//go:embed icon.png
var iconPNG []byte

// Callbacks are invoked on their own goroutine, never on the tray event loop.
type Callbacks struct {
	// Show brings the main window back to the foreground.
	Show func()
	// Sync triggers a full sync of all accounts.
	Sync func()
	// Quit performs an orderly application shutdown.
	Quit func()
}

var started atomic.Bool

// quitting is set while a Stop is tearing the icon down. It blocks a Start()
// from launching a second systray.Run against the same package-global state
// before the first one has returned.
var quitting atomic.Bool

// Start creates the tray icon. Safe to call repeatedly: only the first call
// has an effect. A nil callback disables the matching menu item.
//
// systray.Run blocks for the icon's whole lifetime and owns its own D-Bus
// event loop, so it must not run on Wails' main thread.
func Start(cb Callbacks) {
	if quitting.Load() {
		return
	}
	if !started.CompareAndSwap(false, true) {
		return
	}

	go func() {
		systray.Run(func() {
			systray.SetIcon(iconPNG)
			systray.SetTitle("Email Hub")
			systray.SetTooltip("Email Hub")

			show := addItem("Open Email Hub", "Show the window", cb.Show)
			syncNow := addItem("Sync now", "Check all accounts for new mail", cb.Sync)
			systray.AddSeparator()
			quit := addItem("Quit", "Exit Email Hub", cb.Quit)

			go func() {
				// A nil item is a disabled entry: skip it rather than block.
				showC, syncC, quitC := items(show, syncNow, quit)
				for {
					select {
					case <-showC:
						fire(cb.Show)
					case <-syncC:
						fire(cb.Sync)
					case <-quitC:
						fire(cb.Quit)
						return
					}
				}
			}()

			// Release the started flag only now: systray.Quit() merely signals
			// the loop, Run returns on its own schedule, and a Start() landing
			// in that window would otherwise start a second Run.
			started.Store(false)
		}, func() {
			// onExit runs once the icon is gone, so a later Start can rebuild it.
			started.Store(false)
		})
	}()
}

// Stop tears the icon down. Used by the shutdown path and whenever background
// mode and autostart are both switched off.
func Stop() {
	if !started.Load() {
		return
	}
	quitting.Store(true)
	systray.Quit()

	// The Run goroutine clears `started` when it unwinds; clear `quitting`
	// after that so a later Start() is not permanently suppressed. A short
	// bounded wait is enough because Quit is local, not a network round-trip.
	deadline := time.Now().Add(2 * time.Second)
	for started.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	quitting.Store(false)
}

// Running reports whether this process currently owns a tray icon.
func Running() bool { return started.Load() }

// addItem adds a menu entry, returning nil when there is no handler so the
// entry is rendered greyed-out instead of being a silent no-op.
func addItem(title, tooltip string, fn func()) *systray.MenuItem {
	if fn == nil {
		item := systray.AddMenuItem(title, tooltip)
		item.Disable()
		return nil
	}
	return systray.AddMenuItem(title, tooltip)
}

func items(a, b, c *systray.MenuItem) (chan struct{}, chan struct{}, chan struct{}) {
	return clickCh(a), clickCh(b), clickCh(c)
}

func clickCh(item *systray.MenuItem) chan struct{} {
	if item == nil {
		return nil
	}
	return item.ClickedCh
}

// fire runs the handler on its own goroutine so a slow handler cannot wedge
// the tray event loop.
func fire(fn func()) {
	if fn != nil {
		go fn()
	}
}
