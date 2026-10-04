//go:build linux

package launcherbadge

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// The signal signature is the whole contract: a shell that cannot unpack it shows
// no badge, and nothing reports the problem. The receive side is exercised
// through `gdbus monitor` rather than an in-process godbus subscription: that is
// the same tool the shells' code paths are built on, so a test that passes here
// means a real consumer can read it. An in-process test also proved flaky,
// because a match rule on the test's own connection does not behave the way a
// second party's subscription does.
//
// Skipped when there is no session bus.
func TestUpdateSignalReachesTheBus(t *testing.T) {
	if _, err := exec.LookPath("gdbus"); err != nil {
		t.Skipf("gdbus not available: %v", err)
	}
	if _, err := exec.LookPath("dbus-send"); err != nil {
		// dbus-launch is another way to get a bus; without either, skip.
		if _, err2 := exec.LookPath("dbus-launch"); err2 != nil {
			t.Skip("no session bus tooling available")
		}
	}

	const desktopID = "io.github.beheoxinh.Hsx2Mail.desktop"
	wantPath := objectPathPrefix + pathSafeID(desktopID)
	wantURI := "application://" + desktopID

	mon := exec.Command("gdbus", "monitor", "--session", "--dest", nameLauncherEntry)
	pipe, err := mon.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := mon.Start(); err != nil {
		t.Fatalf("gdbus monitor: %v", err)
	}
	defer func() {
		_ = mon.Process.Kill()
		_ = mon.Wait()
	}()
	time.Sleep(700 * time.Millisecond) // let the monitor install its match rule

	// Only the connection that owns the name emits the signals this test watches.
	// A running app instance already owns it, so the badge under test would be
	// silent and the assertion below would report a failure that is really just
	// contention. Say so and skip instead of failing on the wrong thing.
	if owner, err := nameOwner(); err == nil && owner != "" {
		t.Skipf("%s is already owned by %s; close the running app first", nameLauncherEntry, owner)
	}

	b := New(desktopID)
	if err := b.Start(); err != nil {
		// Not a skip: with a session bus present, failing to export is the bug
		// under test, and skipping here is what let a broken export pass before.
		t.Fatalf("Start failed: %v", err)
	}
	defer b.Stop()

	// gdbus monitor installs its destination match only after it observes the
	// name being acquired, which happens concurrently with Start() returning. An
	// emit in that window is missed, so let the match settle first.
	time.Sleep(600 * time.Millisecond)

	// A non-zero count must be visible, and dropping to zero must clear it.
	b.SetCount(7)
	time.Sleep(600 * time.Millisecond)
	b.SetCount(0)

	// StdoutPipe returns an io.ReadCloser; the deadline-bounded reads below need
	// the *os.File underneath it.
	reader, ok := pipe.(*os.File)
	if !ok {
		t.Skip("stdout is not a file; cannot bound the read")
	}

	buf := make([]byte, 8192)
	var out strings.Builder
	end := time.Now().Add(8 * time.Second)
	satisfied := func() bool {
		s := out.String()
		return strings.Contains(s, "'count': <uint32 7>") &&
			strings.Contains(s, "'count': <uint32 0>")
	}
	for time.Now().Before(end) && !satisfied() {
		// Short per-read deadline so the loop keeps polling instead of giving up
		// on the first idle moment.
		_ = reader.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _ := reader.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
	}
	got := out.String()

	if !strings.Contains(got, "Update") {
		t.Fatalf("no Update signal observed by gdbus monitor; got:\n%s", got)
	}
	if !strings.Contains(got, wantPath) {
		t.Errorf("signal not emitted from the protocol path %q; got:\n%s", wantPath, got)
	}
	if !strings.Contains(got, wantURI) {
		t.Errorf("signal app_uri should be %q (a shell strips scheme:// and matches the rest against app.get_id(), which keeps the .desktop suffix); got:\n%s", wantURI, got)
	}
	// gdbus renders a{sv} as {'count': <uint32 0>, 'count-visible': <false>,
	// 'urgent': <false>}. Assert the exact rendering, because a shell that
	// cannot unpack these values shows nothing.
	if !strings.Contains(got, "'count': <uint32 7>") {
		t.Errorf("expected 'count': <uint32 7>; got:\n%s", got)
	}
	if !strings.Contains(got, "'count-visible': <true>") {
		t.Errorf("expected a non-zero count to set 'count-visible': <true>; got:\n%s", got)
	}
	// The zero publish must hide the badge, otherwise a stale number sticks to
	// the icon after the user reads everything.
	if !strings.Contains(got, "'count': <uint32 0>") {
		t.Errorf("expected 'count': <uint32 0>; got:\n%s", got)
	}
	if !strings.Contains(got, "'count-visible': <false>") {
		t.Errorf("expected 'count-visible': <false> so the badge is hidden; got:\n%s", got)
	}
}

// nameOwner reports the unique bus name currently holding nameLauncherEntry, or
// "" when the name is unowned.
func nameOwner() (string, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return "", err
	}
	obj := conn.Object("org.freedesktop.DBus", dbus.ObjectPath("/org/freedesktop/DBus"))
	var owner string
	err = obj.Call("org.freedesktop.DBus.GetNameOwner", 0, nameLauncherEntry).Store(&owner)
	return owner, err
}
