//go:build linux

package launcherbadge

import (
	"regexp"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// The object path is the whole contract: a consumer strips the prefix and matches
// the remainder against Shell.App.get_id(). Getting the prefix wrong means the
// badge silently never appears, which is exactly the failure nobody notices.
func TestObjectPathConvention(t *testing.T) {
	if objectPathPrefix != "/org/unity/launcherentry/" {
		t.Errorf("objectPathPrefix = %q, want /org/unity/launcherentry/", objectPathPrefix)
	}
	if ifaceLauncherEntry != "com.canonical.Unity.LauncherEntry" {
		t.Errorf("interface = %q, want com.canonical.Unity.LauncherEntry", ifaceLauncherEntry)
	}
	if signalUpdate != "Update" {
		t.Errorf("signal = %q, want Update", signalUpdate)
	}
}

// GNOME's Shell.App.get_id() keeps the ".desktop" suffix while the original spec
// drops it, and consumers disagree. Both spellings are exported for that reason.
func TestTrimDesktopSuffix(t *testing.T) {
	cases := map[string]string{
		"io.github.beheoxinh.Hsx2Mail.desktop": "io.github.beheoxinh.Hsx2Mail",
		"io.github.beheoxinh.Hsx2Mail":         "io.github.beheoxinh.Hsx2Mail",
		"desktop":                              "desktop",
		"":                                     "",
		".desktop":                             ".desktop", // too short to strip
	}
	for in, want := range cases {
		if got := trimDesktopSuffix(in); got != want {
			t.Errorf("trimDesktopSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}

// SetCount must be safe before Start: the app calls it from paths that can run
// before startup completes, and a nil connection must not panic.
func TestSetCountBeforeStartDoesNotPanic(t *testing.T) {
	b := New("io.github.beheoxinh.Hsx2Mail.desktop")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("SetCount before Start panicked: %v", r)
		}
	}()
	b.SetCount(5)
	b.SetUrgent(true)
	b.Stop()
}

// A negative count would make the uint32 variant impossible for a shell to
// unpack, and an absurd one renders as an unreadable blob over the icon.
func TestSetCountClamps(t *testing.T) {
	cases := map[int]uint32{
		-1:    0,
		-9999: 0,
		0:     0,
		1:     1,
		42:    42,
	}
	for in, want := range cases {
		b := New("x.desktop")
		b.SetCount(in)
		b.mu.Lock()
		got := b.count
		b.mu.Unlock()
		if got != want {
			t.Errorf("SetCount(%d) stored %d, want %d", in, got, want)
		}
	}

	// Above the cap the stored value is clamped, not rejected.
	b := New("x.desktop")
	b.SetCount(maxCount + 5000)
	b.mu.Lock()
	got := b.count
	b.mu.Unlock()
	if got != maxCount {
		t.Errorf("SetCount(%d) stored %d, want clamp to %d", maxCount+5000, got, maxCount)
	}
}

// Stop on a never-started badge must be a no-op, not a nil dereference.
func TestStopBeforeStartDoesNotPanic(t *testing.T) {
	b := New("x.desktop")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Stop before Start panicked: %v", r)
		}
	}()
	b.Stop()
	b.Stop()
}

// The published property set must be exactly what the consumers unpack.
func TestEntryMapHasProtocolProperties(t *testing.T) {
	m := newEntryMap()
	iface, ok := m[ifaceLauncherEntry]
	if !ok {
		t.Fatalf("property map has no %s interface", ifaceLauncherEntry)
	}
	for _, name := range []string{"count", "count-visible", "urgent", "updating", "progress"} {
		if _, ok := iface[name]; !ok {
			t.Errorf("missing property %q; consumers unpack it", name)
		}
	}
	// count must start as uint32: the consumer does `this._remoteEntry.count ?? 0`
	// and then renders it into a badge label.
	if got := iface["count"].Value; got != uint32(0) {
		t.Errorf("count default = %T(%v), want uint32(0)", got, got)
	}
	if got := iface["count-visible"].Value; got != false {
		t.Errorf("count-visible default = %T(%v), want false", got, got)
	}
}

// godbus rejects any object path whose element contains a dot: a reverse-DNS
// desktop id is NOT a valid D-Bus path. Verified with ObjectPath.IsValid(),
// because this is the failure that makes the badge silently never appear —
// Start() returns an error and the caller logs it at debug level.
func TestPathSafeIDProducesAValidObjectPath(t *testing.T) {
	ids := []string{
		"io.github.beheoxinh.Hsx2Mail.desktop",
		"io.github.beheoxinh.Hsx2Mail",
		"org.gnome.Nautilus.desktop",
		"thunderbird.desktop", // single-word ids keep the conventional form
	}
	for _, id := range ids {
		raw := objectPathPrefix + trimDesktopSuffix(id)
		safe := objectPathPrefix + pathSafeID(id)

		// Whatever we export must be a path godbus will accept.
		if !dbus.ObjectPath(safe).IsValid() {
			t.Errorf("id %q -> %q is not a valid D-Bus object path", id, safe)
		}
		// And the unsafe form is what used to be shipped, so assert the two
		// genuinely differ where they need to.
		if raw != safe && dbus.ObjectPath(raw).IsValid() {
			t.Errorf("id %q: %q is valid too; pathSafeID mangled it needlessly", id, raw)
		}
	}
}

func TestPathSafeID(t *testing.T) {
	cases := map[string]string{
		"io.github.beheoxinh.Hsx2Mail.desktop": "io_github_beheoxinh_Hsx2Mail",
		"io.github.beheoxinh.Hsx2Mail":         "io_github_beheoxinh_Hsx2Mail",
		"thunderbird.desktop":                  "thunderbird",
		"org.gnome.Nautilus.desktop":           "org_gnome_Nautilus",
	}
	for in, want := range cases {
		if got := pathSafeID(in); got != want {
			t.Errorf("pathSafeID(%q) = %q, want %q", in, got, want)
		}
	}
}

// The consumer strips `scheme://` from the first Update argument and matches the
// remainder against Shell.App.get_id(), which KEEPS the suffix. Sending the
// object path instead (mangled, no suffix) would match nothing.
func TestAppURICarriesTheDesktopSuffix(t *testing.T) {
	const id = "io.github.beheoxinh.Hsx2Mail.desktop"
	b := New(id)
	uri := b.appURI()
	if uri != "application://"+id {
		t.Errorf("appURI() = %q, want application://%s", uri, id)
	}

	// Reproduce the consumer's exact transform.
	stripped := regexp.MustCompile(`(^\w+:|^)//`).ReplaceAllString(uri, "")
	if stripped != id {
		t.Errorf("consumer transform of %q yields %q, want %q", uri, stripped, id)
	}
	// The URI is never the object path.
	if strings.Contains(uri, objectPathPrefix) {
		t.Errorf("appURI %q must not be the object path", uri)
	}
}
