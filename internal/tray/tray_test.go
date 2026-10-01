package tray

import (
	"bytes"
	"image/png"
	"testing"
)

// TestEmbeddedIconIsValidPNG is the runnable check that the tray badge
// embedded at build time decodes as a PNG and stays small enough to send over
// D-Bus on every property update.
func TestEmbeddedIconIsValidPNG(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		t.Fatalf("embedded icon is not a valid PNG: %v", err)
	}
	if got := img.Bounds().Dx(); got != 22 {
		t.Errorf("icon width = %d, want 22", got)
	}
	if got := img.Bounds().Dy(); got != 22 {
		t.Errorf("icon height = %d, want 22", got)
	}
	if len(iconPNG) > 2048 {
		t.Errorf("icon payload is %d bytes, want <= 2048", len(iconPNG))
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
