package tray

import (
	"bytes"
	"image/png"
	"testing"
)

// TestEmbeddedIconsAreValidPNGs is the runnable check that the tray badge
// embedded at build time decodes as a PNG and stays small enough to send over
// D-Bus on every property update.
func TestEmbeddedIconIsValidPNG(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"read", iconReadPNG},
		{"read-dark", iconReadDarkPNG},
		{"unread", iconUnreadPNG},
		{"unread-dark", iconUnreadDarkPNG},
	} {
		img, err := png.Decode(bytes.NewReader(tc.data))
		if err != nil {
			t.Fatalf("decode %s-state icon failed: %v", tc.name, err)
		}
		if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
			t.Errorf("%s-state icon size %dx%d, want 32x32",
				tc.name, img.Bounds().Dx(), img.Bounds().Dy())
		}
		if len(tc.data) > 2048 {
			t.Errorf("%s-state icon payload is %d bytes, want <= 2048", tc.name, len(tc.data))
		}
	}
	all := [][]byte{iconReadPNG, iconReadDarkPNG, iconUnreadPNG, iconUnreadDarkPNG}
	names := []string{"read", "read-dark", "unread", "unread-dark"}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if bytes.Equal(all[i], all[j]) {
				t.Errorf("%s and %s icons are identical; the tray cannot show that difference",
					names[i], names[j])
			}
		}
	}
}

// TestAddItemDisabledForNilCallback guards the menu contract: a nil handler
// must produce a nil item (rendered disabled), never a live channel to nothing.
func TestAddItemDisabledForNilCallback(t *testing.T) {
	if it := addItem("x", "y", nil); it != nil {
		t.Fatalf("addItem(nil) = %v, want nil", it)
	}
}

// TestClickChNilSafe ensures the menu pump cannot panic on a disabled entry.
func TestClickChNilSafe(t *testing.T) {
	if ch := clickCh(nil); ch != nil {
		t.Fatalf("clickCh(nil) = %v, want nil", ch)
	}
}

// TestIconForPicksState pins the whole 2x2: mail state on one axis, OS colour
// scheme on the other. Getting an axis backwards either shows a permanent "you
// have mail" or paints black strokes on a dark panel, and neither shows up in
// the UI or in a normal test run.
func TestIconForPicksState(t *testing.T) {
	cases := []struct {
		unread, dark bool
		want         []byte
		name         string
	}{
		{false, false, iconReadPNG, "read/light"},
		{false, true, iconReadDarkPNG, "read/dark"},
		{true, false, iconUnreadPNG, "unread/light"},
		{true, true, iconUnreadDarkPNG, "unread/dark"},
	}
	for _, tc := range cases {
		if got := iconFor(tc.unread, tc.dark); !bytes.Equal(got, tc.want) {
			t.Errorf("iconFor(unread=%v, dark=%v) [%s] picked the wrong artwork",
				tc.unread, tc.dark, tc.name)
		}
	}
}

// TestSetDarkIsIdempotent mirrors TestSetUnreadIsIdempotent for the colour axis.
func TestSetDarkIsIdempotent(t *testing.T) {
	dark.Store(false)
	SetDark(false)
	SetDark(true)
	if !dark.Load() {
		t.Error("SetDark(true) did not record the state")
	}
	SetDark(true)
	if !dark.Load() {
		t.Error("repeat SetDark(true) cleared the state")
	}
	SetDark(false)
	if dark.Load() {
		t.Error("SetDark(false) did not clear the state")
	}
}

// TestSetUnreadIsIdempotent covers the guard that keeps sync from spamming the
// StatusNotifierItem bus: only an actual change may publish.
func TestSetUnreadIsIdempotent(t *testing.T) {
	unread.Store(false)
	SetUnread(false) // no change: must not touch systray before Start either
	SetUnread(true)
	if !unread.Load() {
		t.Error("SetUnread(true) did not record the state")
	}
	SetUnread(true)
	if !unread.Load() {
		t.Error("repeat SetUnread(true) cleared the state")
	}
	SetUnread(false)
	if unread.Load() {
		t.Error("SetUnread(false) did not clear the state")
	}
}

// recordIconPublishes swaps the setIcon seam for the duration of the test and
// returns the slice of icons publish() pushed. The atomics are reset so each
// test starts from the light-scheme "inbox clear" default.
func recordIconPublishes(t *testing.T) *[][]byte {
	t.Helper()
	var calls [][]byte
	orig := setIcon
	setIcon = func(icon []byte) { calls = append(calls, icon) }
	t.Cleanup(func() {
		setIcon = orig
		unread.Store(false)
		dark.Store(false)
	})
	unread.Store(false)
	dark.Store(false)
	return &calls
}

// TestSetUnreadPublishesBothTransitions is the regression test for the
// inverted Swap guard: clearing the unread state must republish the green
// tick, not silently keep the red dot on the shell.
func TestSetUnreadPublishesBothTransitions(t *testing.T) {
	calls := recordIconPublishes(t)

	SetUnread(true)
	if len(*calls) != 1 {
		t.Fatalf("SetUnread(false→true) publishes = %d, want 1", len(*calls))
	}
	if !bytes.Equal((*calls)[0], iconUnreadPNG) {
		t.Error("false→true published the wrong artwork")
	}

	SetUnread(false)
	if len(*calls) != 2 {
		t.Fatalf("SetUnread(true→false) publishes = %d, want 1 more (the clearing must repaint)", len(*calls))
	}
	if !bytes.Equal((*calls)[1], iconReadPNG) {
		t.Error("true→false published the wrong artwork")
	}
}

// TestSetUnreadSkipsNoopPublishes keeps the spam guard honest: unchanged
// states must not touch the StatusNotifierItem bus.
func TestSetUnreadSkipsNoopPublishes(t *testing.T) {
	calls := recordIconPublishes(t)

	SetUnread(false) // already false
	SetUnread(true)
	SetUnread(true) // repeat
	if len(*calls) != 1 {
		t.Fatalf("publishes = %d, want 1 (only the false→true change)", len(*calls))
	}
}

// TestSetDarkPublishesBothTransitions applies the same Swap fix to the colour
// scheme: switching back to light must repaint the black-stroke artwork.
func TestSetDarkPublishesBothTransitions(t *testing.T) {
	calls := recordIconPublishes(t)

	SetDark(true)
	if len(*calls) != 1 || !bytes.Equal((*calls)[0], iconReadDarkPNG) {
		t.Fatalf("false→true publishes = %d with correct artwork = %v", len(*calls),
			len(*calls) == 1 && bytes.Equal((*calls)[0], iconReadDarkPNG))
	}

	SetDark(false)
	if len(*calls) != 2 || !bytes.Equal((*calls)[1], iconReadPNG) {
		t.Fatalf("true→false publishes = %d with correct artwork = %v", len(*calls),
			len(*calls) == 2 && bytes.Equal((*calls)[1], iconReadPNG))
	}
}
