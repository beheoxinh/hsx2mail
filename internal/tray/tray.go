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

// Two mail states crossed with the two OS colour schemes. A tray icon has no
// window behind it, so black artwork vanishes into a dark panel; the envelope
// strokes are therefore drawn white for a dark desktop, while the tick and the
// dot keep their colour because those carry meaning, not contrast. Rasterised
// from brand/tray-read.svg and brand/tray-unread.svg by
// build/icons/generate-icons.sh.
//
//go:embed icon-read.png
var iconReadPNG []byte

//go:embed icon-read-dark.png
var iconReadDarkPNG []byte

//go:embed icon-unread.png
var iconUnreadPNG []byte

//go:embed icon-unread-dark.png
var iconUnreadDarkPNG []byte

// Callbacks are invoked on their own goroutine, never on the tray event loop.
type Callbacks struct {
	// Show brings the main window back to the foreground.
	Show func()
	// Sync triggers a full sync of all accounts.
	Sync func()
	// Quit performs an orderly application shutdown.
	Quit func()
}

// The two live states. Both default to false, which is the light-scheme
// "inbox clear" icon: the safe default before anything has been counted, and the
// one that reads correctly on a light desktop.
var (
	unread atomic.Bool
	dark   atomic.Bool
)

// iconFor picks the tray artwork for the given mail and colour-scheme states.
func iconFor(hasUnread, isDark bool) []byte {
	switch {
	case hasUnread && isDark:
		return iconUnreadDarkPNG
	case hasUnread:
		return iconUnreadPNG
	case isDark:
		return iconReadDarkPNG
	default:
		return iconReadPNG
	}
}

// publish pushes the current state to the shell. Safe before Start and from any
// goroutine: fyne systray keeps the payload and does nothing until its event
// loop exists.
func publish() {
	systray.SetIcon(iconFor(unread.Load(), dark.Load()))
}

// SetUnread switches the tray icon between the two mail states: a red dot while
// mail is waiting, a green tick once the inbox is clear. A no-op when the state
// has not changed, so callers can invoke it on every sync without spamming the
// StatusNotifierItem bus.
func SetUnread(hasUnread bool) {
	if unread.Swap(hasUnread) {
		return
	}
	publish()
}

// SetDark switches the tray artwork between the light-scheme (black strokes) and
// dark-scheme (white strokes) renderings. Like SetUnread it is a no-op when
// nothing changed, and safe to call before Start.
func SetDark(isDark bool) {
	if dark.Swap(isDark) {
		return
	}
	publish()
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
			publish()
			systray.SetTitle("Hsx2Mail")
			systray.SetTooltip("Hsx2Mail")

			show := addItem("Show Mail Client", "Show the window", cb.Show)
			syncNow := addItem("Sync All Mail Now", "Check all accounts for new mail", cb.Sync)
			systray.AddSeparator()
			quit := addItem("Quit", "Exit Hsx2Mail", cb.Quit)

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
