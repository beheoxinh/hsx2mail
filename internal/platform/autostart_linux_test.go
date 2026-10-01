//go:build linux

package platform

import (
	"fmt"
	"strings"
	"testing"
)

// TestAutostartExecForcesHiddenStart is the runnable check for Phase 3 tasks
// 3-04/3-05: the Exec= value the autostart manager writes must be a quoted
// absolute path (or the Flatpak command) followed by --start-hidden, so a
// session login never opens a window and a path with spaces still parses.
func TestAutostartExecForcesHiddenStart(t *testing.T) {
	m := &linuxAutostartManager{isFlatpak: false}

	got := m.execCommand()
	if !strings.HasSuffix(got, " --start-hidden") {
		t.Errorf("execCommand() = %q, want it to end with %q", got, " --start-hidden")
	}
	if !strings.HasPrefix(got, `"`) || !strings.Contains(got, `"`) {
		t.Errorf("execCommand() = %q, want the executable path quoted per the desktop-entry spec", got)
	}

	// The template must render exactly one Exec line with that value.
	content := fmt.Sprintf(desktopEntryTmpl, got)
	var execLines []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "Exec=") {
			execLines = append(execLines, line)
		}
	}
	if len(execLines) != 1 {
		t.Fatalf("rendered entry has %d Exec lines, want 1:\n%s", len(execLines), content)
	}
	if want := "Exec=" + got; execLines[0] != want {
		t.Errorf("Exec line = %q, want %q", execLines[0], want)
	}
}

// TestAutostartCommandlineReentersFlatpak covers task 3-02: the Background
// portal must be handed a host-runnable command, never the sandbox-internal
// binary name.
func TestAutostartCommandlineReentersFlatpak(t *testing.T) {
	t.Setenv("FLATPAK_ID", "io.github.beheoxinh.Hsx2Mail")
	got := autostartCommandline()
	want := []string{"flatpak", "run", "io.github.beheoxinh.Hsx2Mail", "--start-hidden"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("autostartCommandline() = %v, want %v", got, want)
	}
}

// TestAutostartCommandlineAlwaysHidden guards both autostart channels: the XDG
// Exec line and the Flatpak portal argv must both carry --start-hidden, so a
// session login never opens a window no matter which channel installed it.
func TestAutostartCommandlineAlwaysHidden(t *testing.T) {
	t.Setenv("FLATPAK_ID", "")
	if got := autostartCommandline(); got[len(got)-1] != "--start-hidden" {
		t.Errorf("native autostartCommandline() = %v, want it to end with --start-hidden", got)
	}
	t.Setenv("FLATPAK_ID", "io.github.beheoxinh.Hsx2Mail")
	if got := autostartCommandline(); got[len(got)-1] != "--start-hidden" {
		t.Errorf("flatpak autostartCommandline() = %v, want it to end with --start-hidden", got)
	}
}
