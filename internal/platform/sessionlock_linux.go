//go:build linux

package platform

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/beheoxinh/hsx2mail/internal/logging"
	"github.com/godbus/dbus/v5"
)

const (
	login1Path       = "/org/freedesktop/login1"
	login1Iface      = "org.freedesktop.login1.Manager"
	screenSaverPath  = "/org/gnome/ScreenSaver"
	screenSaverIface = "org.gnome.ScreenSaver"
)

// linuxSessionLockMonitor tracks the session lock state over D-Bus.
//
// Preferred source is systemd-logind (LockSignal / UnlockSignal, plus the
// LockedHint property for the initial state), because it is the only one
// present on KDE and on GNOME sessions without the screensaver extension.
// org.gnome.ScreenSaver ActiveChanged is subscribed as a fallback so GNOME
// covers the case where logind is not reachable on the session bus.
type linuxSessionLockMonitor struct {
	conn     *dbus.Conn
	locked   atomic.Bool
	mu       sync.Mutex
	stopped  bool
	signals  chan *dbus.Signal
	listened bool
}

// NewSessionLockMonitor creates a monitor for the running platform.
func NewSessionLockMonitor() SessionLockMonitor {
	return &linuxSessionLockMonitor{signals: make(chan *dbus.Signal, 16)}
}

func (m *linuxSessionLockMonitor) Start(ctx context.Context) error {
	log := logging.WithComponent("session-lock")

	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	m.conn = conn

	// Seed with the current state so a notification fired while the user
	// locks the screen in the same instant is still suppressed.
	m.locked.Store(m.queryLockedHint() || m.queryScreenSaverActive())

	// logind first — it is authoritative where present.
	loginRule := "type='signal',interface='" + login1Iface + "',member='LockSignal'"
	if call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, loginRule); call.Err != nil {
		log.Warn().Err(call.Err).Msg("Cannot subscribe to logind LockSignal")
	} else {
		loginUnlock := "type='signal',interface='" + login1Iface + "',member='UnlockSignal'"
		if call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, loginUnlock); call.Err != nil {
			log.Warn().Err(call.Err).Msg("Cannot subscribe to logind UnlockSignal")
		}
		log.Info().Msg("Monitoring session lock via systemd-logind signals")
	}

	// GNOME screensaver, for sessions where logind is not on the bus.
	ssRule := "type='signal',interface='" + screenSaverIface + "',member='ActiveChanged'"
	if call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, ssRule); call.Err != nil {
		log.Debug().Err(call.Err).Msg("Cannot subscribe to GNOME ScreenSaver ActiveChanged")
	}

	conn.Signal(m.signals)
	m.mu.Lock()
	m.listened = true
	m.mu.Unlock()

	go m.listen(ctx)

	log.Info().Bool("locked", m.locked.Load()).Msg("Session lock monitor started")
	return nil
}

// queryLockedHint reads the logind LockedHint property. Missing property or
// missing bus is not an error: it just means "not known to be locked".
func (m *linuxSessionLockMonitor) queryLockedHint() bool {
	v, err := m.conn.Object(login1Path, login1Iface).GetProperty(login1Iface + ".LockedHint")
	if err != nil {
		return false
	}
	locked, ok := v.Value().(bool)
	return ok && locked
}

func (m *linuxSessionLockMonitor) queryScreenSaverActive() bool {
	v, err := m.conn.Object(screenSaverPath, screenSaverIface).GetProperty(screenSaverIface + ".Active")
	if err != nil {
		return false
	}
	active, ok := v.Value().(bool)
	return ok && active
}

func (m *linuxSessionLockMonitor) listen(ctx context.Context) {
	log := logging.WithComponent("session-lock")

	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-m.signals:
			if sig == nil {
				continue
			}
			switch sig.Name {
			case login1Iface + ".LockSignal":
				m.setLocked(true)
			case login1Iface + ".UnlockSignal":
				m.setLocked(false)
			case screenSaverIface + ".ActiveChanged":
				// ActiveChanged carries (b, active).
				if len(sig.Body) >= 2 {
					if active, ok := sig.Body[1].(bool); ok {
						m.setLocked(active)
					}
				}
			default:
				continue
			}
			log.Debug().Str("signal", sig.Name).Bool("locked", m.locked.Load()).Msg("Session lock signal")
		}
	}
}

func (m *linuxSessionLockMonitor) setLocked(v bool) {
	m.locked.Store(v)
}

func (m *linuxSessionLockMonitor) Locked() bool { return m.locked.Load() }

func (m *linuxSessionLockMonitor) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped || !m.listened {
		return nil
	}
	m.stopped = true
	if m.conn != nil {
		return m.conn.Close()
	}
	return nil
}
