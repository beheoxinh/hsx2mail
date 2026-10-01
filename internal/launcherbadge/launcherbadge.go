//go:build linux

// Package launcherbadge publishes the application's unread count to the desktop
// shell, so the dock/dash icon can show a badge.
//
// # What actually draws a badge
//
// GNOME Shell itself has no app-icon badge API: nothing in the shell's own
// source reads a count. The badge is implemented by the shells that grew one as
// an extension — Dash to Dock, Ubuntu Dock, Dash to Panel — and by anything else
// that speaks the Unity LauncherEntry protocol.
//
// The contract an app has to implement, verified against the consumers' source
// (dash-to-dock launcherAPI.js and appIconIndicators.js):
//
//   - export an object at /org/unity/launcherentry/<desktop file id>
//   - on interface com.canonical.Unity.LauncherEntry
//   - emit  Update(s app_uri, a{sv} properties)
//     with keys count (uint32), count-visible (bool), urgent (bool)
//
// The consumers derive the app id by stripping the path prefix and matching it
// against Shell.App.get_id(), which INCLUDES the ".desktop" suffix, while parts
// of the original spec and other consumers drop it. The object is therefore
// exported under both spellings.
//
// Two details the consumer source settles, both easy to get wrong:
//   - the signal is the only thing they react to; they never call GetAll;
//   - the first Update argument is the OBJECT PATH, not the sender name — the
//     consumer runs `appUri.replace(/(^\w+:|^)\/\//, ”)` on it.
//
// The org.freedesktop.DBus.Properties implementation comes from godbus's prop
// package, so clients that do poll (and busctl) still see the values.
//
// # When nothing reads it
//
// On a stock GNOME session this is inert: the object is exported, the signal is
// emitted, and no shell draws anything. That is not an error and it costs one
// D-Bus object. SetCount is safe to call regardless.
package launcherbadge

import (
	"fmt"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
	"github.com/rs/zerolog/log"
)

// Unity LauncherEntry protocol constants.
const (
	ifaceLauncherEntry = "com.canonical.Unity.LauncherEntry"
	signalUpdate       = "Update"
	nameLauncherEntry  = "com.canonical.Unity.LauncherEntry"

	// objectPathPrefix is fixed by the protocol; the app id is appended to it.
	objectPathPrefix = "/org/unity/launcherentry/"

	// maxCount caps the published value. A four-digit blob over a 24px icon is
	// unreadable, and some shells clamp it themselves, inconsistently.
	maxCount = 9999
)

// entry is the set of properties published on one object path.
func newEntryMap() prop.Map {
	return prop.Map{
		ifaceLauncherEntry: {
			"count": {
				Value:    uint32(0),
				Writable: false,
				Emit:     prop.EmitTrue,
			},
			"count-visible": {
				Value:    false,
				Writable: false,
				Emit:     prop.EmitTrue,
			},
			"urgent": {
				Value:    false,
				Writable: false,
				Emit:     prop.EmitTrue,
			},
			"updating": {
				Value:    false,
				Writable: false,
				Emit:     prop.EmitTrue,
			},
			"progress": {
				Value:    float64(0),
				Writable: false,
				Emit:     prop.EmitTrue,
			},
		},
	}
}

// Badge publishes the unread count to the shell.
type Badge struct {
	desktopID string
	conn      *dbus.Conn
	paths     []dbus.ObjectPath
	props     []*prop.Properties
	ownedName string

	mu    sync.Mutex
	count uint32
}

// New returns a Badge for the given desktop file id, e.g.
// "io.github.beheoxinh.Hsx2Mail.desktop". It does not touch the bus; call Start.
func New(desktopID string) *Badge {
	return &Badge{desktopID: desktopID}
}

// appURI is the value the Update signal's first argument must carry. The
// consumer strips `scheme://` from it and matches the rest against the shell's
// app id, which includes the ".desktop" suffix.
func (b *Badge) appURI() string {
	return "application://" + b.desktopID
}

// introspectNode describes what the app publishes on the launcher-entry object,
// so generic D-Bus tooling (gdbus introspect, busctl, D-Bus browsers) can see
// it. prop.Properties answers Get/GetAll/Set but not Introspect, so without this
// the object is invisible to all of them.
func introspectNode() *introspect.Node {
	return &introspect.Node{
		Name: ".",
		Interfaces: []introspect.Interface{
			{
				Name: "com.canonical.Unity.LauncherEntry",
				Properties: []introspect.Property{
					{Name: "count", Type: "u", Access: "read"},
					{Name: "count-visible", Type: "b", Access: "read"},
					{Name: "urgent", Type: "b", Access: "read"},
					{Name: "updating", Type: "b", Access: "read"},
					{Name: "progress", Type: "d", Access: "read"},
				},
				Signals: []introspect.Signal{{
					Name: signalUpdate,
					Args: []introspect.Arg{
						{Name: "app_uri", Type: "s", Direction: "out"},
						{Name: "properties", Type: "a{sv}", Direction: "out"},
					},
				}},
			},
			{Name: "org.freedesktop.DBus.Properties"},
		},
	}
}

// pathSafeID converts a desktop file id into a valid D-Bus path element.
//
// A D-Bus path element may only contain [A-Za-z0-9_]; dots make the whole path
// invalid. The ".desktop" suffix is dropped and the remaining dots become
// underscores.
func pathSafeID(desktopID string) string {
	trimmed := trimDesktopSuffix(desktopID)
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// trimDesktopSuffix removes a trailing ".desktop".
func trimDesktopSuffix(id string) string {
	const suffix = ".desktop"
	if len(id) > len(suffix) && id[len(id)-len(suffix):] == suffix {
		return id[:len(id)-len(suffix)]
	}
	return id
}

// Start connects to the session bus, exports the object under both id spellings
// and claims the well-known name.
//
// Failure is not fatal by design: with no session bus, or in a sandbox that
// forbids the name, the badge simply never appears. Callers log and continue.
func (b *Badge) Start() error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("session bus unavailable: %w", err)
	}
	b.conn = conn

	// A D-Bus path element cannot contain a dot, so the reverse-DNS id is
	// mangled to its underscore form. Harmless: consumers key off the Update
	// signal's URI argument, not off this path.
	for _, id := range []string{pathSafeID(b.desktopID)} {
		path := dbus.ObjectPath(objectPathPrefix + id)
		props, err := prop.Export(conn, path, newEntryMap())
		if err != nil {
			return fmt.Errorf("export %s: %w", path, err)
		}
		// prop.Properties answers Get/GetAll/Set but not Introspect, so without
		// this the object is invisible to `gdbus introspect`, busctl and any
		// D-Bus browser. Registering a second interface on the same path is how
		// godbus allows it.
		if err := conn.Export(introspect.NewIntrospectable(introspectNode()), path, "org.freedesktop.DBus.Introspectable"); err != nil {
			log.Debug().Err(err).Str("path", string(path)).
				Msg("launcherbadge: could not register Introspectable")
		}
		b.paths = append(b.paths, path)
		b.props = append(b.props, props)
	}

	// Claiming the name is not required by the consumers — they react to the
	// signal from any sender — but real clients look for it, and a name changing
	// hands is what makes them rebuild their model.
	reply, err := conn.RequestName(nameLauncherEntry, dbus.NameFlagDoNotQueue)
	switch {
	case err != nil:
		log.Debug().Err(err).Msg("launcherbadge: could not request " + nameLauncherEntry)
	case reply == dbus.RequestNameReplyPrimaryOwner:
		b.ownedName = nameLauncherEntry
	default:
		// Another launcher already holds it; the object and signal still work.
		log.Debug().Str("name", nameLauncherEntry).
			Int("reply", int(reply)).
			Msg("launcherbadge: name already owned by another launcher")
	}
	return nil
}

// publish writes the properties and emits the Update signal.
//
// The signal is emitted on every call rather than only on change: a shell that
// loaded after the application started learns the current count from the next
// emit, and consumers differ on whether they replay state when the object
// appears.
func (b *Badge) publish(count uint32, urgent bool) {
	if b.conn == nil {
		return
	}
	visible := count > 0
	for i, path := range b.paths {
		p := b.props[i]
		// The local store is the fallback for consumers that read properties
		// rather than the signal; a failure there is a D-Bus error worth
		// surfacing, not swallowing.
		if err := p.Set(ifaceLauncherEntry, "count", dbus.MakeVariant(count)); err != nil {
			log.Debug().Err(err).Str("path", string(path)).Msg("launcherbadge: set count failed")
		}
		if err := p.Set(ifaceLauncherEntry, "count-visible", dbus.MakeVariant(visible)); err != nil {
			log.Debug().Err(err).Str("path", string(path)).Msg("launcherbadge: set count-visible failed")
		}
		if err := p.Set(ifaceLauncherEntry, "urgent", dbus.MakeVariant(urgent)); err != nil {
			log.Debug().Err(err).Str("path", string(path)).Msg("launcherbadge: set urgent failed")
		}

		props := map[string]dbus.Variant{
			"count":         dbus.MakeVariant(count),
			"count-visible": dbus.MakeVariant(visible),
			"urgent":        dbus.MakeVariant(urgent),
		}
		// The first argument is a URI, not the object path: the consumer applies
		// `replace(/(^\w+:|^)\/\//, '')` to it and matches the result against
		// Shell.App.get_id(), which keeps the ".desktop" suffix.
		if err := b.conn.Emit(path, ifaceLauncherEntry, signalUpdate, 0, b.appURI(), props); err != nil {
			log.Debug().Err(err).Msg("launcherbadge: Update emit failed")
		}
	}
}

// SetCount publishes the unread count. Zero hides the badge, which is what
// consumers treat as "no badge" (count-visible false).
//
// Negative values are clamped to zero: the protocol type is unsigned and a
// negative count would make the variant impossible for a shell to unpack.
func (b *Badge) SetCount(n int) {
	if n < 0 {
		n = 0
	}
	if n > maxCount {
		n = maxCount
	}

	b.mu.Lock()
	count := uint32(n)
	changed := count != b.count
	b.count = count
	b.mu.Unlock()

	if changed {
		b.publish(count, false)
	}
}

// SetUrgent marks the application as needing attention independently of the
// count, for consumers that do not implement count-visible.
func (b *Badge) SetUrgent(urgent bool) {
	b.mu.Lock()
	count := b.count
	b.mu.Unlock()
	b.publish(count, urgent)
}

// Stop releases the exported objects and any owned name.
func (b *Badge) Stop() {
	if b.conn == nil {
		return
	}
	// godbus un-exports an object path when it is re-exported with an empty
	// property map, so that is how the objects go away.
	for _, path := range b.paths {
		if _, err := prop.Export(b.conn, path, prop.Map{}); err != nil {
			log.Debug().Err(err).Str("path", string(path)).
				Msg("launcherbadge: un-export failed")
		}
	}
	if b.ownedName != "" {
		_, _ = b.conn.ReleaseName(b.ownedName)
	}
	b.props = nil
	b.paths = nil
	b.ownedName = ""
	b.conn = nil
}
